package main

import (
	"argus/bc"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Frame is a decoded frame from the sub stream (YUV 4:2:0).
type Frame struct {
	W, H int
	YUV  []byte
	At   time.Time
}

// CamState is what the status API shows.
type CamState struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	MainOK     bool      `json:"main_ok"`
	SubOK      bool      `json:"sub_ok"`
	MainErr    string    `json:"main_error,omitempty"`
	SubErr     string    `json:"sub_error,omitempty"`
	Width      int       `json:"width"`
	Height     int       `json:"height"`
	Motion     bool      `json:"motion"`
	LastFrame  time.Time `json:"last_frame"`
	LastMotion time.Time `json:"last_motion"`
	MainVia    string    `json:"main_via"` // the method the stream is fetched with right now (bc/flv/rtsp)
	SubVia     string    `json:"sub_via"`
}

type CamRunner struct {
	cam *Camera
	cfg *Config
	ev  *EventManager

	mu    sync.Mutex
	state CamState
	last  *Frame

	gateMu   sync.Mutex
	lastConn time.Time

	protoMu sync.Mutex
	pstate  map[string]*protoState // fallback methods per stream (failover.go)

	procMu sync.Mutex
	procs  map[string]*exec.Cmd // running ffmpeg per stream
}

func (r *CamRunner) setProc(stream string, c *exec.Cmd) {
	r.procMu.Lock()
	if r.procs == nil {
		r.procs = map[string]*exec.Cmd{}
	}
	r.procs[stream] = c
	r.procMu.Unlock()
}

// qualityLoop: Reolink now and then delivers a "bad" connection with broken frames (seen after reconnects).
// Every 5 minutes 10 s of the latest complete segment is decoded; if there are errors, the stream reconnects.
func (r *CamRunner) qualityLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Minute):
		}
		for _, s := range []string{"main", "sub"} {
			f := lastCompleteFile(r.segDir(s))
			if f == "" {
				continue
			}
			n := decodeErrors(ctx, f, 10)
			if n > 10 {
				log.Printf("%s/%s: %d decoding errors in 10 s – reconnecting", r.cam.ID, s, n)
				r.procMu.Lock()
				if c := r.procs[s]; c != nil && c.Process != nil {
					c.Process.Kill()
				}
				r.procMu.Unlock()
			}
		}
	}
}

func decodeErrors(ctx context.Context, file string, secs int) int {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, "nice", "-n", "10", "ffmpeg", "-nostdin", "-loglevel", "repeat+error",
		"-threads", "1", "-t", strconv.Itoa(secs), "-i", file, "-f", "null", "-").CombinedOutput()
	n := 0
	for _, l := range strings.Split(string(out), "\n") {
		if strings.Contains(l, "@") && !strings.Contains(l, "dts") {
			n++
		}
	}
	return n
}

func lastCompleteFile(dir string) string {
	d := filepath.Join(dir, time.Now().UTC().Format("20060102"))
	ents, err := os.ReadDir(d)
	if err != nil || len(ents) < 2 {
		return ""
	}
	e := ents[len(ents)-2]
	if fi, err := e.Info(); err != nil || time.Since(fi.ModTime()) > 3*time.Minute {
		return ""
	}
	return filepath.Join(d, e.Name())
}

// gate: Reolink mixes up the main and sub streams if two connections are opened almost at the same time.
// Therefore connections to the same camera are opened at least 4 s apart.
func (r *CamRunner) gate(ctx context.Context) {
	r.gateMu.Lock()
	defer r.gateMu.Unlock()
	if d := time.Until(r.lastConn.Add(4 * time.Second)); d > 0 {
		select {
		case <-ctx.Done():
		case <-time.After(d):
		}
	}
	r.lastConn = time.Now()
}

func newCamRunner(cam *Camera, cfg *Config, ev *EventManager) *CamRunner {
	return &CamRunner{cam: cam, cfg: cfg, ev: ev, state: CamState{ID: cam.ID, Name: cam.Name}}
}

func (r *CamRunner) State() CamState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

func (r *CamRunner) LastFrame() *Frame {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last
}

func (r *CamRunner) set(f func(s *CamState)) {
	r.mu.Lock()
	f(&r.state)
	r.mu.Unlock()
}

func (r *CamRunner) segDir(stream string) string {
	return filepath.Join(store.Active(), "rec", r.cam.ID, stream)
}

// ensureDirs creates the day folders for today and tomorrow (ffmpeg does not create folders itself).
func (r *CamRunner) ensureDirs() {
	now := time.Now().UTC()
	for _, s := range []string{"main", "sub"} {
		for _, d := range []time.Time{now, now.Add(24 * time.Hour)} {
			os.MkdirAll(filepath.Join(r.segDir(s), d.Format("20060102")), 0o755)
		}
	}
}

func (r *CamRunner) Run(ctx context.Context) {
	r.ensureDirs()
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				r.ensureDirs()
			}
		}
	}()
	r.initProtos()
	go r.preferLoop(ctx, "main")
	go r.preferLoop(ctx, "sub")
	go r.qualityLoop(ctx)
	go r.loop(ctx, "main", r.runMain)
	r.loop(ctx, "sub", r.runSub)
}

// loop restarts a stream when it drops, with increasing waiting time.
func (r *CamRunner) loop(ctx context.Context, stream string, run func(context.Context) error) {
	wait := 5 * time.Second
	lastErr := ""
	var failingSince time.Time
	fastFails := 0
	for ctx.Err() == nil {
		// planned switch back to the preferred method?
		r.protoMu.Lock()
		if ps := r.pstate[stream]; ps.pending >= 0 {
			ps.idx, ps.pending = ps.pending, -1
		}
		r.protoMu.Unlock()
		r.setVia(stream)
		start := time.Now()
		rememberTimer := time.AfterFunc(stableRun, func() { r.markWorking(stream) }) // runs stably → remember the method
		err := run(ctx)
		rememberTimer.Stop()
		if ctx.Err() != nil {
			return
		}
		ran := time.Since(start)
		r.protoMu.Lock()
		planned := r.pstate[stream].pending >= 0 // we stopped the stream ourselves to switch method
		r.protoMu.Unlock()
		if planned {
			lastErr = ""
			continue
		}
		if ran >= stableRun {
			failingSince, fastFails = time.Time{}, 0
		} else {
			if failingSince.IsZero() {
				failingSince = start
			}
			if ran < fastFail {
				fastFails++
			} else {
				fastFails = 0
			}
			r.protoMu.Lock()
			n := len(r.pstate[stream].list)
			r.protoMu.Unlock()
			if n > 1 && (time.Since(failingSince) >= switchAfter || fastFails >= fastFailLimit) {
				reason := "could not run for 5 min"
				if fastFails >= fastFailLimit {
					reason = "fails immediately"
				}
				msg := "stopped"
				if err != nil {
					msg = err.Error()
				}
				if msg != lastErr {
					log.Printf("%s/%s: %s", r.cam.ID, stream, msg)
				}
				r.switchProto(stream, -1, reason)
				failingSince, fastFails = time.Now(), 0
				lastErr = ""
				r.set(func(s *CamState) {
					if stream == "main" {
						s.MainOK, s.MainErr = false, "switching method"
					} else {
						s.SubOK, s.SubErr = false, "switching method"
					}
				})
				select {
				case <-ctx.Done():
					return
				case <-time.After(2 * time.Second):
				}
				continue
			}
		}
		msg := "stopped"
		if err != nil {
			msg = err.Error()
		}
		r.set(func(s *CamState) {
			if stream == "main" {
				s.MainOK, s.MainErr = false, msg
			} else {
				s.SubOK, s.SubErr = false, msg
			}
		})
		r.ensureDirs()      // the day folders may have been deleted from outside (e.g. cleaned up on the NAS)
		if msg != lastErr { // log only when the error changes (a switched-off camera can be gone for days)
			log.Printf("%s/%s: %s – retrying", r.cam.ID, stream, msg)
			lastErr = msg
		}
		if time.Since(start) > 2*time.Minute {
			wait = 5 * time.Second
		} else if wait < time.Minute {
			wait *= 2
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

var ffmpegEnv = append(os.Environ(), "TZ=UTC")

func inputArgs(u string) []string {
	base := []string{"-nostdin", "-hide_banner", "-loglevel", "error"}
	if strings.HasPrefix(u, "rtsp:") || strings.HasPrefix(u, "rtsps:") {
		return append(base, "-rtsp_transport", "tcp", "-timeout", "10000000", "-i", u)
	}
	return append(base, "-rw_timeout", "10000000", "-i", u)
}

func (r *CamRunner) segmentArgs(stream string) []string {
	return []string{"-map", "0:v:0", "-c", "copy", "-f", "segment",
		"-segment_time", "60", "-segment_atclocktime", "1", "-reset_timestamps", "1",
		"-strftime", "1", "-segment_format", "mpegts",
		filepath.Join(r.segDir(stream), "%Y%m%d", "%H%M%S.ts")}
}

// lastLine keeps ffmpeg's last error line without leaking the password.
type lastLine struct {
	mu   sync.Mutex
	line string
	cam  *Camera
}

func (l *lastLine) Write(p []byte) (int, error) {
	s := strings.TrimSpace(string(p))
	if s != "" {
		s = hidePass(s, l.cam.pass)
		if i := strings.LastIndex(s, "\n"); i >= 0 {
			s = s[i+1:]
		}
		l.mu.Lock()
		l.line = s
		l.mu.Unlock()
	}
	return len(p), nil
}

// hidePass removes a password (also URL-encoded) from a text, e.g. an ffmpeg error with the camera's address.
func hidePass(s, pass string) string {
	if pass == "" {
		return s
	}
	for _, p := range []string{pass, url.QueryEscape(pass), url.PathEscape(pass), url.UserPassword("x", pass).String()[2:]} {
		if p != "" {
			s = strings.ReplaceAll(s, p, "***")
		}
	}
	return s
}

func (l *lastLine) err(e error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.line != "" {
		return fmt.Errorf("%s", l.line)
	}
	return e
}

// liveDir: short HLS segments in RAM for live view (same connection as the recording, no re-encoding).
func liveDir(cam, stream string) string { return filepath.Join("/dev/shm/argus/live", cam, stream) }

func (r *CamRunner) liveArgs(stream string) []string {
	d := liveDir(r.cam.ID, stream)
	os.RemoveAll(d)
	os.MkdirAll(d, 0o755)
	return []string{"-map", "0:v:0", "-c", "copy", "-f", "hls", "-hls_time", "1", "-hls_list_size", "6", "-hls_delete_threshold", "6",
		"-hls_flags", "delete_segments+independent_segments+temp_file",
		"-hls_segment_filename", filepath.Join(d, "%d.ts"), filepath.Join(d, "index.m3u8")}
}

// bcInput fetches the stream with Reolink's own protocol (package bc) and delivers MPEG-TS to ffmpeg on stdin.
// Waits until the camera has reported the frame size. stop() closes the connection cleanly; bcErr() gives the reason if it died.
func (r *CamRunner) bcInput(ctx context.Context, stream string) (args []string, stdin io.Reader, w, h int, stop func(), bcErr func() error, err error) {
	st := "mainStream"
	if stream == "sub" {
		st = "subStream"
	}
	cctx, cancel := context.WithCancel(ctx)
	pr, pw := io.Pipe()
	info := make(chan [2]int, 1)
	stats := &bc.Stats{OnInfo: func(w, h int) {
		select {
		case info <- [2]int{w, h}:
		default:
		}
	}}
	var mu sync.Mutex
	var lastErr error
	done := make(chan struct{})
	mux := bc.NewTSMuxer(pw)
	go func() {
		started := false
		e := bc.Stream(cctx, fmt.Sprintf("%s:%d", r.cam.Host, r.cam.bcPort()), r.cam.user, r.cam.pass, st, func(f bc.Frame) {
			if !started { // ffmpeg must start on a keyframe
				if !f.Key {
					return
				}
				started = true
			}
			if mux.WriteFrame(f) != nil {
				cancel()
			}
		}, stats)
		if e == nil {
			e = io.EOF
		}
		mu.Lock()
		lastErr = e
		mu.Unlock()
		pw.CloseWithError(e)
		close(done)
	}()
	stop = func() { cancel(); pr.Close(); <-done }
	bcErr = func() error {
		mu.Lock()
		defer mu.Unlock()
		if lastErr == io.EOF || errors.Is(lastErr, context.Canceled) {
			return nil
		}
		return lastErr
	}
	select {
	case wh := <-info:
		w, h = wh[0], wh[1]
	case <-done:
		stop()
		if e := bcErr(); e != nil {
			return nil, nil, 0, 0, nil, nil, e
		}
		return nil, nil, 0, 0, nil, nil, errors.New("the camera closed the connection")
	case <-time.After(20 * time.Second):
		stop()
		return nil, nil, 0, 0, nil, nil, errors.New("the camera sent no image size")
	}
	args = []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-f", "mpegts", "-i", "pipe:0"}
	return args, pr, w, h, stop, bcErr, nil
}

func (r *CamRunner) runMain(ctx context.Context) error {
	r.gate(ctx)
	var args []string
	var stdin io.Reader
	var bcErr func() error
	if r.proto("main") == "bc" {
		a, in, _, _, stop, be, err := r.bcInput(ctx, "main")
		if err != nil {
			return err
		}
		defer stop()
		args, stdin, bcErr = a, in, be
	} else {
		args = inputArgs(r.cam.urlFor("main", r.proto("main")))
	}
	args = append(args, r.segmentArgs("main")...)
	args = append(args, r.liveArgs("main")...)
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Env = ffmpegEnv
	cmd.Stdin = stdin
	ll := &lastLine{cam: r.cam}
	cmd.Stderr = ll
	if err := cmd.Start(); err != nil {
		return err
	}
	r.setProc("main", cmd)
	// Watchdog: if nothing is written to the recording for 90 s, restart.
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		var lastSize int64 = -1
		stale := 0
		for {
			select {
			case <-done:
				return
			case <-t.C:
				sz := newestSize(r.segDir("main"))
				if sz > 0 && sz != lastSize {
					stale = 0
					r.set(func(s *CamState) { s.MainOK, s.MainErr = true, "" })
				} else if stale++; stale >= 3 {
					log.Printf("%s/main: no data for 90 s – restarting", r.cam.ID)
					cmd.Process.Kill()
				}
				lastSize = sz
			}
		}
	}()
	err := cmd.Wait()
	close(done)
	if bcErr != nil {
		if e := bcErr(); e != nil {
			return e
		}
	}
	return ll.err(err)
}

// newestSize gives the size of the newest segment file (in today's folder).
func newestSize(dir string) int64 {
	d := filepath.Join(dir, time.Now().UTC().Format("20060102"))
	ents, err := os.ReadDir(d)
	if err != nil || len(ents) == 0 {
		return 0
	}
	fi, err := ents[len(ents)-1].Info()
	if err != nil {
		return 0
	}
	return fi.Size() + fi.ModTime().UnixNano()
}

// lastCompleteSize: the size of the second newest segment (the newest is still being written).
func lastCompleteSize(dir string) int64 {
	d := filepath.Join(dir, time.Now().UTC().Format("20060102"))
	ents, err := os.ReadDir(d)
	if err != nil || len(ents) < 2 {
		return 0
	}
	fi, err := ents[len(ents)-2].Info()
	if err != nil || time.Since(fi.ModTime()) > 3*time.Minute {
		return 0
	}
	return fi.Size()
}

func probeSize(ctx context.Context, u string) (int, int, error) {
	w, h, _, err := probeStream(ctx, u)
	return w, h, err
}

// probeStream: the size and codec (h264, hevc …) of a stream's video.
func probeStream(ctx context.Context, u string) (int, int, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	args := []string{"-v", "error", "-rw_timeout", "10000000"}
	if strings.HasPrefix(u, "rtsp:") || strings.HasPrefix(u, "rtsps:") {
		args = []string{"-v", "error", "-rtsp_transport", "tcp", "-timeout", "10000000"}
	}
	args = append(args, "-select_streams", "v:0", "-show_entries", "stream=width,height,codec_name", "-of", "json", u)
	out, err := exec.CommandContext(ctx, "ffprobe", args...).Output()
	if err != nil {
		return 0, 0, "", fmt.Errorf("the camera is not responding")
	}
	var p struct {
		Streams []struct {
			Width, Height int
			Codec         string `json:"codec_name"`
		}
	}
	json.Unmarshal(out, &p)
	if len(p.Streams) == 0 || p.Streams[0].Width == 0 {
		return 0, 0, "", fmt.Errorf("no video in the sub stream")
	}
	return p.Streams[0].Width, p.Streams[0].Height, p.Streams[0].Codec, nil
}

// runSub records the sub stream and at the same time decodes it for motion analysis – one connection to the camera.
func (r *CamRunner) runSub(ctx context.Context) error {
	r.gate(ctx)
	var w, h int
	var err error
	var bcArgs []string
	var stdin io.Reader
	var bcErr func() error
	if r.proto("sub") == "bc" {
		var stop func()
		bcArgs, stdin, w, h, stop, bcErr, err = r.bcInput(ctx, "sub")
		if err != nil {
			return err
		}
		defer stop()
	} else if w, h, err = probeSize(ctx, r.cam.urlFor("sub", r.proto("sub"))); err != nil {
		return err
	}
	if max(w, h) > 1280 {
		return fmt.Errorf("the sub stream sends full resolution (%dx%d)", w, h)
	}
	// The analysis frame is always scaled to the size we expect – also if the camera later switches stream.
	// select (not fps): the fps filter fills gaps in the timestamps with copies – thousands when Reolink jumps.
	// Timestamps can also jump backwards: then the frame is selected as well (otherwise the filter starves until time catches up).
	vf := fmt.Sprintf("select='isnan(prev_selected_t)+isnan(t)+lt(t\\,prev_selected_t)+gte(t-prev_selected_t\\,%.3f)',scale=%d:%d", 0.95/float64(r.cfg.Motion.FPS), w, h)
	r.set(func(s *CamState) { s.Width, s.Height = w, h })
	var args []string
	if bcArgs != nil {
		args = bcArgs
	} else {
		r.gate(ctx)
		args = inputArgs(r.cam.urlFor("sub", r.proto("sub")))
	}
	args = append(args, r.segmentArgs("sub")...)
	args = append(args, r.liveArgs("sub")...)
	args = append(args, "-map", "0:v:0", "-vf", vf, "-fps_mode", "passthrough",
		"-pix_fmt", "yuv420p", "-f", "rawvideo", "pipe:1")
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Env = ffmpegEnv
	cmd.Stdin = stdin
	ll := &lastLine{cam: r.cam}
	cmd.Stderr = ll
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	r.setProc("sub", cmd)
	motionDet := newMotionDetector(w, h, r.cfg.Motion, func() (float64, float64) { return r.ev.settings.Get(r.cam.ID).motion(r.cfg.Motion) })
	frameSize := w * h * 3 / 2
	br := bufio.NewReaderSize(out, frameSize)

	// Watchdog: no frames for 20 s → kill ffmpeg.
	got := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		check := time.NewTicker(90 * time.Second)
		var lastSubSize int64 = -1
		defer check.Stop()
		for {
			select {
			case <-done:
				return
			case <-check.C:
				// The recording must grow – otherwise ffmpeg has stalled (seen in practice: frames came, files stood still)
				if sz := newestSize(r.segDir("sub")); sz == lastSubSize && sz != 0 {
					log.Printf("%s/sub: recording has stalled – restarting", r.cam.ID)
					cmd.Process.Kill()
					return
				} else {
					lastSubSize = sz
				}
				// A finished sub segment above 15 MB means the camera sends the main stream on sub.
				if sz := lastCompleteSize(r.segDir("sub")); sz > 15<<20 {
					log.Printf("%s/sub: the camera sends the main stream on the sub stream (%d MB/min) – reconnecting", r.cam.ID, sz>>20)
					cmd.Process.Kill()
					return
				}
			case <-got:
			case <-time.After(20 * time.Second):
				log.Printf("%s/sub: no frames for 20 s – restarting", r.cam.ID)
				cmd.Process.Kill()
				return
			}
		}
	}()
	defer close(done)

	first := true
	for {
		buf := make([]byte, frameSize)
		if _, err := io.ReadFull(br, buf); err != nil {
			break
		}
		select {
		case got <- struct{}{}:
		default:
		}
		f := &Frame{W: w, H: h, YUV: buf, At: time.Now()}
		moving := motionDet.Feed(buf[:w*h])
		r.mu.Lock()
		r.last = f
		r.state.SubOK, r.state.SubErr, r.state.LastFrame = true, "", f.At
		r.state.Motion = moving
		if moving {
			r.state.LastMotion = f.At
		}
		r.mu.Unlock()
		if first {
			log.Printf("%s: running (%dx%d)", r.cam.ID, w, h)
			first = false
		}
		r.ev.Feed(r.cam, f, moving)
	}
	werr := cmd.Wait()
	if bcErr != nil {
		if e := bcErr(); e != nil {
			return e
		}
	}
	return ll.err(werr)
}

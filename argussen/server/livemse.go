package main

// Fast live view (about half a second behind the camera instead of 2–3 s with HLS).
//
// The live ffmpeg (the same process that records and writes the HLS for the live view – no extra connection to the
// camera, no re-encoding) also writes fragmented MP4 with one fragment per frame to a pipe. The fragments are kept in
// memory from the newest key frame, and every viewer gets: the init segment (ftyp+moov), the fragments since that key
// frame, and then each new fragment as it comes – over one long HTTP response, which the app feeds to the browser's
// MediaSource. A viewer that cannot keep up skips ahead to the next key frame. HLS stays for everything else (recordings,
// Home Assistant's camera entity) and as the fallback for live.

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// mseArgs: the extra ffmpeg output (on file descriptor 3, see camera.go).
func mseArgs() []string {
	return []string{"-map", "0:v:0", "-c", "copy", "-f", "mp4",
		"-movflags", "frag_every_frame+empty_moov+default_base_moof", "-flush_packets", "1", "pipe:3"}
}

const (
	mseMaxGOP     = 12 << 20 // never keep more than this from one key frame (a camera with very rare key frames)
	mseMaxViewers = 12       // per stream
	mseQueue      = 96       // fragments waiting for one viewer before it skips ahead
)

type mseFrag struct {
	data []byte
	key  bool
}

type mseViewer struct {
	ch      chan mseFrag
	waitKey bool // fell behind: wait for the next key frame
}

// mseHub: one camera stream's fast live view.
type mseHub struct {
	mu      sync.Mutex
	init    []byte // ftyp + moov
	codec   string // e.g. avc1.640028 or hev1.1.6.L150.B0
	gop     []mseFrag
	gopSize int
	viewers map[*mseViewer]struct{}
	gen     int // a new ffmpeg (new init) ends all viewers – they connect again
	frames  int // fragments since the start (for tests and checks)
	keys    int
}

var mseHubs = struct {
	sync.Mutex
	m map[string]*mseHub
}{m: map[string]*mseHub{}}

func mseHubFor(cam, stream string) *mseHub {
	mseHubs.Lock()
	defer mseHubs.Unlock()
	k := cam + "/" + stream
	h := mseHubs.m[k]
	if h == nil {
		h = &mseHub{viewers: map[*mseViewer]struct{}{}}
		mseHubs.m[k] = h
	}
	return h
}

// reset: a new ffmpeg begins – the old viewers are ended (their channel is closed).
func (h *mseHub) reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.init, h.codec, h.gop, h.gopSize = nil, "", nil, 0
	h.gen++
	for v := range h.viewers {
		close(v.ch)
		delete(h.viewers, v)
	}
}

func (h *mseHub) setInit(b []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.init, h.codec = b, mp4Codec(b)
}

func (h *mseHub) add(f mseFrag) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.frames++
	if f.key {
		h.keys++
		h.gop, h.gopSize = h.gop[:0:0], 0
	}
	if h.gopSize+len(f.data) <= mseMaxGOP && (f.key || len(h.gop) > 0) {
		h.gop = append(h.gop, f)
		h.gopSize += len(f.data)
	}
	for v := range h.viewers {
		if v.waitKey && !f.key {
			continue
		}
		select {
		case v.ch <- f:
			v.waitKey = false
		default:
			v.waitKey = true // too slow: drop until the next key frame
		}
	}
}

// join: a new viewer gets the init segment and the fragments since the newest key frame.
func (h *mseHub) join() (*mseViewer, []byte, string, []mseFrag, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.init == nil || len(h.gop) == 0 {
		return nil, nil, "", nil, errors.New("not ready")
	}
	if len(h.viewers) >= mseMaxViewers {
		return nil, nil, "", nil, errors.New("too many viewers")
	}
	v := &mseViewer{ch: make(chan mseFrag, mseQueue)}
	h.viewers[v] = struct{}{}
	return v, h.init, h.codec, append([]mseFrag(nil), h.gop...), nil
}

func (h *mseHub) leave(v *mseViewer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.viewers[v]; ok {
		delete(h.viewers, v)
		close(v.ch)
	}
}

// feedMSE reads ffmpeg's fragmented MP4 and fills the hub. It must never block ffmpeg (the recording uses the same
// process), so it only parses and hands on – the viewers have their own queues.
func feedMSE(r io.Reader, h *mseHub) {
	h.reset()
	br := bufio.NewReaderSize(r, 256<<10)
	var init bytes.Buffer
	var moof []byte
	for {
		typ, box, err := readBox(br)
		if err != nil {
			io.Copy(io.Discard, br) // ffmpeg must never wait for us
			return
		}
		switch typ {
		case "ftyp":
			init.Reset()
			init.Write(box)
		case "moov":
			init.Write(box)
			h.setInit(append([]byte(nil), init.Bytes()...))
		case "moof":
			moof = box
		case "mdat":
			if moof != nil {
				f := mseFrag{data: append(moof, box...), key: moofKey(moof)}
				moof = nil
				h.add(f)
			}
		}
	}
}

// readBox reads one top-level MP4 box (header included).
func readBox(r io.Reader) (string, []byte, error) {
	var hdr [8]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return "", nil, err
	}
	size := uint64(binary.BigEndian.Uint32(hdr[:4]))
	typ := string(hdr[4:8])
	head := hdr[:]
	if size == 1 {
		var ext [8]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return "", nil, err
		}
		size = binary.BigEndian.Uint64(ext[:])
		head = append(append([]byte{}, hdr[:]...), ext[:]...)
	}
	if size < uint64(len(head)) || size > 64<<20 {
		return "", nil, fmt.Errorf("mp4: bad box %q (%d bytes)", typ, size)
	}
	b := make([]byte, size)
	copy(b, head)
	if _, err := io.ReadFull(r, b[len(head):]); err != nil {
		return "", nil, err
	}
	return typ, b, nil
}

// children: the boxes inside a box's payload.
func children(payload []byte) map[string][]byte {
	out := map[string][]byte{}
	for len(payload) >= 8 {
		size := int(binary.BigEndian.Uint32(payload[:4]))
		if size < 8 || size > len(payload) {
			break
		}
		if _, ok := out[string(payload[4:8])]; !ok {
			out[string(payload[4:8])] = payload[8:size]
		}
		payload = payload[size:]
	}
	return out
}

// moofKey: whether a fragment starts with a key frame (the first sample's "non-sync" flag is not set).
func moofKey(moof []byte) bool {
	traf := children(children(moof[8:])["traf"])
	tfhd, trun := traf["tfhd"], traf["trun"]
	if len(trun) < 8 {
		return false
	}
	var flags uint32
	have := false
	if len(tfhd) >= 8 {
		tf := binary.BigEndian.Uint32(tfhd[:4]) & 0xffffff
		off := 8                                          // version/flags + track id
		for _, b := range []uint32{0x1, 0x2, 0x8, 0x10} { // base offset (8), description (4), duration (4), size (4)
			if tf&b != 0 {
				if b == 0x1 {
					off += 8
				} else {
					off += 4
				}
			}
		}
		if tf&0x20 != 0 && len(tfhd) >= off+4 {
			flags, have = binary.BigEndian.Uint32(tfhd[off:off+4]), true
		}
	}
	tr := binary.BigEndian.Uint32(trun[:4]) & 0xffffff
	off := 8 // version/flags + sample count
	if tr&0x1 != 0 {
		off += 4 // data offset
	}
	if tr&0x4 != 0 && len(trun) >= off+4 {
		flags, have = binary.BigEndian.Uint32(trun[off:off+4]), true
	} else if tr&0x400 != 0 {
		o := off
		if tr&0x100 != 0 {
			o += 4
		}
		if tr&0x200 != 0 {
			o += 4
		}
		if len(trun) >= o+4 {
			flags, have = binary.BigEndian.Uint32(trun[o:o+4]), true
		}
	}
	return have && flags&0x00010000 == 0
}

// mp4Codec: the codec string the browser's MediaSource wants, from the avcC or hvcC box in the init segment.
func mp4Codec(init []byte) string {
	if i := bytes.Index(init, []byte("avcC")); i >= 0 && len(init) >= i+8 {
		c := init[i+4:]
		return fmt.Sprintf("avc1.%02x%02x%02x", c[1], c[2], c[3])
	}
	if i := bytes.Index(init, []byte("hvcC")); i >= 0 && len(init) >= i+4+13 {
		c := init[i+4:]
		space := []string{"", "A", "B", "C"}[c[1]>>6]
		tier := "L"
		if c[1]&0x20 != 0 {
			tier = "H"
		}
		profile := c[1] & 0x1f
		compat := binary.BigEndian.Uint32(c[2:6])
		var rev uint32 // the compatibility flags in reverse bit order
		for b := 0; b < 32; b++ {
			if compat&(1<<b) != 0 {
				rev |= 1 << (31 - b)
			}
		}
		cons := c[6:12]
		last := len(cons)
		for last > 0 && cons[last-1] == 0 {
			last--
		}
		var cs []string
		for _, x := range cons[:last] {
			cs = append(cs, fmt.Sprintf("%X", x))
		}
		s := fmt.Sprintf("hev1.%s%d.%X.%s%d", space, profile, rev, tier, c[12])
		if len(cs) > 0 {
			s += "." + strings.Join(cs, ".")
		}
		return s
	}
	return ""
}

// liveMSE: GET /api/live/{cam}/{stream}/stream.mp4 – the fast live view (one long response). The codec is in the
// X-Codec header.
func (s *Server) liveMSE(w http.ResponseWriter, r *http.Request) {
	c := s.cam(w, r)
	st := r.PathValue("stream")
	if c == nil || !validStream(st) {
		if c != nil {
			http.NotFound(w, r)
		}
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		jsonErr(w, 500, "streaming is not possible here")
		return
	}
	h := mseHubFor(c.cam.ID, st)
	v, init, codec, gop, err := h.join()
	if err != nil {
		jsonErr(w, 503, err.Error())
		return
	}
	defer h.leave(v)
	// A viewer whose connection hangs (open, but nothing gets through) must not keep its place for ever:
	// each write must get through within 20 s.
	rc := http.NewResponseController(w)
	deadline := func() { rc.SetWriteDeadline(time.Now().Add(20 * time.Second)) }
	deadline()
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no") // proxies must not collect the response
	w.Header().Set("X-Codec", codec)
	w.Header().Set("Access-Control-Expose-Headers", "X-Codec")
	w.WriteHeader(200)
	if _, err := w.Write(init); err != nil {
		return
	}
	for _, f := range gop {
		if _, err := w.Write(f.data); err != nil {
			return
		}
	}
	fl.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case f, ok := <-v.ch:
			if !ok {
				return // ffmpeg started again – the app connects again
			}
			deadline()
			if _, err := w.Write(f.data); err != nil {
				return
			}
			// send what has come in the meantime in one go
			for more := true; more; {
				select {
				case f2, ok2 := <-v.ch:
					if !ok2 {
						fl.Flush()
						return
					}
					if _, err := w.Write(f2.data); err != nil {
						return
					}
				default:
					more = false
				}
			}
			fl.Flush()
		}
	}
}

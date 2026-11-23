package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// API for the app. Everything under /api requires "Authorization: Bearer <token>" except /api/login.
// Everything else answers 404 without content, so the address does not show any web page.
type Server struct {
	cfg  *Config
	ev   *EventManager
	auth *Auth
	ctx  context.Context
	ha   *haBridge

	storagePaused atomic.Bool // network drive with the storage is not mounted (and there is no local fallback)
	storageLow    atomic.Bool // the disk guard could not free enough space (diskguard.go)

	mu      sync.RWMutex
	cams    map[string]*CamRunner
	ids     []string
	cancels map[string]context.CancelFunc
	dones   map[string]chan struct{}
}

// runner: the running camera (nil if it does not exist or is disabled).
func (s *Server) runner(id string) *CamRunner {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cams[id]
}

// camIDs: the running cameras in the order of the registry.
func (s *Server) camIDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.ids...)
}

// startCam starts a camera (if it is enabled).
func (s *Server) startCam(c *Camera) {
	if !c.Enabled || s.storagePaused.Load() {
		return
	}
	r := newCamRunner(c, s.cfg, s.ev)
	ctx, cancel := context.WithCancel(s.ctx)
	done := make(chan struct{})
	s.mu.Lock()
	s.cams[c.ID], s.cancels[c.ID], s.dones[c.ID] = r, cancel, done
	s.ids = nil
	for _, k := range cams.All() {
		if s.cams[k.ID] != nil {
			s.ids = append(s.ids, k.ID)
		}
	}
	s.mu.Unlock()
	go func() { r.Run(ctx); close(done) }()
}

// stopCam stops a camera and waits until its ffmpeg processes and connections are closed.
func (s *Server) stopCam(id string) {
	s.mu.Lock()
	cancel, done := s.cancels[id], s.dones[id]
	delete(s.cams, id)
	delete(s.cancels, id)
	delete(s.dones, id)
	for i, x := range s.ids {
		if x == id {
			s.ids = append(s.ids[:i], s.ids[i+1:]...)
			break
		}
	}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
		}
	}
}

type ctxKey struct{}

func userOf(r *http.Request) *User { u, _ := r.Context().Value(ctxKey{}).(*User); return u }

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("GET /api/setup", s.getSetup)
	mux.HandleFunc("POST /api/setup", s.postSetup)
	priv := http.NewServeMux()
	priv.HandleFunc("POST /api/logout", s.logout)
	priv.HandleFunc("GET /api/me", s.me)
	priv.HandleFunc("POST /api/password", s.password)
	priv.HandleFunc("GET /api/cams", s.camList)
	priv.HandleFunc("GET /api/status", s.status)
	priv.HandleFunc("GET /api/events", s.events)
	priv.HandleFunc("GET /api/events/{id}/thumb.jpg", s.thumb)
	priv.HandleFunc("GET /api/events/{id}/clip.mp4", s.clip)
	priv.HandleFunc("POST /api/events/{id}/keep", s.keepEvent)
	priv.HandleFunc("DELETE /api/events/{id}", s.deleteEvent)
	priv.HandleFunc("GET /api/cams/{cam}/snapshot.jpg", s.snapshot)
	priv.HandleFunc("GET /api/live/{cam}/{stream}/{file}", s.live)
	priv.HandleFunc("GET /api/play/{cam}/{stream}", s.playlist)
	priv.HandleFunc("GET /api/rec/{cam}/{stream}/{day}/{file}", s.recFile)
	priv.HandleFunc("GET /api/timeline/{cam}", s.timeline)
	priv.HandleFunc("GET /api/settings", s.getSettings)
	priv.HandleFunc("GET /api/log", s.logList)
	priv.HandleFunc("PUT /api/settings/{cam}", s.putSettings)
	priv.HandleFunc("GET /api/mode", s.getMode)
	priv.HandleFunc("GET /api/language", s.getLanguage)
	priv.HandleFunc("GET /api/stream", s.stream)
	priv.HandleFunc("PUT /api/mode", s.putMode)
	s.adminRoutes(priv)
	s.privateRoutes(mux, priv) // only in the private build (private.go)
	mux.Handle("/api/", s.requireUser(priv))
	// The web version (the same screens as the phone) – only directly from the local network/VPN, never through a tunnel.
	web := http.FileServer(http.Dir(s.cfg.WebDir))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if (!lanRequest(r) && !s.isIngress(r) && !s.cfg.WebEverywhere) || s.cfg.WebDir == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		web.ServeHTTP(w, r)
	})
	return cors(mux)
}

// webCSP: the web screens only load their own files (hls.js plays video from blob: and runs a worker from blob:).
const webCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; " +
	"media-src 'self' blob:; connect-src 'self'; worker-src 'self' blob:; object-src 'none'; base-uri 'self'; frame-ancestors 'self'"

// cors: the app's screens run from file:// on the phone. Login uses a token in Authorization (no cookies),
// so it is harmless to allow all origins. As a Home Assistant app, HA's ingress cookie is sent as well – so requests
// that change something must be JSON (a form on another site cannot send that without the browser asking first,
// and that question is answered without permission for cookies).
func cors(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Content-Security-Policy", webCSP)
		}
		// also without content (e.g. "log out everywhere"): a form from another site must never get through
		if strings.HasPrefix(r.URL.Path, "/api/") && (r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch) &&
			!strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
			jsonErr(w, http.StatusUnsupportedMediaType, "the request must be JSON")
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Max-Age", "86400")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}

func (s *Server) requireUser(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := s.ingressUser(r) // as a Home Assistant app: the user is logged in to HA
		if u == nil {
			u = s.auth.User(bearer(r), clientIP(r), s.origin(r))
		}
		// Home Assistant's video component cannot send headers: video addresses (only GET of live, play and rec)
		// may carry the token as ?token=…
		if u == nil && r.Method == http.MethodGet && videoPath(r.URL.Path) {
			u = s.auth.User(r.URL.Query().Get("token"), clientIP(r), s.origin(r))
		}
		if u == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"login required"}`))
			return
		}
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// cam fetches the camera from the path and checks that the user may see it. Unknown and forbidden look the same (404).
func (s *Server) cam(w http.ResponseWriter, r *http.Request) *CamRunner {
	id := r.PathValue("cam")
	c := s.runner(id)
	if c == nil || !userOf(r).CanSee(id) {
		http.NotFound(w, r)
		return nil
	}
	return c
}

func (s *Server) allowed(u *User) []string {
	var out []string
	for _, id := range s.camIDs() {
		if u.CanSee(id) {
			out = append(out, id)
		}
	}
	return out
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req struct{ User, Password, Device string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		jsonErr(w, 400, "invalid request")
		return
	}
	ip := clientIP(r)
	tok, u, err := s.auth.Login(ip, strings.TrimSpace(req.User), req.Password, req.Device)
	if err != nil {
		if googleTest(ip) { // Google's automatic test of the app – not an attempt to break in (google.go)
			log.Printf("Login refused for %q from %s (Google – an automatic test of the app, not an attempt to break in): %v", req.User, ip, err)
			s.auth.Record(Access{Type: "google", UserName: strings.TrimSpace(req.User), IP: ip, Origin: s.origin(r), Device: "Google test"})
		} else {
			log.Printf("Login refused for %q from %s: %v", req.User, ip, err)
			s.auth.Record(Access{Type: "refused", UserName: strings.TrimSpace(req.User), IP: ip, Origin: s.origin(r), Device: req.Device})
		}
		jsonErr(w, 401, err.Error())
		return
	}
	log.Printf("Login: %s from %s (%s)", u.Name, ip, req.Device)
	s.auth.Record(Access{Type: "login", UserName: u.Name, IP: ip, Origin: s.origin(r), Device: req.Device})
	writeJSON(w, map[string]any{"token": tok, "user": u})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	s.auth.Logout(bearer(r))
	log.Printf("Logout: %s from %s", userOf(r).Name, clientIP(r))
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"user": userOf(r), "version": version, "instance": s.instanceID(), "standalone": s.cfg.Standalone})
}

// instanceID: a random id that stays the same for this ArgusSen (the Home Assistant integration recognises it by it).
func (s *Server) instanceID() string {
	id := s.ev.settings.GetKV("instance_id")
	if id == "" {
		b := make([]byte, 8)
		rand.Read(b)
		id = hex.EncodeToString(b)
		s.ev.settings.SetKV("instance_id", id)
	}
	return id
}

type camInfo struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Online     bool      `json:"online"`
	Width      int       `json:"width"`
	Height     int       `json:"height"`
	Motion     bool      `json:"motion"`
	LastMotion time.Time `json:"last_motion"`
	Boxes      []Box     `json:"boxes,omitempty"` // AI hits right now, in the sub stream's coordinates (width×height)
	Rotate     int       `json:"rotate"`
	Pre        int       `json:"pre_seconds"`
	Post       int       `json:"post_seconds"`
	EventsOff  bool      `json:"events_off"` // home mode: no events right now
	Optional   bool      `json:"optional"`   // may be switched off
	Zones      []Zone    `json:"zones"`      // the camera's zones and areas to ignore (for the live view and Home Assistant)
}

func (s *Server) camList(w http.ResponseWriter, r *http.Request) {
	out := []camInfo{}
	admin := userOf(r).Admin // only administrators may see the home mode (it tells whether the family is home)
	for _, id := range s.allowed(userOf(r)) {
		rn := s.runner(id)
		if rn == nil {
			continue
		}
		st := rn.State()
		cs := s.ev.settings.Get(id)
		out = append(out, camInfo{ID: id, Name: st.Name, Online: st.SubOK, Width: st.Width, Height: st.Height,
			Motion: st.Motion, LastMotion: st.LastMotion, Boxes: s.ev.LiveBoxes(id), Rotate: cs.Rotate, Pre: cs.PreSeconds, Post: cs.PostSeconds, EventsOff: admin && s.ev.settings.EventsOff(id), Optional: rn.cam.Optional, Zones: zonesOrEmpty(cs.Zones)})
	}
	writeJSON(w, out)
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	if !userOf(r).Admin {
		http.NotFound(w, r)
		return
	}
	out := []CamState{} // empty list (not null) when there are no cameras
	for _, id := range s.camIDs() {
		if rn := s.runner(id); rn != nil {
			out = append(out, rn.State())
		}
	}
	writeJSON(w, map[string]any{"cameras": out, "time": time.Now(), "version": version, "system": s.sysStats()})
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	before := time.Now().Add(time.Minute)
	if b, err := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64); err == nil {
		before = time.UnixMilli(b)
	}
	cams := s.allowed(u)
	if c := r.URL.Query().Get("cam"); c != "" {
		var want []string
		for _, x := range strings.Split(c, ",") {
			if u.CanSee(x) {
				want = append(want, x)
			}
		}
		cams = want
	}
	if cams == nil {
		cams = []string{}
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	evs, err := s.ev.listEvents(cams, before, limit, r.URL.Query().Get("keep") == "1")
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	if evs == nil {
		evs = []Event{}
	}
	writeJSON(w, evs)
}

func (s *Server) thumb(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	p, cam := s.ev.Thumb(id)
	if p == "" || !userOf(r).CanSee(cam) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, p)
}

func (s *Server) snapshot(w http.ResponseWriter, r *http.Request) {
	c := s.cam(w, r)
	if c == nil {
		return
	}
	f := c.LastFrame()
	if f == nil || time.Since(f.At) > 30*time.Second {
		jsonErr(w, http.StatusServiceUnavailable, "no image")
		return
	}
	jpg, err := frameJPEG(f, 75)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(jpg)
}

var liveFile = regexp.MustCompile(`^(index\.m3u8|[0-9]+\.ts)$`)
var recDay = regexp.MustCompile(`^[0-9]{8}$`)
var recFile = regexp.MustCompile(`^[0-9]{6}\.ts$`)

func validStream(s string) bool { return s == "main" || s == "sub" }

func (s *Server) live(w http.ResponseWriter, r *http.Request) {
	c := s.cam(w, r)
	st, f := r.PathValue("stream"), r.PathValue("file")
	if c == nil || !validStream(st) || !liveFile.MatchString(f) {
		if c != nil {
			http.NotFound(w, r)
		}
		return
	}
	p := filepath.Join(liveDir(c.cam.ID, st), f)
	if strings.HasSuffix(f, ".m3u8") {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-store")
		if suf := tokenSuffix(r); suf != "" { // the segments need the token as well
			b, err := os.ReadFile(p)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			lines := strings.Split(string(b), "\n")
			for i, l := range lines {
				if l != "" && !strings.HasPrefix(l, "#") {
					lines[i] = l + suf
				}
			}
			w.Write([]byte(strings.Join(lines, "\n")))
			return
		}
	} else {
		w.Header().Set("Content-Type", "video/mp2t")
		w.Header().Set("Cache-Control", "private, max-age=60")
	}
	http.ServeFile(w, r, p)
}

func (s *Server) recFile(w http.ResponseWriter, r *http.Request) {
	c := s.cam(w, r)
	st, d, f := r.PathValue("stream"), r.PathValue("day"), r.PathValue("file")
	if c == nil {
		return
	}
	if !validStream(st) || !recDay.MatchString(d) || !recFile.MatchString(f) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeFile(w, r, store.Find(filepath.Join("rec", c.cam.ID, st, d, f)))
}

type seg struct {
	t    time.Time
	path string // cam/stream/day/file
}

// readDirCached: a day folder on the NAS takes ~90 ms to read (1440 files over SMB). The list is kept briefly:
// 60 s for finished days (changed only by the cleanup), 3 s for today (a new file arrives every minute).
var dirCache = struct {
	sync.Mutex
	m map[string]dirCacheEntry
}{m: map[string]dirCacheEntry{}}

type dirCacheEntry struct {
	files []os.DirEntry
	until time.Time
}

func readDirCached(dir string, finished bool) []os.DirEntry {
	now := time.Now()
	dirCache.Lock()
	e, ok := dirCache.m[dir]
	dirCache.Unlock()
	if ok && now.Before(e.until) {
		return e.files
	}
	files, _ := os.ReadDir(dir)
	ttl := 3 * time.Second
	if finished {
		ttl = 60 * time.Second
	}
	dirCache.Lock()
	if len(dirCache.m) > 500 {
		dirCache.m = map[string]dirCacheEntry{}
	}
	dirCache.m[dir] = dirCacheEntry{files, now.Add(ttl)}
	dirCache.Unlock()
	return files
}

// segments finds recorded segments for a camera in a time range.
func (s *Server) segments(cam, stream string, from, to time.Time) []seg {
	var out []seg
	seen := map[string]bool{}
	for _, root := range store.Roots() { // NAS and local fallback (after a NAS outage)
		base := filepath.Join(root, "rec", cam, stream)
		for d := from.Add(-2 * time.Minute).UTC().Truncate(24 * time.Hour); !d.After(to); d = d.Add(24 * time.Hour) {
			day := d.Format("20060102")
			files := readDirCached(filepath.Join(base, day), day < time.Now().UTC().Format("20060102"))
			for _, f := range files {
				t, err := time.Parse("20060102150405", day+strings.TrimSuffix(f.Name(), ".ts"))
				p := cam + "/" + stream + "/" + day + "/" + f.Name()
				if err != nil || t.After(to) || t.Before(from.Add(-2*time.Minute)) || seen[p] || strings.HasSuffix(f.Name(), ".moving") || strings.HasSuffix(f.Name(), ".flyt") {
					continue
				}
				seen[p] = true
				out = append(out, seg{t, p})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].t.Before(out[j].t) })
	// the segment before "from" is only included if it reaches into the time range
	for len(out) > 1 && !out[1].t.After(from) {
		out = out[1:]
	}
	if len(out) > 0 && out[0].t.Add(90*time.Second).Before(from) {
		out = out[1:]
	}
	return out
}

// segDur is the segment's length: until the next segment, at most 90 s (otherwise there is a gap).
func segDur(segs []seg, i int) float64 {
	if i+1 < len(segs) {
		if d := segs[i+1].t.Sub(segs[i].t).Seconds(); d <= 90 {
			return d
		}
		return 60
	}
	if left := time.Since(segs[i].t).Seconds(); left < 60 {
		return max(left, 1)
	}
	return 60
}

func parseRange(r *http.Request, def time.Duration) (time.Time, time.Time) {
	q := r.URL.Query()
	to := time.Now()
	from := to.Add(-def)
	// the app may send decimal numbers (tap on the timeline) – use ParseFloat
	if v, err := strconv.ParseFloat(q.Get("from"), 64); err == nil {
		from = time.UnixMilli(int64(v))
	}
	if v, err := strconv.ParseFloat(q.Get("to"), 64); err == nil {
		to = time.UnixMilli(int64(v))
	}
	if to.Sub(from) > 26*time.Hour {
		from = to.Add(-26 * time.Hour)
	}
	return from, to
}

// playlist makes an HLS playlist: /api/play/<camera>/sub?from=<unix-ms>&to=<unix-ms>
// The first segment may start slightly before "from"; EXT-X-PROGRAM-DATE-TIME tells the app where.
func (s *Server) playlist(w http.ResponseWriter, r *http.Request) {
	c := s.cam(w, r)
	if c == nil {
		return
	}
	stream := strings.TrimSuffix(r.PathValue("stream"), ".m3u8")
	if !validStream(stream) {
		http.NotFound(w, r)
		return
	}
	from, to := parseRange(r, time.Hour)
	segs := s.segments(c.cam.ID, stream, from, to)
	if len(segs) == 0 {
		jsonErr(w, 404, "no recording in that period")
		return
	}
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:90\n#EXT-X-PLAYLIST-TYPE:VOD\n")
	for i, sg := range segs {
		// Each minute file starts its timestamps from zero (-reset_timestamps), so each change is a "new piece".
		// Otherwise the player puts all minutes on top of each other, and seeking in long ranges goes wrong.
		if i > 0 {
			b.WriteString("#EXT-X-DISCONTINUITY\n")
		}
		fmt.Fprintf(&b, "#EXT-X-PROGRAM-DATE-TIME:%s\n#EXTINF:%.3f,\n%s/api/rec/%s%s\n",
			sg.t.UTC().Format("2006-01-02T15:04:05.000Z"), segDur(segs, i), s.ingressPrefix(r), sg.path, tokenSuffix(r))
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(b.String()))
}

// timeline: which time ranges are recorded (sub and main) and the events – for the timeline in the app.
func (s *Server) timeline(w http.ResponseWriter, r *http.Request) {
	c := s.cam(w, r)
	if c == nil {
		return
	}
	from, to := parseRange(r, 24*time.Hour)
	ranges := func(stream string) [][2]int64 {
		out := [][2]int64{}
		segs := s.segments(c.cam.ID, stream, from, to)
		for i, sg := range segs {
			a := sg.t.UnixMilli()
			b := sg.t.Add(time.Duration(segDur(segs, i) * float64(time.Second))).UnixMilli()
			if n := len(out); n > 0 && a-out[n-1][1] < 5000 {
				out[n-1][1] = b
			} else {
				out = append(out, [2]int64{a, b})
			}
		}
		return out
	}
	evs, _ := s.ev.ListEvents([]string{c.cam.ID}, to, 500)
	var inRange []Event
	for _, e := range evs {
		if !e.Start.Before(from) {
			inRange = append(inRange, e)
		}
	}
	if inRange == nil {
		inRange = []Event{}
	}
	writeJSON(w, map[string]any{"from": from.UnixMilli(), "to": to.UnixMilli(),
		"sub": ranges("sub"), "main": ranges("main"), "events": inRange})
}

type camSettingsOut struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	CamSettings
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	if !userOf(r).Admin {
		http.NotFound(w, r)
		return
	}
	out := []camSettingsOut{}
	for _, id := range s.camIDs() {
		if rn := s.runner(id); rn != nil {
			out = append(out, camSettingsOut{ID: id, Name: rn.cam.Name, CamSettings: s.ev.settings.Get(id)})
		}
	}
	writeJSON(w, map[string]any{"cameras": out, "objects": s.ev.settings.allObjects()})
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	id := r.PathValue("cam")
	if !u.Admin || cams.Get(id) == nil {
		http.NotFound(w, r)
		return
	}
	cs := s.ev.settings.Get(id)
	oldZones, _ := json.Marshal(cs.Zones)
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768)).Decode(&cs); err != nil {
		jsonErr(w, 400, "invalid request")
		return
	}
	if err := s.ev.settings.Set(id, cs); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if newZones, _ := json.Marshal(s.ev.settings.Get(id).Zones); string(newZones) != string(oldZones) {
		nz, ni := 0, 0
		for _, z := range s.ev.settings.Get(id).Zones {
			if z.Kind == "zone" {
				nz++
			} else {
				ni++
			}
		}
		log.Printf("Zones for %s changed by %s: %d zones, %d areas to ignore", id, u.Name, nz, ni)
		s.haRefresh("") // Home Assistant gets the zones' entities
	}
	log.Printf("settings for %s changed by %s: sensitivity %d, AI %d %%, objects %v, rotation %d, before/after %d/%d s, off when home %v", id, u.Name, cs.Sensitivity, cs.MinConfidence, cs.Objects, cs.Rotate, cs.PreSeconds, cs.PostSeconds, cs.HomeOff)
	writeJSON(w, camSettingsOut{ID: id, Name: cams.Get(id).Name, CamSettings: s.ev.settings.Get(id)})
}

func (s *Server) password(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	var req struct {
		Old string `json:"old"`
		New string `json:"new"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		jsonErr(w, 400, "invalid request")
		return
	}
	// the same lock-out as login against guessing
	ip := clientIP(r)
	s.auth.mu.Lock()
	locked := 0
	for _, t := range s.auth.fails[ip] {
		if time.Since(t) < 15*time.Minute {
			locked++
		}
	}
	s.auth.mu.Unlock()
	if locked >= 5 {
		jsonErr(w, 429, errLocked.Error())
		return
	}
	if err := s.auth.ChangePassword(u, bearer(r), req.Old, req.New); err != nil {
		if errors.Is(err, errWrongCurrent) {
			s.auth.mu.Lock()
			s.auth.fails[ip] = append(s.auth.fails[ip], time.Now())
			s.auth.mu.Unlock()
		}
		log.Printf("password change refused for %s: %v", u.Name, err)
		jsonErr(w, 400, err.Error())
		return
	}
	log.Printf("%s changed password in the app (other devices logged out)", u.Name)
	writeJSON(w, map[string]bool{"ok": true})
}

// logList: /api/log?before=<id>&q=<filter>&limit=  (admin only)
func (s *Server) logList(w http.ResponseWriter, r *http.Request) {
	if !userOf(r).Admin {
		http.NotFound(w, r)
		return
	}
	before := int64(1 << 62)
	if b, err := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64); err == nil {
		before = b
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	l, err := logDB.list(before, strings.TrimSpace(r.URL.Query().Get("q")), limit)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	writeJSON(w, l)
}

// ---- home mode ----

func (s *Server) modeOut() map[string]any {
	off := []string{}
	for _, id := range s.camIDs() {
		if s.ev.settings.EventsOff(id) {
			off = append(off, id)
		}
	}
	return map[string]any{"home": s.ev.settings.Home(), "events_off": off}
}

func (s *Server) setMode(home bool, who string) {
	if s.ev.settings.Home() != home {
		t := "away"
		if home {
			t = "home"
		}
		log.Printf("Home mode: %s (set by %s)", t, who)
	}
	s.ev.settings.SetHome(home)
}

func (s *Server) getMode(w http.ResponseWriter, r *http.Request) {
	if !userOf(r).Admin { // whether the family is home is for administrators only
		http.NotFound(w, r)
		return
	}
	writeJSON(w, s.modeOut())
}

func (s *Server) putMode(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	if !u.Admin {
		http.NotFound(w, r)
		return
	}
	var req struct{ Home bool }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
		jsonErr(w, 400, "invalid request")
		return
	}
	s.setMode(req.Home, u.Name)
	writeJSON(w, s.modeOut())
}

// ---- protect, delete and download events ----

func (s *Server) eventFor(w http.ResponseWriter, r *http.Request) *Event {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	e, err := s.ev.GetEvent(id)
	if err != nil || !userOf(r).CanSee(e.Cam) {
		jsonErr(w, 404, "the event does not exist")
		return nil
	}
	return e
}

func (s *Server) keepEvent(w http.ResponseWriter, r *http.Request) {
	e := s.eventFor(w, r)
	if e == nil {
		return
	}
	var req struct{ Keep bool }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
		jsonErr(w, 400, "invalid request")
		return
	}
	// everybody who can see the camera may protect an event; only administrators may remove the protection
	// (an unprotected event is deleted by the cleanup)
	if !req.Keep && e.Keep && !userOf(r).Admin {
		jsonErr(w, 403, "only administrators can remove the protection")
		return
	}
	if err := s.ev.SetKeep(e.ID, req.Keep); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	action := "protected"
	if !req.Keep {
		action = "unprotected"
	}
	log.Printf("Event %d (%s %s) %s by %s", e.ID, e.Cam, e.Start.In(localTZ).Format("02-01 15:04"), action, userOf(r).Name)
	writeJSON(w, map[string]any{"id": e.ID, "keep": req.Keep})
}

func (s *Server) deleteEvent(w http.ResponseWriter, r *http.Request) {
	if !userOf(r).Admin {
		jsonErr(w, 403, "only admins can delete events")
		return
	}
	e := s.eventFor(w, r)
	if e == nil {
		return
	}
	if err := s.ev.DeleteEvent(e.ID); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	log.Printf("Event %d (%s %s) deleted by %s", e.ID, e.Cam, e.Start.In(localTZ).Format("02-01 15:04"), userOf(r).Name)
	writeJSON(w, map[string]any{"id": e.ID, "deleted": true})
}

// clip: the event as MP4 (time before/after as in the app), HD if it exists. ?sd=1 forces the sub stream.
// clipSlots: at most 2 clips are made at the same time (each runs ffmpeg for up to 3 minutes).
var clipSlots = make(chan struct{}, 2)

const maxClip = 15 * time.Minute // an event lasts at most 10 minutes; with time before/after this is plenty

func (s *Server) clip(w http.ResponseWriter, r *http.Request) {
	e := s.eventFor(w, r)
	if e == nil {
		return
	}
	select {
	case clipSlots <- struct{}{}:
		defer func() { <-clipSlots }()
	default:
		jsonErr(w, 429, "other clips are being made – try again in a moment")
		return
	}
	cs := s.ev.settings.Get(e.Cam)
	end := e.End
	if end.IsZero() {
		end = time.Now()
	}
	from := e.Start.Add(-time.Duration(cs.PreSeconds) * time.Second)
	to := end.Add(time.Duration(cs.PostSeconds) * time.Second)
	if to.Sub(from) > maxClip {
		to = from.Add(maxClip)
	}
	stream := "main"
	if r.URL.Query().Get("sd") == "1" {
		stream = "sub"
	}
	segs := s.segments(e.Cam, stream, from, to)
	if len(segs) == 0 && stream == "main" {
		stream = "sub"
		segs = s.segments(e.Cam, stream, from, to)
	}
	if len(segs) == 0 {
		jsonErr(w, 404, "the recording is no longer stored")
		return
	}
	dir, err := os.MkdirTemp("", "argus-clip-")
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	defer os.RemoveAll(dir)
	var list strings.Builder
	for _, sg := range segs {
		fmt.Fprintf(&list, "file '%s'\n", store.Find(filepath.Join("rec", sg.path)))
	}
	lf := filepath.Join(dir, "list.txt")
	os.WriteFile(lf, []byte(list.String()), 0o600)
	out := filepath.Join(dir, "clip.mp4")
	off := from.Sub(segs[0].t).Seconds()
	if off < 0 {
		off = 0
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-v", "error", "-f", "concat", "-safe", "0", "-i", lf,
		"-ss", fmt.Sprintf("%.2f", off), "-t", fmt.Sprintf("%.2f", to.Sub(from).Seconds()),
		"-map", "0:v:0", "-c", "copy", "-movflags", "+faststart", "-y", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		log.Printf("clip of event %d failed: %v %s", e.ID, err, strings.TrimSpace(string(b)))
		jsonErr(w, 500, "the clip could not be made")
		return
	}
	camName := e.Cam
	if c := cams.Get(e.Cam); c != nil {
		camName = c.Name
	}
	fileName := fmt.Sprintf("ArgusSen_%s_%s.mp4", camName, e.Start.In(localTZ).Format("2006-01-02_15-04-05"))
	fileName = strings.ReplaceAll(fileName, " ", "_")
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Content-Disposition", `attachment; filename="`+fileName+`"`)
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, out)
}

// localTZ: the time zone for times shown to people (log, clip names). "timezone" in the configuration,
// otherwise the machine's own (TZ). Home Assistant sets TZ for its apps.
var localTZ = time.Local

// lanRequest: the request comes directly from a private network (local network/VPN) and NOT through a tunnel/proxy
// (Cloudflare always sets CF-Connecting-IP / CF-Ray; host names in "public_hosts" also count as from outside).
// publicHosts is set from the configuration ("public_hosts").
var publicHosts []string

func lanRequest(r *http.Request) bool {
	if r.Header.Get("CF-Connecting-IP") != "" || r.Header.Get("CF-Ray") != "" || r.Header.Get("X-Forwarded-For") != "" {
		return false
	}
	host := strings.ToLower(r.Host)
	for _, h := range publicHosts {
		if h != "" && strings.Contains(host, strings.ToLower(h)) {
			return false
		}
	}
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(h)
	return ip != nil && (ip.IsPrivate() || ip.IsLoopback())
}

// videoPath: the addresses that may carry ?token= (Home Assistant's video component cannot send headers).
func videoPath(p string) bool {
	return strings.HasPrefix(p, "/api/live/") || strings.HasPrefix(p, "/api/play/") || strings.HasPrefix(p, "/api/rec/")
}

// tokenSuffix: "?token=…" when the request used one, so the addresses in a playlist get it too.
func tokenSuffix(r *http.Request) string {
	if t := r.URL.Query().Get("token"); t != "" && bearer(r) == "" {
		return "?token=" + url.QueryEscape(t)
	}
	return ""
}

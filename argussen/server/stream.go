package main

// GET /api/stream – changes as they happen (server-sent events), for the Home Assistant integration:
//
//	event: state   {"cams":[{"id","name","online","motion"}], "home": bool (admins), "storage_low": bool}  – at the start
//	event: cam     {"id","name","online","motion"}                                                       – when it changes
//	event: event   an event (as in /api/events) when it starts, gets new objects or ends
//	event: home    {"home": bool}                                                                         – admins only
//	event: storage {"storage_low": bool}
//
// A comment line is sent every 20 seconds so connections through proxies stay open. Each user only gets the cameras
// it may see.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type streamMsg struct {
	kind string
	cam  string // "" = not about one camera
	data []byte
}

type streamHub struct {
	mu   sync.Mutex
	subs map[chan streamMsg]bool
}

var hub = &streamHub{subs: map[chan streamMsg]bool{}}

func (h *streamHub) publish(kind, cam string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.subs {
		select {
		case c <- streamMsg{kind, cam, b}:
		default: // a slow client misses a message rather than blocking ArgusSen
		}
	}
}

// streamEvent: an event with "ongoing" (end is the zero time while it lasts).
type streamEvent struct {
	Event
	Ongoing bool `json:"ongoing"`
}

type camLive struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Online bool   `json:"online"`
	Motion bool   `json:"motion"`
}

func (s *Server) camLiveStates() []camLive {
	out := []camLive{}
	for _, id := range s.camIDs() {
		if rn := s.runner(id); rn != nil {
			st := rn.State()
			out = append(out, camLive{ID: id, Name: rn.cam.Name, Online: st.SubOK, Motion: st.Motion})
		}
	}
	return out
}

// watchForStream sends changes of the cameras, the home mode and the storage once a second.
func (s *Server) watchForStream() {
	prev := map[string]camLive{}
	var prevHome, prevLow bool
	first := true
	for range time.Tick(time.Second) {
		for _, c := range s.camLiveStates() {
			if p, ok := prev[c.ID]; !ok || p != c {
				prev[c.ID] = c
				if !first {
					hub.publish("cam", c.ID, c)
				}
			}
		}
		home, low := s.ev.settings.Home(), s.storageLow.Load()
		if !first && home != prevHome {
			hub.publish("home", "", map[string]bool{"home": home})
		}
		if !first && low != prevLow {
			hub.publish("storage", "", map[string]bool{"storage_low": low})
		}
		prevHome, prevLow, first = home, low, false
	}
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	fl, ok := w.(http.Flusher)
	if !ok {
		jsonErr(w, 500, "streaming is not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	c := make(chan streamMsg, 64)
	hub.mu.Lock()
	hub.subs[c] = true
	hub.mu.Unlock()
	defer func() {
		hub.mu.Lock()
		delete(hub.subs, c)
		hub.mu.Unlock()
	}()
	send := func(kind string, data []byte) bool {
		_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, data)
		fl.Flush()
		return err == nil
	}
	start := map[string]any{"storage_low": s.storageLow.Load()}
	cams := []camLive{}
	for _, c := range s.camLiveStates() {
		if u.CanSee(c.ID) {
			cams = append(cams, c)
		}
	}
	start["cams"] = cams
	if u.Admin {
		start["home"] = s.ev.settings.Home()
	}
	b, _ := json.Marshal(start)
	if !send("state", b) {
		return
	}
	beat := time.NewTicker(20 * time.Second)
	defer beat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-beat.C:
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
				return
			}
			fl.Flush()
		case m := <-c:
			if m.cam != "" && !u.CanSee(m.cam) || m.kind == "home" && !u.Admin {
				continue
			}
			if !send(m.kind, m.data) {
				return
			}
		}
	}
}

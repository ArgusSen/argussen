package main

// PTZ: turning (and zooming) a camera from the live view, and its saved positions (presets) – through ONVIF, so it works
// with Reolink (E1, TrackMix …) and other brands (Hikvision, Dahua, Tapo …) alike.
//
// The app sends "move" again every few hundred milliseconds while a button is held; if that stops (the finger is lifted,
// the connection is lost), ArgusSen stops the camera by itself after a second – a camera never keeps turning.
// Whether a camera can do PTZ is found out the first time the live view asks (and remembered until its settings change).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"
)

type ptzPreset struct {
	Token string `json:"token"`
	Name  string `json:"name"`
}

type ptzCam struct {
	mu      sync.Mutex
	key     string // host, port and login – a change means a new probe
	o       *onvifClient
	addr    string // the PTZ service
	profile string // the media profile with a PTZ configuration
	ok      bool   // the camera can turn
	zoom    bool   // … and zoom
	err     string // why not (shown to administrators)
	checked time.Time
	moving  bool
	dir     [3]float64
	retries int // failed stops in a row (the watchdog tries again)
	stopAt  time.Time
	timer   *time.Timer
}

var ptzCams = struct {
	sync.Mutex
	m map[string]*ptzCam
}{m: map[string]*ptzCam{}}

// ptzPorts: the ONVIF ports to try – the camera's own if set; Reolink uses 8000, most others 80.
func ptzPorts(c *Camera) []int {
	if c.OnvifPort > 0 {
		return []int{c.OnvifPort}
	}
	if c.reolink() {
		return []int{8000, 80}
	}
	return []int{80, 8000}
}

func ptzFor(c *Camera) *ptzCam {
	key := fmt.Sprintf("%s|%v|%s|%s", c.Host, ptzPorts(c), c.user, c.pass)
	ptzCams.Lock()
	defer ptzCams.Unlock()
	p := ptzCams.m[c.ID]
	if p == nil || p.key != key {
		p = &ptzCam{key: key}
		ptzCams.m[c.ID] = p
	}
	return p
}

// probe finds the PTZ service and profile (once; a failed probe is tried again after 10 minutes).
func (p *ptzCam) probe(ctx context.Context, c *Camera) {
	if p.ok || (!p.checked.IsZero() && time.Since(p.checked) < 10*time.Minute) {
		return
	}
	p.checked = time.Now()
	p.err = "the camera does not answer ONVIF"
	for _, port := range ptzPorts(c) {
		o := newOnvif(c.Host, port, c.user, c.pass)
		if o.syncClock(ctx) != nil {
			continue
		}
		var caps struct {
			PTZ   string `xml:"Body>GetCapabilitiesResponse>Capabilities>PTZ>XAddr"`
			Media string `xml:"Body>GetCapabilitiesResponse>Capabilities>Media>XAddr"`
		}
		if err := o.call(ctx, o.xaddr, `<tds:GetCapabilities><tds:Category>All</tds:Category></tds:GetCapabilities>`, true, &caps); err != nil {
			p.err = err.Error()
			continue
		}
		ptzAddr, media := fixHost(strings.TrimSpace(caps.PTZ), c.Host), fixHost(strings.TrimSpace(caps.Media), c.Host)
		if ptzAddr == "" || media == "" {
			p.err = "the camera cannot turn (no ONVIF PTZ)"
			return
		}
		var prof struct {
			Profiles []struct {
				Token string `xml:"token,attr"`
				PTZ   *struct {
					Token string `xml:"token,attr"`
				} `xml:"PTZConfiguration"`
			} `xml:"Body>GetProfilesResponse>Profiles"`
		}
		if err := o.call(ctx, media, `<trt:GetProfiles/>`, true, &prof); err != nil {
			p.err = err.Error()
			return
		}
		for _, pr := range prof.Profiles {
			if pr.PTZ != nil {
				p.profile = pr.Token
				break
			}
		}
		if p.profile == "" {
			p.err = "the camera cannot turn (no PTZ profile)"
			return
		}
		var nodes struct {
			Zoom []struct{} `xml:"Body>GetNodesResponse>PTZNode>SupportedPTZSpaces>ContinuousZoomVelocitySpace"`
		}
		o.call(ctx, ptzAddr, `<tptz:GetNodes/>`, true, &nodes)
		p.o, p.addr, p.ok, p.zoom, p.err = o, ptzAddr, true, len(nodes.Zoom) > 0, ""
		return
	}
}

func clampUnit(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return math.Max(-1, math.Min(1, v))
}

// move turns the camera (x: right +, y: up +, z: zoom in +; −1…1) until stop, or for at most a second unless it is
// called again.
func (p *ptzCam) move(ctx context.Context, x, y, z float64) error {
	d := [3]float64{clampUnit(x), clampUnit(y), clampUnit(z)}
	if !p.zoom {
		d[2] = 0
	}
	p.stopAt = time.Now().Add(time.Second)
	p.retries = 0
	if p.timer == nil {
		p.timer = time.AfterFunc(time.Second, p.watchdog)
	} else {
		p.timer.Reset(time.Second)
	}
	if p.moving && p.dir == d {
		return nil // already moving that way – only the watchdog is renewed
	}
	vel := fmt.Sprintf(`<tt:PanTilt x="%.2f" y="%.2f"/>`, d[0], d[1])
	if p.zoom {
		vel += fmt.Sprintf(`<tt:Zoom x="%.2f"/>`, d[2])
	}
	body := `<tptz:ContinuousMove><tptz:ProfileToken>` + xmlEscape(p.profile) + `</tptz:ProfileToken><tptz:Velocity>` + vel +
		`</tptz:Velocity></tptz:ContinuousMove>`
	if err := p.o.call(ctx, p.addr, body, true, &struct{}{}); err != nil {
		return err
	}
	p.moving, p.dir = true, d
	return nil
}

func (p *ptzCam) watchdog() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.moving && time.Now().After(p.stopAt.Add(-50*time.Millisecond)) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := p.stop(ctx); err != nil && p.retries < 3 {
			// the camera did not get the stop (e.g. a lost wifi packet): try again in a second, so it never keeps turning
			p.retries++
			p.moving, p.stopAt = true, time.Now()
			p.timer.Reset(time.Second)
		}
	}
}

func (p *ptzCam) stop(ctx context.Context) error {
	p.moving, p.dir = false, [3]float64{}
	body := `<tptz:Stop><tptz:ProfileToken>` + xmlEscape(p.profile) + `</tptz:ProfileToken><tptz:PanTilt>true</tptz:PanTilt><tptz:Zoom>true</tptz:Zoom></tptz:Stop>`
	return p.o.call(ctx, p.addr, body, true, &struct{}{})
}

func (p *ptzCam) presets(ctx context.Context) ([]ptzPreset, error) {
	var r struct {
		P []struct {
			Token string `xml:"token,attr"`
			Name  string `xml:"Name"`
		} `xml:"Body>GetPresetsResponse>Preset"`
	}
	if err := p.o.call(ctx, p.addr, `<tptz:GetPresets><tptz:ProfileToken>`+xmlEscape(p.profile)+`</tptz:ProfileToken></tptz:GetPresets>`, true, &r); err != nil {
		return nil, err
	}
	out := []ptzPreset{}
	for _, x := range r.P {
		if name := strings.TrimSpace(x.Name); x.Token != "" && name != "" {
			out = append(out, ptzPreset{x.Token, name})
		}
	}
	return out, nil
}

func (p *ptzCam) gotoPreset(ctx context.Context, token string) error {
	p.moving = false
	return p.o.call(ctx, p.addr, `<tptz:GotoPreset><tptz:ProfileToken>`+xmlEscape(p.profile)+`</tptz:ProfileToken><tptz:PresetToken>`+
		xmlEscape(token)+`</tptz:PresetToken></tptz:GotoPreset>`, true, &struct{}{})
}

func (p *ptzCam) savePreset(ctx context.Context, name string) error {
	return p.o.call(ctx, p.addr, `<tptz:SetPreset><tptz:ProfileToken>`+xmlEscape(p.profile)+`</tptz:ProfileToken><tptz:PresetName>`+
		xmlEscape(name)+`</tptz:PresetName></tptz:SetPreset>`, true, &struct{}{})
}

func (p *ptzCam) removePreset(ctx context.Context, token string) error {
	return p.o.call(ctx, p.addr, `<tptz:RemovePreset><tptz:ProfileToken>`+xmlEscape(p.profile)+`</tptz:ProfileToken><tptz:PresetToken>`+
		xmlEscape(token)+`</tptz:PresetToken></tptz:RemovePreset>`, true, &struct{}{})
}

// getPTZ: GET /api/cams/{cam}/ptz – whether the camera can turn and zoom, and its saved positions.
func (s *Server) getPTZ(w http.ResponseWriter, r *http.Request) {
	c := s.cam(w, r)
	if c == nil {
		return
	}
	p := ptzFor(c.cam)
	p.mu.Lock()
	defer p.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	p.probe(ctx, c.cam)
	out := map[string]any{"ptz": p.ok, "zoom": p.zoom, "presets": []ptzPreset{}}
	if !p.ok {
		if userOf(r).Admin {
			out["error"] = p.err
		}
		writeJSON(w, out)
		return
	}
	if ps, err := p.presets(ctx); err == nil {
		out["presets"] = ps
	}
	writeJSON(w, out)
}

// postPTZ: POST /api/cams/{cam}/ptz – {"op":"move","x":…,"y":…,"z":…}, {"op":"stop"}, {"op":"goto","preset":…};
// administrators also {"op":"save","name":…} and {"op":"remove","preset":…}. Everyone who may see the camera may turn it.
func (s *Server) postPTZ(w http.ResponseWriter, r *http.Request) {
	c := s.cam(w, r)
	if c == nil {
		return
	}
	var req struct {
		Op, Preset, Name string
		X, Y, Z          float64
	}
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&raw); err != nil {
		jsonErr(w, 400, "invalid request")
		return
	}
	num := func(k string) float64 {
		var v float64
		json.Unmarshal(raw[k], &v)
		return v
	}
	str := func(k string) string {
		var v string
		json.Unmarshal(raw[k], &v)
		return strings.TrimSpace(v)
	}
	req.Op, req.X, req.Y, req.Z, req.Preset, req.Name = str("op"), num("x"), num("y"), num("z"), str("preset"), str("name")
	admin := userOf(r).Admin
	if (req.Op == "save" || req.Op == "remove") && !admin {
		jsonErr(w, 403, "only administrators can change the saved positions")
		return
	}
	p := ptzFor(c.cam)
	p.mu.Lock()
	defer p.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	p.probe(ctx, c.cam)
	if !p.ok {
		jsonErr(w, 409, "the camera cannot turn")
		return
	}
	var err error
	switch req.Op {
	case "move":
		err = p.move(ctx, req.X, req.Y, req.Z)
	case "stop":
		err = p.stop(ctx)
	case "goto":
		err = p.gotoPreset(ctx, req.Preset)
	case "save":
		if req.Name == "" || len([]rune(req.Name)) > 30 {
			jsonErr(w, 400, "give the position a name (at most 30 characters)")
			return
		}
		err = p.savePreset(ctx, req.Name)
	case "remove":
		err = p.removePreset(ctx, req.Preset)
	default:
		err = errors.New("unknown PTZ command")
	}
	if err != nil {
		jsonErr(w, 502, err.Error())
		return
	}
	out := map[string]any{"ok": true}
	if req.Op == "save" || req.Op == "remove" {
		if ps, e := p.presets(ctx); e == nil {
			out["presets"] = ps
		}
	}
	writeJSON(w, out)
}

package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// CamSettings can be changed from the app (admin only) and take effect immediately.
type CamSettings struct {
	Sensitivity   int      `json:"sensitivity"`    // motion 1 (a little) – 10 (very sensitive), default 5
	MinConfidence int      `json:"min_confidence"` // AI confidence in percent (30–95)
	Objects       []string `json:"objects"`        // objects that create events (person, car, van, bicycle, motion)
	Rotate        int      `json:"rotate"`         // shown in the app: 0 or 180 degrees
	PreSeconds    int      `json:"pre_seconds"`    // included in clips/kept in full resolution before the event (0–60)
	PostSeconds   int      `json:"post_seconds"`   // … and after (0–120)
	HomeOff       bool     `json:"home_off"`       // no events while home mode is on (indoor cameras)
	Zones         []Zone   `json:"zones"`          // up to 3 zones and 3 areas to ignore (zones.go)
}

type Settings struct {
	db   *sql.DB
	cfg  *Config
	mu   sync.RWMutex
	m    map[string]CamSettings
	home bool
	// the zones as block masks per camera and size – made once, dropped when the camera's settings change
	maskCache map[string]*camMasks
}

// masks: the camera's zones as masks of bw × bh motion blocks (nil when the camera has no zones).
func (s *Settings) masks(cam string, bw, bh int) *camMasks {
	key := cam + "/" + strconv.Itoa(bw) + "x" + strconv.Itoa(bh)
	s.mu.RLock()
	m, ok := s.maskCache[key]
	s.mu.RUnlock()
	if ok {
		return m
	}
	zones := s.Get(cam).Zones
	if len(zones) > 0 {
		m = buildMasks(zones, bw, bh)
	}
	s.mu.Lock()
	if s.maskCache == nil {
		s.maskCache = map[string]*camMasks{}
	}
	s.maskCache[key] = m
	s.mu.Unlock()
	return m
}

// aiObjects: the objects the AI can find (person, car, van, bicycle).
func (s *Settings) aiObjects() []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range s.cfg.Labels {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// allObjects: what can be chosen per camera – the AI objects (only when there is an AI) and "motion"
// (an event from motion alone, also without AI).
func (s *Settings) allObjects() []string {
	if s.cfg.BlueOnyx == "" {
		return []string{"motion"}
	}
	return append(s.aiObjects(), "motion")
}

func (s *Settings) defaults() CamSettings {
	objs := s.aiObjects()
	if s.cfg.BlueOnyx == "" {
		objs = []string{"motion"} // without AI: events from motion
	}
	return CamSettings{Sensitivity: 5, MinConfidence: int(math.Round(s.cfg.MinConf * 100)), Objects: objs,
		PreSeconds: s.cfg.PreSec, PostSeconds: s.cfg.PostSec}
}

func newSettings(db *sql.DB, cfg *Config) (*Settings, error) {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS cam_settings(cam TEXT PRIMARY KEY, json TEXT NOT NULL)`); err != nil {
		return nil, err
	}
	s := &Settings{db: db, cfg: cfg, m: map[string]CamSettings{}}
	s.initMode()
	rows, err := db.Query(`SELECT cam, json FROM cam_settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var cam, js string
		rows.Scan(&cam, &js)
		cs := s.camDefaults(cam) // fields missing in the stored row get the configuration's default
		if json.Unmarshal([]byte(js), &cs) == nil {
			s.m[cam] = cs
		}
	}
	return s, nil
}

func (s *Settings) Get(cam string) CamSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if cs, ok := s.m[cam]; ok {
		return cs
	}
	return s.camDefaults(cam)
}

func (s *Settings) camDefaults(cam string) CamSettings {
	d := s.defaults()
	for _, c := range cams.All() {
		if c.ID == cam {
			d.Rotate = c.Rotate
			d.HomeOff = c.HomeOff
		}
	}
	return d
}

func (s *Settings) Set(cam string, cs CamSettings) error {
	if cs.Sensitivity < 1 || cs.Sensitivity > 10 {
		return fmt.Errorf("sensitivity must be 1–10")
	}
	if cs.PreSeconds < 0 || cs.PreSeconds > 60 || cs.PostSeconds < 0 || cs.PostSeconds > 120 {
		return fmt.Errorf("time before must be 0–60 s and time after 0–120 s")
	}
	if cs.Rotate != 0 && cs.Rotate != 180 {
		return fmt.Errorf("rotation must be 0 or 180")
	}
	if cs.MinConfidence < 30 || cs.MinConfidence > 95 {
		return fmt.Errorf("AI confidence must be 30–95 %%")
	}
	valid := map[string]bool{"motion": true} // AI objects stay valid, also while the AI is switched off
	for _, o := range s.aiObjects() {
		valid[o] = true
	}
	objs := []string{}
	for _, o := range cs.Objects {
		if !valid[o] {
			return fmt.Errorf("unknown object %q", o)
		}
		objs = append(objs, o)
	}
	cs.Objects = objs
	zones, err := cleanZones(cs.Zones, valid)
	if err != nil {
		return err
	}
	cs.Zones = zones
	b, _ := json.Marshal(cs)
	if _, err := s.db.Exec(`INSERT INTO cam_settings(cam,json) VALUES(?,?) ON CONFLICT(cam) DO UPDATE SET json=excluded.json`, cam, string(b)); err != nil {
		return err
	}
	s.mu.Lock()
	s.m[cam] = cs
	for k := range s.maskCache {
		if strings.HasPrefix(k, cam+"/") {
			delete(s.maskCache, k)
		}
	}
	s.mu.Unlock()
	return nil
}

// motion turns sensitivity into a threshold and a minimum area. 5 = the configuration's default; each step ±25 %/50 %.
func (cs CamSettings) motion(base MotionConfig) (threshold, minArea float64) {
	d := float64(5 - cs.Sensitivity)
	return base.Threshold * math.Pow(1.25, d), base.MinArea * math.Pow(1.5, d)
}

// labels: AI server label -> object name, only for the chosen objects.
func (s *Settings) labels(cs CamSettings) map[string]string {
	want := map[string]bool{}
	for _, o := range cs.Objects {
		want[o] = true
	}
	out := map[string]string{}
	for k, v := range s.cfg.Labels {
		if want[v] {
			out[k] = v
		}
	}
	return out
}

// Home mode: set by Home Assistant (the phone at home/away) and stored so it survives a restart.
func (s *Settings) initMode() {
	s.db.Exec(`CREATE TABLE IF NOT EXISTS kv(k TEXT PRIMARY KEY, v TEXT)`)
	var v string
	s.db.QueryRow(`SELECT v FROM kv WHERE k='home'`).Scan(&v)
	s.mu.Lock()
	s.home = v == "1"
	s.mu.Unlock()
}

func (s *Settings) Home() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.home
}

func (s *Settings) SetHome(h bool) {
	v := "0"
	if h {
		v = "1"
	}
	s.db.Exec(`INSERT INTO kv(k,v) VALUES('home',?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, v)
	s.mu.Lock()
	s.home = h
	s.mu.Unlock()
}

// EventsOff: should the camera refrain from creating events right now?
func (s *Settings) EventsOff(cam string) bool { return s.Home() && s.Get(cam).HomeOff }

// GetKV/SetKV: small values that must be remembered between restarts (table kv).
func (s *Settings) GetKV(k string) string {
	var v string
	s.db.QueryRow(`SELECT v FROM kv WHERE k=?`, k).Scan(&v)
	return v
}

func (s *Settings) DelKV(k string) { s.db.Exec(`DELETE FROM kv WHERE k=?`, k) }

func (s *Settings) SetKV(k, v string) {
	s.db.Exec(`INSERT INTO kv(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, k, v)
}

// Retention: how long recordings are kept. As a Home Assistant app it is set in ArgusSen (Settings → Storage,
// table kv); otherwise the configuration's sub_days, main_hours and event_days are used. Changes apply at the next
// cleanup (every 10 min).
type Retention struct {
	SubDays   int `json:"sub_days"`   // 24/7 low resolution
	MainHours int `json:"main_hours"` // full resolution without an event
	EventDays int `json:"event_days"` // events + full resolution around them
}

func (s *Settings) Retention() Retention {
	r := Retention{s.cfg.SubDays, s.cfg.MainHours, s.cfg.EventDays}
	if !s.cfg.Ingress && !s.cfg.Standalone {
		return r
	}
	var v string
	s.db.QueryRow(`SELECT v FROM kv WHERE k='retention'`).Scan(&v)
	if v == "" {
		return s.DefaultRetention()
	}
	json.Unmarshal([]byte(v), &r)
	return r
}

// DefaultRetention: until the user chooses, the retention follows the storage. Home Assistant's own disk below 128 GB
// (typical: 32–64 GB, shared with Home Assistant itself): SD 3 days and HD only around events. A bigger disk or a
// network drive: SD 14 days and HD 24 hours. Events (with HD around them) are always kept 30 days.
func (s *Settings) DefaultRetention() Retention {
	if s.cfg.StorageMount == "" && !isNetwork(s.cfg.Storage) {
		if t := totalGB(s.cfg.Storage); t > 0 && t < 128 {
			return Retention{SubDays: 3, MainHours: 0, EventDays: 30}
		}
	}
	return Retention{SubDays: 14, MainHours: 24, EventDays: 30}
}

// MaxGB: the most space ArgusSen's recordings may take on the storage (GB, 0 = no limit – only the disk guard's free
// space). Set in Settings → Storage; the disk guard deletes the oldest recordings when it is passed.
func (s *Settings) MaxGB() float64 {
	v, _ := strconv.ParseFloat(s.GetKV("max_gb"), 64)
	return v
}

func (s *Settings) SetMaxGB(v float64) error {
	if v < 0 || v > 1e6 || (v > 0 && v < 5) {
		return fmt.Errorf("the limit must be at least 5 GB (or empty for no limit)")
	}
	if v == 0 {
		_, err := s.db.Exec(`DELETE FROM kv WHERE k='max_gb'`)
		return err
	}
	s.SetKV("max_gb", strconv.FormatFloat(v, 'f', -1, 64))
	return nil
}

// RetentionIsDefault: the user has not chosen a retention (it follows the storage).
func (s *Settings) RetentionIsDefault() bool { return s.GetKV("retention") == "" }

// SetRetention saves the user's choice (Settings → Storage).
func (s *Settings) SetRetention(r Retention) error {
	if r.SubDays < 1 || r.SubDays > 365 || r.MainHours < 0 || r.MainHours > 720 || r.EventDays < 1 || r.EventDays > 365 {
		return fmt.Errorf("days of SD must be 1–365, hours of HD 0–720 and days of events 1–365")
	}
	b, _ := json.Marshal(r)
	_, err := s.db.Exec(`INSERT INTO kv(k,v) VALUES('retention',?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, string(b))
	return err
}

// ResetRetention: back to the default for the storage.
func (s *Settings) ResetRetention() error {
	_, err := s.db.Exec(`DELETE FROM kv WHERE k='retention'`)
	return err
}

// adoptRetention: 0.9.2–0.9.4 saved the old default 14/24/30 although nobody had chosen it – removed once, so the
// retention follows the storage.
func (s *Settings) adoptRetention() {
	if !s.cfg.Ingress || s.GetKV("retention_checked") == "1" {
		return
	}
	if s.GetKV("retention") == `{"sub_days":14,"main_hours":24,"event_days":30}` {
		s.ResetRetention()
	}
	s.SetKV("retention_checked", "1")
}

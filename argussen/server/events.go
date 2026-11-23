package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Event struct {
	ID     int64     `json:"id"`
	Cam    string    `json:"cam"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"` // zero while the event is ongoing
	Labels []string  `json:"labels"`
	Score  float64   `json:"score"`
	Boxes  []Box     `json:"boxes"` // on the thumbnail
	Keep   bool      `json:"keep"`  // protected: never deleted by the cleanup
	Zones  []string  `json:"zones"` // the zones it has been in (ids, zones.go)
	thumb  string
}

type track struct {
	first, firstBox, last time.Time
	box0, box             Box
}

type camEvState struct {
	active      bool
	motionStart time.Time
	lastMotion  time.Time
	lastAI      time.Time
	aiBusy      bool
	tracks      []*track
	open        *Event
	boxes       []Box // latest AI hits (for live view)
	boxesAt     time.Time
	lastClosed  *Event   // reopened if a new event comes within 30 s
	motionZones []string // the zones with motion when the motion began
}

type EventManager struct {
	cfg      *Config
	settings *Settings
	db       *sql.DB
	mu       sync.Mutex
	st       map[string]*camEvState
	// OnEvent is called when an event starts, is updated or ends (for MQTT).
	OnEvent func(e Event)
}

func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS events(
		id INTEGER PRIMARY KEY, cam TEXT NOT NULL, start INTEGER NOT NULL, end INTEGER,
		labels TEXT NOT NULL DEFAULT '', score REAL NOT NULL DEFAULT 0, thumb TEXT, boxes TEXT);
		CREATE INDEX IF NOT EXISTS events_start ON events(start);
		CREATE INDEX IF NOT EXISTS events_cam ON events(cam, start);`)
	if err == nil {
		db.Exec(`ALTER TABLE events ADD COLUMN keep INTEGER NOT NULL DEFAULT 0`) // already exists = error, harmless
		db.Exec(`ALTER TABLE events ADD COLUMN zones TEXT NOT NULL DEFAULT ''`)
	}
	return db, err
}

func newEventManager(cfg *Config, db *sql.DB, st *Settings) *EventManager {
	// Events without an end time are left by a restart in the middle of an event (none can be ongoing at start-up).
	// They are closed 30 s after the start – otherwise the cleanup would keep all HD from the event onwards (found 2026-11-19).
	if r, err := db.Exec(`UPDATE events SET end = start + 30000 WHERE end IS NULL OR end < start`); err == nil {
		if n, _ := r.RowsAffected(); n > 0 {
			log.Printf("cleanup: %d events without an end time closed", n)
		}
	}
	return &EventManager{cfg: cfg, settings: st, db: db, st: map[string]*camEvState{}}
}

// LiveBoxes gives the latest AI hits if they are less than 3 s old.
func (m *EventManager) LiveBoxes(cam string) []Box {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.st[cam]; s != nil && time.Since(s.boxesAt) < 3*time.Second {
		return s.boxes
	}
	return nil
}

// Feed is called for every analysed frame (e.g. 5 per second) from the camera's own goroutine.
// zones: the zones with motion in this frame (zones.go).
func (m *EventManager) Feed(cam *Camera, f *Frame, moving bool, zones []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.st[cam.ID]
	if s == nil {
		s = &camEvState{}
		m.st[cam.ID] = s
	}
	if moving {
		if !s.active {
			s.active, s.motionStart, s.motionZones = true, f.At, nil
		}
		s.lastMotion = f.At
		// "motion" chosen as an object: an event as soon as the motion has lasted 2 seconds (also without AI)
		if len(zones) > 0 {
			s.motionZones = zones
		}
		if s.open == nil && f.At.Sub(s.motionStart) >= 2*time.Second && m.motionEvents(cam.ID) {
			m.openMotionEvent(cam, s, f)
		}
		// a motion event that moves into another zone (AI events get their zones from the hits)
		if s.open != nil && len(zones) > 0 && contains(s.open.Labels, "motion") && addZones(&s.open.Zones, zones) {
			sort.Strings(s.open.Zones)
			m.save(s.open, nil, false)
			if m.OnEvent != nil {
				m.OnEvent(*s.open)
			}
		}
		if !s.aiBusy && f.At.Sub(s.lastAI) >= time.Duration(m.cfg.Motion.AIInterval*float64(time.Second)) {
			s.aiBusy, s.lastAI = true, f.At
			go m.analyze(cam, f)
		}
	} else if s.active {
		s.active = false
		if s.open != nil {
			m.closeEvent(s, s.lastMotion)
		}
	}
	// very long events are split
	if s.open != nil && f.At.Sub(s.open.Start) > 10*time.Minute {
		m.closeEvent(s, f.At)
		s.motionStart = f.At
	}
}

func (m *EventManager) analyze(cam *Camera, f *Frame) {
	cs := m.settings.Get(cam.ID)
	labels := m.settings.labels(cs)
	var boxes []Box
	var jpg []byte
	var err error
	if len(labels) > 0 && m.cfg.BlueOnyx != "" && !m.settings.EventsOff(cam.ID) { // no objects / home mode = no events
		jpg, err = frameJPEG(f, 85)
		if err == nil {
			boxes, err = detect(m.cfg.BlueOnyx, jpg, labels, float64(cs.MinConfidence)/100)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.st[cam.ID]
	s.aiBusy = false
	if err != nil {
		log.Printf("%s: AI error: %v", cam.ID, err)
		return
	}
	now := f.At
	// zones: a hit counts where its foot point is (the middle of the bottom edge); areas to ignore drop it
	if zones := m.settings.Get(cam.ID).Zones; len(zones) > 0 && f.W > 0 && f.H > 0 {
		kept := boxes[:0]
		for _, b := range boxes {
			ignored, in := hitZones(zones, float64(b.X1+b.X2)/2/float64(f.W), float64(b.Y2)/float64(f.H), b.Label)
			if ignored {
				continue
			}
			b.Zones = in
			kept = append(kept, b)
		}
		boxes = kept
	}
	s.boxes, s.boxesAt = boxes, now
	// forget objects not seen for 10 min
	keep := s.tracks[:0]
	for _, t := range s.tracks {
		if now.Sub(t.last) < 10*time.Minute {
			keep = append(keep, t)
		}
	}
	s.tracks = keep

	// An object that has stood still in the same place for over a minute (a parked car) does not trigger events.
	var fresh []Box
	for _, b := range boxes {
		var match *track
		best := 0.5
		for _, t := range s.tracks {
			if t.box.Label == b.Label {
				if v := iou(t.box, b); v > best {
					best, match = v, t
				}
			}
		}
		if match == nil {
			s.tracks = append(s.tracks, &track{first: now, last: now, box0: b, box: b})
			fresh = append(fresh, b)
			continue
		}
		match.last, match.box = now, b
		if iou(match.box0, b) > 0.7 && now.Sub(match.first) > time.Minute {
			continue // standing still
		}
		if iou(match.box0, b) <= 0.7 {
			match.box0, match.first = b, now // has moved
		}
		fresh = append(fresh, b)
	}
	if len(fresh) == 0 {
		return
	}
	e := s.open
	if e == nil && s.lastClosed != nil && now.Sub(s.lastClosed.End) < 30*time.Second {
		e = s.lastClosed
		e.End = time.Time{}
		s.open, s.lastClosed = e, nil
	}
	if e == nil {
		start := s.motionStart
		if now.Sub(start) > 30*time.Second {
			start = now.Add(-30 * time.Second)
		}
		e = &Event{Cam: cam.ID, Start: start}
		s.open = e
	}
	score := 0.0
	for _, b := range fresh {
		score = max(score, b.Conf)
		if !contains(e.Labels, b.Label) {
			e.Labels = append(e.Labels, b.Label)
		}
		addZones(&e.Zones, b.Zones)
	}
	sort.Strings(e.Labels)
	sort.Strings(e.Zones)
	newThumb := score > e.Score
	if newThumb {
		e.Score, e.Boxes = score, fresh
	}
	isNew := e.ID == 0
	m.save(e, jpg, newThumb)
	if isNew {
		var detected []string
		for _, b := range fresh {
			detected = append(detected, fmt.Sprintf("%s:%.0f%% [%d,%d %d,%d]", b.Label, b.Conf*100, b.X1, b.Y1, b.X2, b.Y2))
		}
		log.Printf("%s: event %d confirmed by AI: %s", cam.ID, e.ID, strings.Join(detected, " "))
	}
	if m.OnEvent != nil {
		m.OnEvent(*e)
	}
}

// motionEvents: whether motion alone creates events for the camera (the object "motion" is chosen).
func (m *EventManager) motionEvents(cam string) bool {
	return contains(m.settings.Get(cam).Objects, "motion") && !m.settings.EventsOff(cam)
}

// openMotionEvent starts an event from motion (m.mu is held). An event that ended less than 30 s ago continues.
func (m *EventManager) openMotionEvent(cam *Camera, s *camEvState, f *Frame) {
	if s.lastClosed != nil && f.At.Sub(s.lastClosed.End) < 30*time.Second {
		e := s.lastClosed
		e.End = time.Time{}
		if !contains(e.Labels, "motion") {
			e.Labels = append(e.Labels, "motion")
			sort.Strings(e.Labels)
		}
		s.open, s.lastClosed = e, nil
		m.save(e, nil, false)
	} else {
		e := &Event{Cam: cam.ID, Start: s.motionStart, Labels: []string{"motion"}, Zones: append([]string(nil), s.motionZones...)}
		sort.Strings(e.Zones)
		s.open = e
		jpg, _ := frameJPEG(f, 85)
		m.save(e, jpg, true)
		log.Printf("%s: event %d started by motion", cam.ID, e.ID)
	}
	if m.OnEvent != nil {
		m.OnEvent(*s.open)
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func (m *EventManager) closeEvent(s *camEvState, end time.Time) {
	e := s.open
	s.open = nil
	e.End = end
	s.lastClosed = e
	m.save(e, nil, false)
	if m.OnEvent != nil {
		m.OnEvent(*e)
	}
}

func ms(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UnixMilli()
}

func (m *EventManager) save(e *Event, jpg []byte, newThumb bool) {
	boxes, _ := json.Marshal(e.Boxes)
	labels := strings.Join(e.Labels, ",")
	zones := strings.Join(e.Zones, ",")
	if e.ID == 0 {
		r, err := m.db.Exec(`INSERT INTO events(cam,start,end,labels,score,boxes,zones) VALUES(?,?,?,?,?,?,?)`,
			e.Cam, ms(e.Start), ms(e.End), labels, e.Score, string(boxes), zones)
		if err != nil {
			log.Printf("db: %v", err)
			return
		}
		e.ID, _ = r.LastInsertId()
	}
	if newThumb && jpg != nil {
		rel := filepath.Join("events", e.Start.UTC().Format("20060102"), e.Cam+"-"+itoa(e.ID)+".jpg")
		p := filepath.Join(store.Active(), rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, jpg, 0o644); err == nil {
			e.thumb = rel
		}
	}
	_, err := m.db.Exec(`UPDATE events SET end=?, labels=?, score=?, boxes=?, zones=?, thumb=COALESCE(?,thumb) WHERE id=?`,
		ms(e.End), labels, e.Score, string(boxes), zones, nullStr(e.thumb), e.ID)
	if err != nil {
		log.Printf("db: %v", err)
	}
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }

// ListEvents fetches events, newest first.
func (m *EventManager) ListEvents(cams []string, before time.Time, limit int) ([]Event, error) {
	return m.listEvents(cams, before, limit, false)
}

func (m *EventManager) listEvents(cams []string, before time.Time, limit int, keepOnly bool) ([]Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id,cam,start,end,labels,score,COALESCE(thumb,''),COALESCE(boxes,''),keep,zones FROM events WHERE start < ?`
	args := []any{before.UnixMilli()}
	if keepOnly {
		q += ` AND keep=1`
	}
	if cams != nil {
		if len(cams) == 0 {
			return nil, nil
		}
		q += ` AND cam IN (?` + strings.Repeat(",?", len(cams)-1) + `)`
		for _, c := range cams {
			args = append(args, c)
		}
	}
	q += ` ORDER BY start DESC LIMIT ?`
	args = append(args, limit)
	rows, err := m.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var start int64
		var end sql.NullInt64
		var labels, boxes, zones string
		if err := rows.Scan(&e.ID, &e.Cam, &start, &end, &labels, &e.Score, &e.thumb, &boxes, &e.Keep, &zones); err != nil {
			return nil, err
		}
		if zones != "" {
			e.Zones = strings.Split(zones, ",")
		}
		e.Start = time.UnixMilli(start)
		if end.Valid {
			e.End = time.UnixMilli(end.Int64)
		}
		if labels != "" {
			e.Labels = strings.Split(labels, ",")
		}
		json.Unmarshal([]byte(boxes), &e.Boxes)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (m *EventManager) Thumb(id int64) (string, string) {
	var p, cam string
	m.db.QueryRow(`SELECT COALESCE(thumb,''), cam FROM events WHERE id=?`, id).Scan(&p, &cam)
	if p == "" {
		return "", cam
	}
	return store.Find(p), cam
}

// keepRanges gives the time ranges per camera where full resolution must be kept.
func (m *EventManager) keepRanges(since time.Time) map[string][][2]time.Time {
	return m.ranges(`SELECT cam,start,COALESCE(end,start) FROM events WHERE start >= ? OR keep=1`, since.UnixMilli())
}

// protRanges: time ranges around protected events – both sub and main are kept there, regardless of age.
func (m *EventManager) protRanges() map[string][][2]time.Time {
	return m.ranges(`SELECT cam,start,COALESCE(end,start) FROM events WHERE keep=1`)
}

func (m *EventManager) ranges(q string, args ...any) map[string][][2]time.Time {
	out := map[string][][2]time.Time{}
	rows, err := m.db.Query(q, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var cam string
		var s, e int64
		rows.Scan(&cam, &s, &e)
		cs := m.settings.Get(cam)
		pre := time.Duration(cs.PreSeconds) * time.Second
		post := time.Duration(cs.PostSeconds) * time.Second
		end := time.UnixMilli(e)
		if e == s { // still ongoing – but an event lasts at most 10 min, so an older one has just not been closed
			if time.Since(time.UnixMilli(s)) < 15*time.Minute {
				end = time.Now()
			}
		}
		out[cam] = append(out[cam], [2]time.Time{time.UnixMilli(s).Add(-pre), end.Add(post)})
	}
	return out
}

// GetEvent fetches one event.
func (m *EventManager) GetEvent(id int64) (*Event, error) {
	var e Event
	var start int64
	var end sql.NullInt64
	var labels, boxes, zones string
	err := m.db.QueryRow(`SELECT id,cam,start,end,labels,score,COALESCE(thumb,''),COALESCE(boxes,''),keep,zones FROM events WHERE id=?`, id).
		Scan(&e.ID, &e.Cam, &start, &end, &labels, &e.Score, &e.thumb, &boxes, &e.Keep, &zones)
	if err != nil {
		return nil, err
	}
	if zones != "" {
		e.Zones = strings.Split(zones, ",")
	}
	e.Start = time.UnixMilli(start)
	if end.Valid {
		e.End = time.UnixMilli(end.Int64)
	}
	if labels != "" {
		e.Labels = strings.Split(labels, ",")
	}
	json.Unmarshal([]byte(boxes), &e.Boxes)
	return &e, nil
}

func (m *EventManager) SetKeep(id int64, keep bool) error {
	v := 0
	if keep {
		v = 1
	}
	_, err := m.db.Exec(`UPDATE events SET keep=? WHERE id=?`, v, id)
	return err
}

// DeleteEvent deletes the event and the thumbnail. The recording around it is removed by the normal cleanup.
func (m *EventManager) DeleteEvent(id int64) error {
	e, err := m.GetEvent(id)
	if err != nil {
		return err
	}
	if e.thumb != "" {
		store.Remove(e.thumb)
	}
	_, err = m.db.Exec(`DELETE FROM events WHERE id=?`, id)
	return err
}

// keepThumbs: thumbnails of protected events (relative paths).
func (m *EventManager) keepThumbs() map[string]bool {
	out := map[string]bool{}
	rows, err := m.db.Query(`SELECT COALESCE(thumb,'') FROM events WHERE keep=1`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		rows.Scan(&p)
		out[p] = true
	}
	return out
}

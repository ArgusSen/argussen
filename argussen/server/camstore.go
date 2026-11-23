package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
)

// The cameras are stored in the database (table cameras) so they can be added and changed from the app while ArgusSen runs.
// On the first start, cameras listed in the configuration ("cameras") are imported. After that the database is what counts.

type camStore struct {
	db      *sql.DB
	mu      sync.RWMutex
	list    []*Camera
	dataDir string // data folder (the private build writes its camera list there, private.go: writeList)
}

var cams *camStore

// camRecord is what is stored (incl. the login, which Camera keeps outside JSON).
type camRecord struct {
	Camera
	User string `json:"user"`
	Pass string `json:"pass"`
}

func normalizeCam(c *Camera) {
	if c.Port == 0 {
		c.Port = 554
	}
	if c.Main == "" && c.reolink() {
		c.Main = "/h264Preview_01_main"
	}
	if c.Sub == "" && c.reolink() {
		c.Sub = "/h264Preview_01_sub"
	}
	if c.Name == "" {
		c.Name = c.ID
	}
}

func openCamStore(db *sql.DB, cfg *Config) (*camStore, error) {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS cameras(id TEXT PRIMARY KEY, pos INTEGER NOT NULL, data TEXT NOT NULL)`); err != nil {
		return nil, err
	}
	s := &camStore{db: db, dataDir: cfg.Data}
	rows, err := db.Query(`SELECT data FROM cameras ORDER BY pos, id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var js string
		rows.Scan(&js)
		var r camRecord
		if json.Unmarshal([]byte(js), &r) == nil {
			c := r.Camera
			c.user, c.pass = r.User, r.Pass
			normalizeCam(&c)
			s.list = append(s.list, &c)
		}
	}
	rows.Close()
	if len(s.list) == 0 && len(cfg.Cameras) > 0 {
		for i, c := range cfg.Cameras {
			normalizeCam(c)
			if err := s.write(c, i); err != nil {
				return nil, err
			}
			s.list = append(s.list, c)
		}
		log.Printf("Cameras: %d moved from argus.json to the database", len(s.list))
	}
	s.writeList()
	return s, nil
}

func (s *camStore) write(c *Camera, pos int) error {
	b, _ := json.Marshal(camRecord{Camera: *c, User: c.user, Pass: c.pass})
	_, err := s.db.Exec(`INSERT INTO cameras(id,pos,data) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, c.ID, pos, string(b))
	return err
}

func (s *camStore) All() []*Camera {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]*Camera(nil), s.list...)
}

func (s *camStore) Get(id string) *Camera {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.list {
		if c.ID == id {
			return c
		}
	}
	return nil
}

// Save creates or replaces a camera (new object – running streams use the old one until they are restarted).
func (s *camStore) Save(c *Camera) error {
	normalizeCam(c)
	s.mu.Lock()
	defer s.mu.Unlock()
	pos := len(s.list)
	for i, x := range s.list {
		if x.ID == c.ID {
			pos = i
		}
	}
	if err := s.write(c, pos); err != nil {
		return err
	}
	if pos == len(s.list) {
		s.list = append(s.list, c)
	} else {
		s.list[pos] = c
	}
	s.writeList()
	return nil
}

// Reorder saves a new order of the cameras (Settings → Cameras → Change order). ids must hold every camera once.
func (s *camStore) Reorder(ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(ids) != len(s.list) {
		return fmt.Errorf("give every camera once")
	}
	byID := map[string]*Camera{}
	for _, c := range s.list {
		byID[c.ID] = c
	}
	list := make([]*Camera, 0, len(ids))
	for _, id := range ids {
		c := byID[id]
		if c == nil {
			return fmt.Errorf("give every camera once")
		}
		delete(byID, id) // a camera given twice is caught here
		list = append(list, c)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for i, c := range list {
		if _, err := tx.Exec(`UPDATE cameras SET pos=? WHERE id=?`, i, c.ID); err != nil {
			tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.list = list
	s.writeList()
	return nil
}

func (s *camStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec(`DELETE FROM cameras WHERE id=?`, id); err != nil {
		return err
	}
	for i, x := range s.list {
		if x.ID == id {
			s.list = append(s.list[:i], s.list[i+1:]...)
			break
		}
	}
	s.writeList()
	return nil
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// slug makes an id from a name: "Basement 3" → "basement_3", "Kælder" → "kaelder".
func slug(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.NewReplacer("æ", "ae", "ø", "oe", "å", "aa").Replace(s)
	s = strings.Trim(slugRe.ReplaceAllString(s, "_"), "_")
	if s == "" {
		s = "camera"
	}
	return s
}

func (s *camStore) newID(name string) string {
	base := slug(name)
	id := base
	for i := 2; s.Get(id) != nil; i++ {
		id = fmt.Sprintf("%s_%d", base, i)
	}
	return id
}

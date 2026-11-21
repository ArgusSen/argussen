package main

// One language for the whole ArgusSen: the user interface (default for devices set to "Automatic"), the log and the
// entity names in Home Assistant. The setting (table kv, "language") is "auto" or a language code. "auto" means
// Home Assistant's own language when running as a Home Assistant app (asked via the Supervisor, checked again every
// 10 minutes), otherwise "language" from the configuration (default English).

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

var languages = []string{"en", "da", "de", "fr", "sv", "nb", "nl"}

// languageNames: each language's name in itself (for the log).
var languageNames = map[string]string{"en": "English", "da": "Dansk", "de": "Deutsch", "fr": "Français", "sv": "Svenska", "nb": "Norsk", "nl": "Nederlands"}

var langState = struct {
	sync.RWMutex
	cur  string // the language in use
	auto string // what "auto" means right now
}{cur: "en", auto: "en"}

// lang: the language in use (en, da, de, fr, sv, nb, nl).
func lang() string {
	langState.RLock()
	defer langState.RUnlock()
	return langState.cur
}

// normLang turns a language tag (e.g. "en-GB", "no", "da") into one of the supported languages ("" if none).
func normLang(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.IndexAny(s, "-_"); i > 0 {
		s = s[:i]
	}
	if s == "no" || s == "nn" {
		s = "nb"
	}
	for _, l := range languages {
		if l == s {
			return l
		}
	}
	return ""
}

func (s *Server) langSetting() string {
	if v := normLang(s.ev.settings.GetKV("language")); v != "" {
		return v
	}
	return "auto"
}

// applyLanguage works out the language from the setting and, if it changed, updates the names in Home Assistant.
func (s *Server) applyLanguage() {
	l := s.langSetting()
	langState.Lock()
	if l == "auto" {
		l = langState.auto
	}
	changed := l != langState.cur
	langState.cur = l
	langState.Unlock()
	if changed {
		s.haRefresh("")
	}
}

// startLanguage sets the language at start-up and keeps "auto" in step with Home Assistant.
func (s *Server) startLanguage() {
	auto := normLang(s.cfg.Language)
	if auto == "" {
		auto = "en"
	}
	if s.cfg.Ingress {
		if v := normLang(s.ev.settings.GetKV("language_ha")); v != "" { // last known HA language until HA answers
			auto = v
		}
	}
	langState.Lock()
	langState.auto = auto
	langState.Unlock()
	s.applyLanguage()
	if !s.cfg.Ingress {
		return
	}
	go func() {
		wait := 5 * time.Second
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-time.After(wait):
			}
			l, err := haCoreLanguage()
			if err != nil {
				wait = time.Minute
				continue
			}
			wait = 10 * time.Minute
			if v := normLang(l); v != "" {
				l = v
			} else {
				l = "en" // a language ArgusSen does not have yet
			}
			s.ev.settings.SetKV("language_ha", l)
			langState.Lock()
			changed := langState.auto != l
			langState.auto = l
			langState.Unlock()
			if changed {
				s.applyLanguage()
			}
		}
	}()
}

// haCoreLanguage asks Home Assistant (through the Supervisor) which language it uses.
func haCoreLanguage() (string, error) {
	tok := os.Getenv("SUPERVISOR_TOKEN")
	if tok == "" {
		return "", fmt.Errorf("SUPERVISOR_TOKEN is missing")
	}
	req, _ := http.NewRequest("GET", "http://supervisor/core/api/config", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	c := &http.Client{Timeout: 10 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var cfg struct {
		Language string `json:"language"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return "", err
	}
	return cfg.Language, nil
}

type languageOut struct {
	Setting  string   `json:"setting"`  // "auto" or a language
	Language string   `json:"language"` // the language in use
	Auto     string   `json:"auto"`     // what "auto" means right now
	HA       bool     `json:"ha"`       // "auto" follows Home Assistant
	All      []string `json:"languages"`
}

func (s *Server) languageOut() languageOut {
	langState.RLock()
	defer langState.RUnlock()
	return languageOut{Setting: s.langSetting(), Language: langState.cur, Auto: langState.auto, HA: s.cfg.Ingress, All: languages}
}

func (s *Server) getLanguage(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.languageOut()) }

func (s *Server) putLanguage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Setting string `json:"setting"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
		jsonErr(w, 400, "invalid request")
		return
	}
	v := normLang(req.Setting)
	if req.Setting != "auto" && v == "" {
		jsonErr(w, 400, "invalid request")
		return
	}
	if v == "" {
		v = "auto"
	}
	s.ev.settings.SetKV("language", v)
	s.applyLanguage()
	if v == "auto" {
		log.Printf("Language changed to automatic (%s) by %s", languageNames[lang()], userOf(r).Name)
	} else {
		log.Printf("Language changed to %s by %s", languageNames[v], userOf(r).Name)
	}
	writeJSON(w, s.languageOut())
}

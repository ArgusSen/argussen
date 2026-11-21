package main

// The Home Assistant integration (custom_components/argussen) talks to ArgusSen's API with a token.
// As a Home Assistant app, ArgusSen sets this up itself: an internal user "Home Assistant (integration)" (admin, all
// cameras, no password) with a token that is kept in the app's data, and the address + token are announced to Home
// Assistant through the Supervisor's discovery – Home Assistant then shows "Discovered: ArgusSen".
// Outside Home Assistant (a separate server) the integration logs in with a username and password instead.

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const integrationDevice = "Home Assistant integration"

// integrationToken returns the integration's token (made the first time; made again if its session was removed).
func (s *Server) integrationToken() (string, error) {
	db := s.auth.db
	var uid int64
	err := db.QueryRow(`SELECT id FROM users WHERE ha_id='argussen-integration'`).Scan(&uid)
	if err != nil {
		b := make([]byte, 24)
		rand.Read(b)
		name := "Home Assistant (integration)"
		res, err := db.Exec(`INSERT INTO users(name,pw,admin,cams,ha_id) VALUES(?,?,1,'*','argussen-integration')`, name, "ha$"+hex.EncodeToString(b))
		if err != nil {
			return "", err
		}
		uid, _ = res.LastInsertId()
	}
	tok := s.ev.settings.GetKV("integration_token")
	if tok != "" {
		var n int
		db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE token=? AND user_id=?`, tokenHash(tok), uid).Scan(&n)
		if n > 0 {
			return tok, nil
		}
	}
	b := make([]byte, 32)
	rand.Read(b)
	tok = base64.RawURLEncoding.EncodeToString(b)
	now := time.Now().Unix()
	if _, err := db.Exec(`INSERT INTO sessions(token,user_id,device,created,last_used) VALUES(?,?,?,?,?)`,
		tokenHash(tok), uid, integrationDevice, now, now); err != nil {
		return "", err
	}
	s.ev.settings.SetKV("integration_token", tok)
	return tok, nil
}

// announceIntegration tells Home Assistant (Supervisor discovery) how to reach ArgusSen.
func (s *Server) announceIntegration() {
	if !s.cfg.Ingress || os.Getenv("SUPERVISOR_TOKEN") == "" {
		return
	}
	time.Sleep(10 * time.Second)
	// run.sh put a new version of the integration into Home Assistant: it is used after a restart
	if v, err := os.ReadFile("/data/integration_installed"); err == nil {
		os.Remove("/data/integration_installed")
		log.Printf("The ArgusSen integration %s was installed in Home Assistant", strings.TrimSpace(string(v)))
		haNotify(hn("ArgusSen: restart Home Assistant"),
			hn("The ArgusSen integration has been installed or updated. Restart Home Assistant once to use it (Settings → ⋮ → Restart Home Assistant)."))
	}
	tok, err := s.integrationToken()
	if err != nil {
		log.Printf("Home Assistant integration: %v", err)
		return
	}
	host, err := supervisorSelfHostname()
	if err != nil {
		log.Printf("Home Assistant integration: %v", err)
		return
	}
	body, _ := json.Marshal(map[string]any{"service": "argussen", "config": map[string]any{
		"host": host, "port": 8099, "token": tok, "version": version}})
	req, _ := http.NewRequest("POST", "http://supervisor/discovery", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+os.Getenv("SUPERVISOR_TOKEN"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		log.Printf("Home Assistant integration: %v", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("Home Assistant integration: discovery answered HTTP %d", resp.StatusCode)
	}
}

// supervisorSelfHostname: the app's host name on Home Assistant's internal network (e.g. "1a2b3c4d-argussen").
func supervisorSelfHostname() (string, error) {
	req, _ := http.NewRequest("GET", "http://supervisor/addons/self/info", nil)
	req.Header.Set("Authorization", "Bearer "+os.Getenv("SUPERVISOR_TOKEN"))
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var r struct {
		Data struct {
			Hostname string `json:"hostname"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil || r.Data.Hostname == "" {
		return "", fmt.Errorf("no host name (HTTP %d)", resp.StatusCode)
	}
	return r.Data.Hostname, nil
}

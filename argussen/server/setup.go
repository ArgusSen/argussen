package main

// The first start of a standalone ArgusSen (outside Home Assistant): there are no users yet, so the web page asks
// for the first administrator. Only possible while no user exists, and only from the local network – a server that
// is reachable from the internet before it is set up cannot be taken over from outside.
//
//	GET  /api/setup  {"needed": bool}
//	POST /api/setup  {"user", "password", "device"}  → as /api/login: {"token", "user"}

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

func (s *Server) setupNeeded() bool {
	if s.cfg.Ingress {
		return false // in Home Assistant the users come from Home Assistant
	}
	var n int
	s.auth.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n == 0
}

func (s *Server) getSetup(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]bool{"needed": s.setupNeeded()})
}

func (s *Server) postSetup(w http.ResponseWriter, r *http.Request) {
	var req struct{ User, Password, Device string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		jsonErr(w, 400, "invalid request")
		return
	}
	if !s.setupNeeded() {
		jsonErr(w, 403, "ArgusSen is already set up – log in")
		return
	}
	if !lanRequest(r) && !trustedLAN(r) {
		jsonErr(w, 403, "the first administrator can only be created from the local network")
		return
	}
	name := strings.TrimSpace(req.User)
	if name == "" || len(name) > 64 {
		jsonErr(w, 400, "give a username")
		return
	}
	if len([]rune(req.Password)) < 8 {
		jsonErr(w, 400, "the password must be at least 8 characters")
		return
	}
	// only while there are no users (also if two browsers try at the same time)
	res, err := s.auth.db.Exec(`INSERT INTO users(name,pw,admin,cams) SELECT ?,?,1,'*' WHERE NOT EXISTS (SELECT 1 FROM users)`,
		name, hashPassword(req.Password))
	if n, _ := res.RowsAffected(); err != nil || n == 0 {
		jsonErr(w, 403, "ArgusSen is already set up – log in")
		return
	}
	ip := clientIP(r)
	log.Printf("User %s created by %s", name, "the first start")
	tok, u, err := s.auth.Login(ip, name, req.Password, req.Device)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	s.auth.Record(Access{Type: "login", UserName: u.Name, IP: ip, Origin: s.origin(r), Device: req.Device})
	writeJSON(w, map[string]any{"token": tok, "user": u})
}

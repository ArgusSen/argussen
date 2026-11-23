package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// The first administrator of a standalone server: only while there are no users, only from the local network.
func TestFirstSetup(t *testing.T) {
	s := newTestServer(t)
	h := s.routes()
	do := func(method, body, remote, xff string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/setup", strings.NewReader(body))
		r.RemoteAddr = remote
		r.Header.Set("Content-Type", "application/json")
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := do("GET", "", "192.168.1.9:5000", ""); !strings.Contains(w.Body.String(), `"needed":true`) {
		t.Fatalf("setup not needed on a new server: %s", w.Body)
	}
	body := `{"user":"admin","password":"good password","device":"test"}`
	if w := do("POST", body, "8.8.8.8:5000", ""); w.Code != 403 {
		t.Errorf("setup from the internet: %d", w.Code)
	}
	if w := do("POST", body, "192.168.1.2:5000", "8.8.8.8"); w.Code != 403 {
		t.Errorf("setup through an untrusted proxy: %d", w.Code)
	}
	if w := do("POST", `{"user":"admin","password":"short"}`, "192.168.1.9:5000", ""); w.Code != 400 {
		t.Errorf("short password: %d", w.Code)
	}
	if w := do("POST", body, "192.168.1.9:5000", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"token"`) {
		t.Fatalf("setup from the local network: %d %s", w.Code, w.Body)
	}
	if u := s.findUser("admin"); u == nil || !u.Admin {
		t.Errorf("the first user is not an administrator: %+v", u)
	}
	if w := do("POST", `{"user":"eve","password":"good password"}`, "192.168.1.9:5000", ""); w.Code != 403 {
		t.Errorf("a second setup was allowed: %d", w.Code)
	}
	if w := do("GET", "", "192.168.1.9:5000", ""); !strings.Contains(w.Body.String(), `"needed":false`) {
		t.Errorf("setup still needed: %s", w.Body)
	}
}

func TestSetupInHomeAssistant(t *testing.T) {
	s := newTestServer(t)
	s.cfg.Ingress = true
	if s.setupNeeded() {
		t.Error("Home Assistant's users are used – no setup")
	}
}

func TestTrustedProxy(t *testing.T) {
	defer func() { trustedProxies = nil }()
	trustedProxies = parseNets([]string{"10.20.0.5", "10.1.0.0/16", "nonsense"})
	if len(trustedProxies) != 2 {
		t.Fatalf("parsed: %v", trustedProxies)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.20.0.5:4000"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 192.168.1.20")
	if ip := clientIP(r); ip != "192.168.1.20" || !trustedLAN(r) {
		t.Errorf("trusted proxy: %s", ip)
	}
	r.RemoteAddr = "10.20.0.6:4000" // not trusted: the header is ignored
	if ip := clientIP(r); ip != "10.20.0.6" || trustedLAN(r) {
		t.Errorf("untrusted proxy: %s", ip)
	}
}

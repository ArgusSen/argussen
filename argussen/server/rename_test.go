package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// Issue #2: renaming a camera must show the new name at once (overview, Status, Home Assistant) – without restarting it.
func TestRenameCamera(t *testing.T) {
	s := newTestServer(t)
	c := &Camera{ID: "c1", Name: "Same name", Host: "192.0.2.1", Enabled: true, user: "admin", pass: "secret", MainProto: "bc,rtsp,flv", SubProto: "bc,rtsp,flv"}
	normalizeCam(c)
	if err := cams.Save(c); err != nil {
		t.Fatal(err)
	}
	rn := newCamRunner(c, s.cfg, s.ev) // running (not started – no camera in a test)
	s.cams["c1"], s.ids = rn, append(s.ids, "c1")
	addUser(t, s, "boss", hashPassword("good password"), true, "*")
	tok, _, err := s.auth.Login("10.0.0.1", "boss", "good password", "test")
	if err != nil {
		t.Fatal(err)
	}
	h := s.routes()
	do := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.RemoteAddr = "10.0.0.1:5000"
		r.Header.Set("Authorization", "Bearer "+tok)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	var list []map[string]any
	json.Unmarshal(do("GET", "/api/admin/cameras", "").Body.Bytes(), &list)
	var cur map[string]any
	for _, x := range list {
		if x["id"] == "c1" {
			cur = x
		}
	}
	if cur == nil {
		t.Fatal("camera c1 not in the admin list")
	}
	cur["name"] = "Front garden"
	b, _ := json.Marshal(cur)
	if w := do("PUT", "/api/admin/cameras/c1", string(b)); w.Code != 200 {
		t.Fatalf("rename: %d %s", w.Code, w.Body)
	}
	if s.runner("c1") != rn {
		n := cams.Get("c1")
		t.Errorf("a new name must not restart the camera: before %+v user=%q / after %+v user=%q", *c, c.user, *n, n.user)
	}
	var shown []camInfo
	json.Unmarshal(do("GET", "/api/cams", "").Body.Bytes(), &shown)
	for _, x := range shown {
		if x.ID == "c1" && x.Name != "Front garden" {
			t.Errorf("/api/cams still shows %q", x.Name)
		}
	}
	var st struct {
		Cameras []CamState `json:"cameras"`
	}
	json.Unmarshal(do("GET", "/api/status", "").Body.Bytes(), &st)
	for _, x := range st.Cameras {
		if x.ID == "c1" && x.Name != "Front garden" {
			t.Errorf("/api/status still shows %q", x.Name)
		}
	}
}

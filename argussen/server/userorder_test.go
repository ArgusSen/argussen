package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// Each user has their own order; others keep the default; nothing is learnt about cameras one may not see;
// an empty list goes back to the default; deleting the user removes it.
func TestUserOrder(t *testing.T) {
	s := newTestServer(t)
	c := &Camera{ID: "c", Name: "C", Host: "192.168.1.3", Enabled: true}
	normalizeCam(c)
	s.cams["c"] = newCamRunner(c, s.cfg, s.ev)
	s.ids = append(s.ids, "c") // default order: a, b, c
	addUser(t, s, "anna", hashPassword("good password"), false, "*")
	addUser(t, s, "bo", hashPassword("good password"), false, "b,c")
	addUser(t, s, "boss", hashPassword("good password"), true, "*")
	h := s.routes()
	login := func(name string) string {
		tok, _, err := s.auth.Login("10.0.0.1", name, "good password", "test")
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	anna, bo, boss := login("anna"), login("bo"), login("boss")
	do := func(tok, method, path, body string) *httptest.ResponseRecorder {
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
	order := func(tok string) string {
		var l []camInfo
		json.Unmarshal(do(tok, "GET", "/api/cams", "").Body.Bytes(), &l)
		var ids []string
		for _, x := range l {
			ids = append(ids, x.ID)
		}
		return strings.Join(ids, ",")
	}
	own := func(tok string) bool {
		var m struct {
			Own bool `json:"own_order"`
		}
		json.Unmarshal(do(tok, "GET", "/api/me", "").Body.Bytes(), &m)
		return m.Own
	}
	if got := order(anna); got != "a,b,c" || own(anna) {
		t.Fatalf("default: %s own=%v", got, own(anna))
	}
	if w := do(anna, "PUT", "/api/me/order", `{"ids":["c","a"]}`); w.Code != 200 {
		t.Fatalf("PUT: %d %s", w.Code, w.Body)
	}
	if got := order(anna); got != "c,a,b" || !own(anna) {
		t.Fatalf("anna's own order: %s (want c,a,b – missing ones last) own=%v", got, own(anna))
	}
	if got := order(bo); got != "b,c" {
		t.Fatalf("bo must keep the default: %s", got)
	}
	// bo names a camera he may not see: it is left out (and the answer is the same as without it)
	w1 := do(bo, "PUT", "/api/me/order", `{"ids":["a","c","b"]}`)
	w2 := do(bo, "PUT", "/api/me/order", `{"ids":["c","b"]}`)
	if w1.Body.String() != w2.Body.String() {
		t.Fatalf("the answer differs for a hidden camera: %s / %s", w1.Body, w2.Body)
	}
	if got := s.ev.settings.GetKV(orderKey(2)); strings.Contains(got, `"a"`) {
		t.Fatalf("a hidden camera was stored: %s", got)
	}
	if got := order(bo); got != "c,b" {
		t.Fatalf("bo's own order: %s", got)
	}
	// the administrators' default changes for those without their own
	s.ids = []string{"b", "a", "c"}
	if got := order(boss); got != "b,a,c" {
		t.Fatalf("boss follows the default: %s", got)
	}
	if got := order(anna); got != "c,a,b" {
		t.Fatalf("anna keeps her own: %s", got)
	}
	// an empty list = the default again
	do(anna, "PUT", "/api/me/order", `{"ids":[]}`)
	if got := order(anna); got != "b,a,c" || own(anna) {
		t.Fatalf("back to the default: %s own=%v", got, own(anna))
	}
	// deleting bo removes his order (an id can be used again)
	if w := do(boss, "DELETE", "/api/admin/users/bo", ""); w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if got := s.ev.settings.GetKV(orderKey(2)); got != "" {
		t.Fatalf("bo's order is still there: %s", got)
	}
	// without JSON: refused like every other change
	r := httptest.NewRequest("PUT", "/api/me/order", strings.NewReader(`ids=a`))
	r.RemoteAddr = "10.0.0.1:5000"
	r.Header.Set("Authorization", "Bearer "+anna)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r.WithContext(context.Background()))
	if w.Code != 415 {
		t.Fatalf("form data: %d", w.Code)
	}
}

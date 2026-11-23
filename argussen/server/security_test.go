package main

// Tests of what protects ArgusSen: login, trust in Home Assistant's ingress, Home Assistant users, access per camera,
// the JSON requirement and the disk guard.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	db, err := openDB(filepath.Join(dir, "argus.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	cfg := &Config{Data: dir, Storage: filepath.Join(dir, "storage"), SubDays: 14, MainHours: 24, EventDays: 30,
		Labels: map[string]string{"person": "person", "car": "car"}, MinConf: 0.5}
	if err := initAuthDB(db); err != nil {
		t.Fatal(err)
	}
	if cams, err = openCamStore(db, cfg); err != nil {
		t.Fatal(err)
	}
	settings, err := newSettings(db, cfg)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: cfg, ev: newEventManager(cfg, db, settings), auth: newAuth(db), ctx: context.Background(),
		cams: map[string]*CamRunner{}, cancels: map[string]context.CancelFunc{}, dones: map[string]chan struct{}{}}
	for _, id := range []string{"a", "b"} {
		c := &Camera{ID: id, Name: strings.ToUpper(id), Host: "192.168.1.2", Enabled: true}
		normalizeCam(c)
		s.cams[id] = newCamRunner(c, cfg, s.ev)
		s.ids = append(s.ids, id)
	}
	return s
}

func addUser(t *testing.T, s *Server, name, pw string, admin bool, cams string) {
	t.Helper()
	if _, err := s.auth.db.Exec(`INSERT INTO users(name,pw,admin,cams) VALUES(?,?,?,?)`, name, pw, boolInt(admin), cams); err != nil {
		t.Fatal(err)
	}
}

func TestLogin(t *testing.T) {
	s := newTestServer(t)
	addUser(t, s, "anna", hashPassword("good password"), false, "a")
	addUser(t, s, "hauser", "ha$abcdef", false, "a")
	if _, _, err := s.auth.Login("10.0.0.1", "anna", "good password", "test"); err != nil {
		t.Fatalf("right password refused: %v", err)
	}
	for _, c := range [][2]string{{"anna", "wrong"}, {"nobody", "good password"}, {"hauser", "ha$abcdef"}, {"hauser", ""}} {
		if _, _, err := s.auth.Login("10.0.0.2", c[0], c[1], "test"); err == nil {
			t.Errorf("login %q/%q should be refused", c[0], c[1])
		}
	}
	// 5 failures from one address lock it – also for the right password
	for i := 0; i < 5; i++ {
		s.auth.Login("10.0.0.3", "anna", "wrong", "test")
	}
	if _, _, err := s.auth.Login("10.0.0.3", "anna", "good password", "test"); err != errLocked {
		t.Errorf("address not locked after 5 failures: %v", err)
	}
	// 10 failures for one username from different addresses lock the username
	for i := 0; i < 10; i++ {
		s.auth.Login("10.1.0."+string(rune('a'+i)), "Anna", "wrong", "test")
	}
	if _, _, err := s.auth.Login("10.0.0.9", "anna", "good password", "test"); err != errLocked {
		t.Errorf("username not locked after 10 failures: %v", err)
	}
}

func TestIngressTrust(t *testing.T) {
	s := newTestServer(t)
	req := func(from string) *http.Request {
		r := httptest.NewRequest("GET", "/api/me", nil)
		r.RemoteAddr = from + ":40000"
		r.Header.Set("X-Remote-User-Id", "ha-id-1")
		r.Header.Set("X-Remote-User-Name", "eve")
		return r
	}
	if u := s.ingressUser(req(ingressProxy)); u != nil {
		t.Error("ingress headers trusted although ingress is off")
	}
	s.cfg.Ingress = true
	for _, from := range []string{"172.30.32.9", "192.168.1.50", "127.0.0.1"} {
		if u := s.ingressUser(req(from)); u != nil {
			t.Errorf("ingress headers trusted from %s", from)
		}
	}
	u := s.ingressUser(req(ingressProxy))
	if u == nil {
		t.Fatal("ingress user from the Supervisor refused")
	}
	if u.Admin || len(u.Cams) != 0 {
		t.Errorf("a new Home Assistant user (admin unknown) must not be admin or see cameras: %+v", u)
	}
}

func TestHAUsersByID(t *testing.T) {
	s := newTestServer(t)
	addUser(t, s, "bob", hashPassword("bobs password"), true, "*")
	addUser(t, s, "carl", "ha$old", false, "a") // made by an older version, linked by name
	yes := true
	// an HA user with the same name as a local admin gets an account of its own
	u, created, err := s.auth.EnsureUser("id-bob", "Bob", nil, "")
	if err != nil || !created || u.Name == "bob" || u.Admin || len(u.Cams) != 0 {
		t.Fatalf("took over the local account or got rights: %+v created=%v err=%v", u, created, err)
	}
	// renamed in HA: the same account
	u2, created, _ := s.auth.EnsureUser("id-bob", "Robert", nil, "")
	if created || u2.ID != u.ID || u2.Name != "Robert" {
		t.Errorf("rename made a new user: %+v", u2)
	}
	// the old HA-made user is adopted
	var carlID int64
	s.auth.db.QueryRow(`SELECT id FROM users WHERE name='carl'`).Scan(&carlID)
	u3, created, _ := s.auth.EnsureUser("id-carl", "carl", &yes, "")
	if created || u3.ID != carlID || !u3.Admin {
		t.Errorf("old HA user not adopted: %+v", u3)
	}
	// new users see what the administrator chose
	u4, _, _ := s.auth.EnsureUser("id-dora", "dora", nil, "*")
	if !u4.CanSee("b") {
		t.Errorf("new user should see all cameras: %+v", u4)
	}
}

func TestAccessPerCamera(t *testing.T) {
	s := newTestServer(t)
	addUser(t, s, "anna", hashPassword("good password"), false, "a")
	tok, _, err := s.auth.Login("10.0.0.1", "anna", "good password", "test")
	if err != nil {
		t.Fatal(err)
	}
	h := s.routes()
	do := func(method, path, body, ctype string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.RemoteAddr = "10.0.0.1:5000"
		r.Header.Set("Authorization", "Bearer "+tok)
		if ctype != "" {
			r.Header.Set("Content-Type", ctype)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	var list []camInfo
	json.Unmarshal(do("GET", "/api/cams", "", "").Body.Bytes(), &list)
	if len(list) != 1 || list[0].ID != "a" {
		t.Errorf("/api/cams shows other cameras: %+v", list)
	}
	for _, p := range []string{"/api/cams/b/snapshot.jpg", "/api/live/b/sub/index.m3u8", "/api/rec/b/sub/20260101/000000.ts",
		"/api/play/b/sub", "/api/timeline/b", "/api/admin/cameras", "/api/admin/users", "/api/mode", "/api/status", "/api/log"} {
		if c := do("GET", p, "", "").Code; c != 404 && c != 403 {
			t.Errorf("%s: %d, want 403/404", p, c)
		}
	}
	if c := do("GET", "/api/cams", "", "").Code; c != 200 {
		t.Errorf("own cameras: %d", c)
	}
	// without a token: nothing
	r := httptest.NewRequest("GET", "/api/cams", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Errorf("without login: %d", w.Code)
	}
	// ?token= only works for video addresses
	if c := do("GET", "/api/cams?token="+tok, "", "").Code; c != 401 && c != 200 { // do() also sends the header
		t.Errorf("cams with token: %d", c)
	}
	for _, path := range []string{"/api/cams", "/api/events", "/api/admin/cameras", "/api/stream"} {
		r := httptest.NewRequest("GET", path+"?token="+tok, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Errorf("%s with ?token=: %d, want 401", path, w.Code)
		}
	}
	r2 := httptest.NewRequest("GET", "/api/play/a/sub?token="+tok, nil)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r2)
	if w2.Code == 401 {
		t.Errorf("/api/play with ?token=: 401")
	}
	// changes must be JSON (a form on another web site cannot send that)
	if c := do("POST", "/api/password", "old=x&new=y", "application/x-www-form-urlencoded").Code; c != 415 {
		t.Errorf("form instead of JSON: %d, want 415", c)
	}
	if c := do("POST", "/api/logout", "", "").Code; c != 415 {
		t.Errorf("empty POST without JSON: %d, want 415", c)
	}
	if c := do("POST", "/api/logout", "", "application/json").Code; c != 200 {
		t.Errorf("empty POST with JSON: %d, want 200", c)
	}
}

func TestDiskGuard(t *testing.T) {
	s := newTestServer(t)
	root := t.TempDir()
	day := time.Now().UTC().Add(-48 * time.Hour)
	write := func(stream string, at time.Time) string {
		p := filepath.Join(root, "rec", "a", stream, at.Format("20060102"), at.Format("150405")+".ts")
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, make([]byte, 1000), 0o644)
		return p
	}
	var old []string
	for i := 0; i < 10; i++ {
		at := day.Add(time.Duration(i) * time.Minute).Truncate(time.Minute)
		old = append(old, write("main", at), write("sub", at))
	}
	recent := write("main", time.Now().UTC().Truncate(time.Minute))
	// a protected event around minute 5
	ps := day.Add(5 * time.Minute).Truncate(time.Minute)
	s.ev.db.Exec(`INSERT INTO events(cam,start,end,labels,score,keep) VALUES('a',?,?,'person',0.9,1)`, ps.UnixMilli(), ps.Add(10*time.Second).UnixMilli())
	if low, n := makeRoomIn(root, 1e6, s.ev); !low || n == 0 { // can never be satisfied: everything allowed is deleted
		t.Errorf("should report too little space and delete files: low=%v deleted=%d", low, n)
	}
	if _, n := makeRoomIn(root, 1e6, s.ev); n != 0 { // only protected and newest left: nothing more to delete
		t.Errorf("deleted %d more files", n)
	}
	if _, err := os.Stat(recent); err != nil {
		t.Error("the newest recording was deleted")
	}
	protected := 0
	for _, p := range old {
		if _, err := os.Stat(p); err == nil {
			protected++
		}
	}
	if protected == 0 {
		t.Error("recordings around a protected event were deleted")
	}
	if protected == len(old) {
		t.Error("nothing was deleted")
	}
}

// setCamUsers runs in a transaction – it must not read through the database outside it (one connection: deadlock).
func TestSetCamUsers(t *testing.T) {
	s := newTestServer(t)
	addUser(t, s, "anna", hashPassword("good password"), false, "a")
	done := make(chan error, 1)
	go func() { done <- s.setCamUsers("b", []string{"anna"}) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("setCamUsers hangs")
	}
	var c string
	s.auth.db.QueryRow(`SELECT cams FROM users WHERE name='anna'`).Scan(&c)
	if c != "a,b" {
		t.Errorf("cams = %q", c)
	}
}

// An upgrade from 0.8: old HA users (not admins) lose "all cameras".
func TestUpgradeFrom08(t *testing.T) {
	s := newTestServer(t)
	s.cfg.Ingress = true
	addUser(t, s, "gina", "ha$old", false, "*")
	addUser(t, s, "hans", "ha$old2", true, "*")
	no, yes := false, true
	if u, _, _ := s.auth.EnsureUser("id-gina", "gina", &no, ""); u.CanSee("a") {
		t.Errorf("old HA user (not admin) still sees all: %+v", u)
	}
	if u, _, _ := s.auth.EnsureUser("id-hans", "hans", &yes, ""); !u.CanSee("a") {
		t.Errorf("HA admin lost the cameras: %+v", u)
	}
	if u, _, _ := s.auth.EnsureUser("id-hans", "hans", &no, ""); u.CanSee("a") {
		t.Errorf("former admin still sees all: %+v", u)
	}
}

// The default retention follows the storage until the user chooses.
func TestDefaultRetention(t *testing.T) {
	s := newTestServer(t)
	s.cfg.Ingress = true
	os.MkdirAll(s.cfg.Storage, 0o755)
	small := totalGB(s.cfg.Storage) < 128
	got := s.ev.settings.Retention()
	if small && got != (Retention{3, 0, 30}) || !small && got != (Retention{14, 24, 30}) {
		t.Errorf("own disk (%.0f GB): %+v", totalGB(s.cfg.Storage), got)
	}
	s.cfg.StorageMount = "/media/nas" // a network drive
	if got := s.ev.settings.Retention(); got != (Retention{14, 24, 30}) {
		t.Errorf("network drive: %+v", got)
	}
	s.ev.settings.SetRetention(Retention{7, 2, 30})
	if got := s.ev.settings.Retention(); got != (Retention{7, 2, 30}) || s.ev.settings.RetentionIsDefault() {
		t.Errorf("the user's choice: %+v", got)
	}
	s.ev.settings.ResetRetention()
	if !s.ev.settings.RetentionIsDefault() {
		t.Error("not back to the default")
	}
	// 0.9.2–0.9.4 saved 14/24/30 without anybody choosing it: removed once
	s.ev.settings.SetRetention(Retention{14, 24, 30})
	s.ev.settings.adoptRetention()
	if !s.ev.settings.RetentionIsDefault() {
		t.Error("the old saved default was not removed")
	}
	// a fresh install without old options saves nothing
	s.ev.settings.adoptRetention()
	if !s.ev.settings.RetentionIsDefault() {
		t.Error("a new installation saved a retention")
	}
}

// ArgusSen's own limit (Settings → Storage): the oldest recordings are deleted – HD outside events first.
func TestStorageLimit(t *testing.T) {
	s := newTestServer(t)
	root := t.TempDir()
	day := time.Now().UTC().Add(-48 * time.Hour)
	write := func(stream string, at time.Time) string {
		p := filepath.Join(root, "rec", "a", stream, at.Format("20060102"), at.Format("150405")+".ts")
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, make([]byte, 1000), 0o644)
		return p
	}
	var main0, sub0 string
	for i := 0; i < 10; i++ {
		at := day.Add(time.Duration(i) * time.Minute).Truncate(time.Minute)
		m, sb := write("main", at), write("sub", at)
		if i == 0 {
			main0, sub0 = m, sb
		}
	}
	defer usage.Store(-1)
	usage.Store(usedBytes(root, time.Minute))
	if usage.Load() != 20000 {
		t.Fatalf("measured %d bytes", usage.Load())
	}
	if over, n := keepWithin(root, 0, s.ev); over || n != 0 {
		t.Error("no limit: nothing may be deleted")
	}
	// a limit of 15 000 bytes: down to 14 700 → six HD files go first, the SD files stay
	if over, n := keepWithin(root, 15000/1e9, s.ev); over || n != 6 {
		t.Errorf("over=%v deleted=%d", over, n)
	}
	if _, err := os.Stat(main0); err == nil {
		t.Error("the oldest HD file was not deleted")
	}
	if _, err := os.Stat(sub0); err != nil {
		t.Error("an SD file was deleted before the HD files")
	}
	if usage.Load() != 14000 {
		t.Errorf("usage after deleting: %d", usage.Load())
	}
	if err := s.ev.settings.SetMaxGB(2); err == nil {
		t.Error("a limit below 5 GB was accepted")
	}
	if err := s.ev.settings.SetMaxGB(500); err != nil || s.ev.settings.MaxGB() != 500 {
		t.Errorf("limit 500: %v %v", err, s.ev.settings.MaxGB())
	}
	s.ev.settings.SetMaxGB(0)
	if s.ev.settings.MaxGB() != 0 {
		t.Error("the limit was not removed")
	}
}

// The Home Assistant switch "Active": only administrators, and only the enabled flag changes.
func TestCamEnabled(t *testing.T) {
	s := newTestServer(t)
	c := &Camera{ID: "c1", Name: "C1", Host: "192.0.2.1", Enabled: true}
	normalizeCam(c)
	if err := cams.Save(c); err != nil {
		t.Fatal(err)
	}
	addUser(t, s, "boss", hashPassword("good password"), true, "*")
	addUser(t, s, "anna", hashPassword("good password"), false, "*")
	h := s.routes()
	put := func(user, body string) int {
		tok, _, err := s.auth.Login("10.0.0.1", user, "good password", "test")
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("PUT", "/api/admin/cameras/c1/enabled", strings.NewReader(body))
		r.RemoteAddr = "10.0.0.1:5000"
		r.Header.Set("Authorization", "Bearer "+tok)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if code := put("anna", `{"enabled":false}`); code == 200 || !cams.Get("c1").Enabled {
		t.Errorf("a user who is not an administrator switched the camera off (%d)", code)
	}
	if code := put("boss", `{}`); code != 400 {
		t.Errorf("without enabled: %d", code)
	}
	if code := put("boss", `{"enabled":false}`); code != 200 || cams.Get("c1").Enabled {
		t.Errorf("switching off: %d, enabled=%v", code, cams.Get("c1").Enabled)
	}
	if got := cams.Get("c1"); got.Host != "192.0.2.1" || got.Name != "C1" {
		t.Errorf("other settings changed: %+v", got)
	}
}

// The order of the cameras: every camera once, saved in the database (survives a restart).
func TestCamOrder(t *testing.T) {
	s := newTestServer(t)
	for _, id := range []string{"x1", "x2", "x3"} {
		c := &Camera{ID: id, Name: id, Host: "192.0.2.1", Enabled: false}
		normalizeCam(c)
		if err := cams.Save(c); err != nil {
			t.Fatal(err)
		}
	}
	addUser(t, s, "boss", hashPassword("good password"), true, "*")
	tok, _, _ := s.auth.Login("10.0.0.1", "boss", "good password", "test")
	h := s.routes()
	put := func(body string) int {
		r := httptest.NewRequest("PUT", "/api/admin/cameras/order", strings.NewReader(body))
		r.RemoteAddr = "10.0.0.1:5000"
		r.Header.Set("Authorization", "Bearer "+tok)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	for _, bad := range []string{`{"ids":["x3","x1"]}`, `{"ids":["x3","x1","x1"]}`, `{"ids":["x3","x1","nope"]}`} {
		if code := put(bad); code != 400 {
			t.Errorf("%s: %d", bad, code)
		}
	}
	if code := put(`{"ids":["x3","x1","x2"]}`); code != 200 {
		t.Fatalf("reorder: %d", code)
	}
	order := func(list []*Camera) string {
		var ids []string
		for _, c := range list {
			ids = append(ids, c.ID)
		}
		return strings.Join(ids, ",")
	}
	if got := order(cams.All()); got != "x3,x1,x2" {
		t.Errorf("order: %s", got)
	}
	again, err := openCamStore(s.auth.db, s.cfg) // as after a restart
	if err != nil {
		t.Fatal(err)
	}
	if got := order(again.All()); got != "x3,x1,x2" {
		t.Errorf("order after a restart: %s", got)
	}
}

// Google's automatic test of the app: said in the log, not counted as a refused login.
func TestGoogleTest(t *testing.T) {
	for ip, want := range map[string]bool{"74.125.209.66": true, "66.249.84.10": true, "142.251.1.1": true,
		"35.190.1.1": false, "8.8.8.8": false, "192.168.1.9": false, "nonsense": false} {
		if googleTest(ip) != want {
			t.Errorf("%s: want %v", ip, want)
		}
	}
	s := newTestServer(t)
	h := s.routes()
	r := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"user":"gzkzof","password":"x","device":"Android-app"}`))
	r.RemoteAddr = "74.125.209.66:4000"
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Errorf("login: %d", w.Code)
	}
	if n := len(s.auth.Accesses(100, 0, "refused")); n != 0 {
		t.Errorf("counted as %d refused logins", n)
	}
	if n := len(s.auth.Accesses(100, 0, "google")); n != 1 {
		t.Errorf("google tests: %d", n)
	}
}

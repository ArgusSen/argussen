package main

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

type User struct {
	ID    int64    `json:"id"`
	Name  string   `json:"name"`
	Admin bool     `json:"admin"`
	Cams  []string `json:"cams"` // empty = none; "*" = all
}

func (u *User) CanSee(cam string) bool {
	for _, c := range u.Cams {
		if c == "*" || c == cam {
			return true
		}
	}
	return false
}

const pbkdfIter = 300_000

func initAuthDB(db *sql.DB) error {
	_, err := db.Exec(`
	CREATE TABLE IF NOT EXISTS users(id INTEGER PRIMARY KEY, name TEXT UNIQUE NOT NULL COLLATE NOCASE,
		pw TEXT NOT NULL, admin INTEGER NOT NULL DEFAULT 0, cams TEXT NOT NULL DEFAULT '');
	CREATE TABLE IF NOT EXISTS sessions(token TEXT PRIMARY KEY, user_id INTEGER NOT NULL,
		device TEXT, created INTEGER NOT NULL, last_used INTEGER NOT NULL);
	CREATE TABLE IF NOT EXISTS access(id INTEGER PRIMARY KEY, at INTEGER NOT NULL, type TEXT NOT NULL,
		username TEXT NOT NULL, ip TEXT NOT NULL, origin TEXT NOT NULL, device TEXT NOT NULL DEFAULT '');
	CREATE INDEX IF NOT EXISTS access_at ON access(at);`)
	if err != nil {
		return err
	}
	// ha_id: the Home Assistant user id of users who log in through Home Assistant (ingress.go)
	db.Exec(`ALTER TABLE users ADD COLUMN ha_id TEXT`) // fails harmlessly if it exists
	_, err = db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS users_ha_id ON users(ha_id) WHERE ha_id IS NOT NULL`)
	return err
}

// dummyHash: compared against when the user does not exist or cannot log in with a password (Home Assistant users),
// so the answer takes the same time in every case and does not reveal which usernames exist.
var dummyHash = sync.OnceValue(func() string { return hashPassword("not a password") })

func hashPassword(pw string) string {
	salt := make([]byte, 16)
	rand.Read(salt)
	k, _ := pbkdf2.Key(sha256.New, pw, salt, pbkdfIter, 32)
	return fmt.Sprintf("pbkdf2$%d$%s$%s", pbkdfIter, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(k))
}

func checkPassword(stored, pw string) bool {
	var iter int
	p := strings.Split(stored, "$")
	if len(p) != 4 || p[0] != "pbkdf2" {
		return false
	}
	fmt.Sscan(p[1], &iter)
	salt, err1 := base64.RawStdEncoding.DecodeString(p[2])
	want, err2 := base64.RawStdEncoding.DecodeString(p[3])
	if err1 != nil || err2 != nil || iter <= 0 {
		return false
	}
	k, _ := pbkdf2.Key(sha256.New, pw, salt, iter, len(want))
	return subtle.ConstantTimeCompare(k, want) == 1
}

func tokenHash(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

type Auth struct {
	db *sql.DB
	mu sync.Mutex
	// failed logins per IP ("ip:…") and per username ("user:…")
	fails map[string][]time.Time
	// OnAccess is called after every recorded access (for Home Assistant)
	OnAccess func(Access)
}

// Access is one line in the access log: "login" (with password), "opened" (the app used after at least 30 min pause)
// "refused" (wrong password/locked) or "google" (refused, but Google's automatic test of the app – google.go). Origin: "local" (directly on the local network), "ha" (through Home Assistant),
// "home" or "remote" (through a tunnel – only in the private build).
type Access struct {
	At       int64  `json:"at"`
	Type     string `json:"type"`
	UserName string `json:"user"`
	IP       string `json:"ip"`
	Origin   string `json:"origin"`
	Device   string `json:"device"`
	Remote   bool   `json:"remote"`
}

const openGap = 30 * 60 // seconds without use before the next use counts as "opened"

func (a *Auth) Record(x Access) {
	if x.Type != "refused" && x.Type != "google" && testUser(x.UserName) {
		return
	}
	x.At = time.Now().Unix()
	x.Remote = x.Origin == "remote"
	a.db.Exec(`INSERT INTO access(at,type,username,ip,origin,device) VALUES(?,?,?,?,?,?)`, x.At, x.Type, x.UserName, x.IP, x.Origin, x.Device)
	a.db.Exec(`DELETE FROM access WHERE at < ?`, x.At-90*86400)
	if a.OnAccess != nil {
		a.OnAccess(x)
	}
}

// Accesses: newest first; types = allowed types.
func (a *Auth) Accesses(limit int, since int64, types ...string) []Access {
	q := `SELECT at,type,username,ip,origin,device FROM access WHERE at >= ? AND type IN (?` + strings.Repeat(",?", len(types)-1) + `) ORDER BY at DESC LIMIT ?`
	args := []any{since}
	for _, t := range types {
		args = append(args, t)
	}
	args = append(args, limit)
	out := []Access{}
	rows, err := a.db.Query(q, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var x Access
		rows.Scan(&x.At, &x.Type, &x.UserName, &x.IP, &x.Origin, &x.Device)
		x.Remote = x.Origin == "remote"
		out = append(out, x)
	}
	return out
}

func newAuth(db *sql.DB) *Auth { return &Auth{db: db, fails: map[string][]time.Time{}} }

// cleanup: forgets old failed logins and deletes sessions that have expired (90 days without use).
func (a *Auth) cleanup() {
	now := time.Now()
	a.mu.Lock()
	for k := range a.fails {
		a.recentFails(k, now)
	}
	a.mu.Unlock()
	a.db.Exec(`DELETE FROM sessions WHERE last_used < ?`, now.Unix()-90*86400)
}

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var admin int
	var cams string
	if err := row.Scan(&u.ID, &u.Name, &admin, &cams); err != nil {
		return nil, err
	}
	u.Admin = admin == 1
	if cams != "" {
		u.Cams = strings.Split(cams, ",")
	}
	return &u, nil
}

var errWrongCurrent = errors.New("the current password is wrong")

var errLocked = errors.New("too many failed attempts – try again in 15 minutes")

// recentFails: failures within the last 15 minutes (old ones are dropped). a.mu must be held.
func (a *Auth) recentFails(key string, now time.Time) int {
	recent := a.fails[key][:0]
	for _, t := range a.fails[key] {
		if now.Sub(t) < 15*time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) == 0 {
		delete(a.fails, key)
	} else {
		a.fails[key] = recent
	}
	return len(recent)
}

// Login: at most 5 failed attempts per IP and 10 per username within 15 minutes.
func (a *Auth) Login(ip, name, pw, device string) (string, *User, error) {
	a.mu.Lock()
	now := time.Now()
	userKey := "user:" + strings.ToLower(strings.TrimSpace(name))
	locked := a.recentFails("ip:"+ip, now) >= 5 || a.recentFails(userKey, now) >= 10
	a.mu.Unlock()
	if locked {
		return "", nil, errLocked
	}
	var stored string
	row := a.db.QueryRow(`SELECT id,name,admin,cams,pw FROM users WHERE name=?`, name)
	var u User
	var admin int
	var cams string
	err := row.Scan(&u.ID, &u.Name, &admin, &cams, &stored)
	if err != nil || !strings.HasPrefix(stored, "pbkdf2$") {
		stored = dummyHash() // unknown user, or a Home Assistant user (no password): same work, always refused
	}
	if ok := checkPassword(stored, pw); err != nil || !ok || stored == dummyHash() {
		a.mu.Lock()
		a.fails["ip:"+ip] = append(a.fails["ip:"+ip], now)
		a.fails[userKey] = append(a.fails[userKey], now)
		a.mu.Unlock()
		return "", nil, errors.New("wrong username or password")
	}
	u.Admin = admin == 1
	if cams != "" {
		u.Cams = strings.Split(cams, ",")
	}
	b := make([]byte, 32)
	rand.Read(b)
	tok := base64.RawURLEncoding.EncodeToString(b)
	_, err = a.db.Exec(`INSERT INTO sessions(token,user_id,device,created,last_used) VALUES(?,?,?,?,?)`,
		tokenHash(tok), u.ID, device, now.Unix(), now.Unix())
	return tok, &u, err
}

// User finds the user behind a token. Sessions expire after 90 days without use.
// If the session has not been used for openGap, the access is recorded as "opened" (the app opened again).
func (a *Auth) User(tok, ip, origin string) *User {
	if tok == "" {
		return nil
	}
	h := tokenHash(tok)
	now := time.Now().Unix()
	var u User
	var admin int
	var cams, device string
	var last int64
	err := a.db.QueryRow(`SELECT u.id,u.name,u.admin,u.cams,COALESCE(s.device,''),s.last_used FROM sessions s JOIN users u ON u.id=s.user_id
		WHERE s.token=? AND s.last_used > ?`, h, now-90*86400).Scan(&u.ID, &u.Name, &admin, &cams, &device, &last)
	if err != nil {
		return nil
	}
	u.Admin = admin == 1
	if cams != "" {
		u.Cams = strings.Split(cams, ",")
	}
	if now-last >= 60 {
		a.db.Exec(`UPDATE sessions SET last_used=? WHERE token=?`, now, h)
	}
	if now-last >= openGap && device != integrationDevice { // the integration is not a person opening the app
		log.Printf("Opened: %s from %s (%s)", u.Name, ip, device)
		a.Record(Access{Type: "opened", UserName: u.Name, IP: ip, Origin: origin, Device: device})
	}
	return &u
}

func (a *Auth) Logout(tok string) { a.db.Exec(`DELETE FROM sessions WHERE token=?`, tokenHash(tok)) }

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if t, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(t)
	}
	return ""
}

// ChangePassword: users change their own password (requires the current one). Other devices are logged out, this one is kept.
func (a *Auth) ChangePassword(u *User, tok, oldPw, newPw string) error {
	if len([]rune(newPw)) < 8 {
		return errors.New("the new password must be at least 8 characters")
	}
	var stored string
	if err := a.db.QueryRow(`SELECT pw FROM users WHERE id=?`, u.ID).Scan(&stored); err != nil || !checkPassword(stored, oldPw) {
		return errWrongCurrent
	}
	if _, err := a.db.Exec(`UPDATE users SET pw=? WHERE id=?`, hashPassword(newPw), u.ID); err != nil {
		return err
	}
	a.db.Exec(`DELETE FROM sessions WHERE user_id=? AND token<>?`, u.ID, tokenHash(tok))
	return nil
}

// testUser: test users (names starting with "tmp") are not recorded in the access log.
func testUser(name string) bool { return strings.HasPrefix(strings.ToLower(name), "tmp") }

package main

// ArgusSen as a Home Assistant app: the user interface is shown in HA's sidebar via ingress.
// HA has already logged the user in, and the Supervisor sends who it is in X-Remote-User-* headers.
// The headers are used ONLY when "ingress" is enabled in the configuration AND the request comes from the Supervisor's
// ingress proxy (172.30.32.2). A server without "ingress" is not affected.

import (
	"bufio"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const ingressProxy = "172.30.32.2"

func (s *Server) isIngress(r *http.Request) bool {
	if !s.cfg.Ingress {
		return false
	}
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	return err == nil && h == ingressProxy
}

// ingressPrefix: the path HA shows the app under (e.g. /api/hassio_ingress/<key>), without a trailing "/".
// Empty outside ingress. Used for addresses ArgusSen writes itself (e.g. in playlists).
func (s *Server) ingressPrefix(r *http.Request) string {
	if !s.isIngress(r) {
		return ""
	}
	return strings.TrimRight(r.Header.Get("X-Ingress-Path"), "/")
}

type haCached struct {
	u     User
	until time.Time
}

var ingressCache = struct {
	sync.Mutex
	m map[string]haCached
}{m: map[string]haCached{}}

// ingressUser: the ArgusSen user for the HA user who opened the app (nil outside ingress).
// The user is found by HA's user id (not the name). A new user is created: HA administrators see all cameras,
// others the cameras chosen under Settings → Users (default: none). Admin follows HA (checked at most every 5 minutes).
func (s *Server) ingressUser(r *http.Request) *User {
	if !s.isIngress(r) {
		return nil
	}
	id := r.Header.Get("X-Remote-User-Id")
	name := strings.TrimSpace(r.Header.Get("X-Remote-User-Name"))
	if id == "" || name == "" {
		return nil
	}
	ingressCache.Lock()
	c, ok := ingressCache.m[id]
	ingressCache.Unlock()
	if ok && time.Now().Before(c.until) {
		u := c.u
		return &u
	}
	ingressCache.Lock()
	for k, v := range ingressCache.m { // forget users not seen for a while
		if time.Since(v.until) > time.Hour {
			delete(ingressCache.m, k)
		}
	}
	ingressCache.Unlock()
	var admin *bool
	if a, err := haIsAdmin(id); err != nil {
		log.Printf("HA: could not check whether %s is an administrator: %v", name, err)
	} else {
		admin = &a
	}
	u, created, err := s.auth.EnsureUser(id, name, admin, s.ev.settings.GetKV("ha_new_user_cams"))
	if err != nil {
		log.Printf("HA: user %s: %v", name, err)
		return nil
	}
	if created {
		log.Printf("New user from Home Assistant: %s (admin: %v)", u.Name, u.Admin)
	}
	if !ok {
		log.Printf("Opened: %s via Home Assistant", u.Name)
		s.auth.Record(Access{Type: "opened", UserName: u.Name, IP: r.Header.Get("X-Forwarded-For"), Origin: "ha", Device: "Home Assistant"})
	}
	ingressCache.Lock()
	ingressCache.m[id] = haCached{u: *u, until: time.Now().Add(5 * time.Minute)}
	ingressCache.Unlock()
	return u
}

// EnsureUser finds or creates the user for a Home Assistant user (haID = HA's user id, which never changes).
// admin = nil: leave the admin flag (HA could not be asked; a new user is then not admin). New users get an
// unusable password (they log in through HA) and the cameras newCams (administrators: all).
func (a *Auth) EnsureUser(haID, name string, admin *bool, newCams string) (*User, bool, error) {
	u, err := scanUser(a.db.QueryRow(`SELECT id,name,admin,cams FROM users WHERE ha_id=?`, haID))
	adopted := false
	if errors.Is(err, sql.ErrNoRows) {
		// created by an older version (linked by name): adopt it – only users that were made by Home Assistant
		u, err = scanUser(a.db.QueryRow(`SELECT id,name,admin,cams FROM users WHERE name=? AND ha_id IS NULL AND pw LIKE 'ha$%'`, name))
		if err == nil {
			a.db.Exec(`UPDATE users SET ha_id=? WHERE id=?`, haID, u.ID)
			adopted = true
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		ad := admin != nil && *admin
		cams := newCams
		if ad {
			cams = "*"
		}
		b := make([]byte, 24)
		rand.Read(b)
		// the name may already belong to an ArgusSen user of its own – that account is never taken over
		uname := name
		for i := 1; ; i++ {
			var n int
			a.db.QueryRow(`SELECT COUNT(*) FROM users WHERE name=?`, uname).Scan(&n)
			if n == 0 {
				break
			}
			uname = fmt.Sprintf("%s (HA%s)", name, map[bool]string{true: "", false: " " + fmt.Sprint(i)}[i == 1])
		}
		res, err := a.db.Exec(`INSERT INTO users(name,pw,admin,cams,ha_id) VALUES(?,?,?,?,?)`, uname, "ha$"+hex.EncodeToString(b), boolInt(ad), cams, haID)
		if err != nil {
			return nil, false, err
		}
		id, _ := res.LastInsertId()
		nu := &User{ID: id, Name: uname, Admin: ad}
		if cams != "" {
			nu.Cams = strings.Split(cams, ",")
		}
		return nu, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	// renamed in Home Assistant: follow, unless the new name is taken by another user
	if name != u.Name {
		var n int
		a.db.QueryRow(`SELECT COUNT(*) FROM users WHERE name=? AND id<>?`, name, u.ID).Scan(&n)
		if n == 0 {
			a.db.Exec(`UPDATE users SET name=? WHERE id=?`, name, u.ID)
			u.Name = name
		}
	}
	if admin != nil && *admin != u.Admin {
		if _, err := a.db.Exec(`UPDATE users SET admin=? WHERE id=?`, boolInt(*admin), u.ID); err != nil {
			return nil, false, err
		}
		if *admin {
			log.Printf("HA: %s is now an administrator", u.Name)
		} else {
			log.Printf("HA: %s is no longer an administrator", u.Name)
			adopted = true // "all cameras" came with the admin rights
		}
		u.Admin = *admin
	}
	// Older versions gave every Home Assistant user all cameras, and HA administrators have all: a user who is (no
	// longer) an administrator gets what new Home Assistant users get – an administrator chooses more under Settings → Users.
	if adopted && !u.Admin && u.CanSee("*") && newCams != "*" {
		if _, err := a.db.Exec(`UPDATE users SET cams=? WHERE id=?`, newCams, u.ID); err == nil {
			log.Printf("HA: %s is not an administrator – sees the cameras chosen for new Home Assistant users", u.Name)
			u.Cams = nil
			if newCams != "" {
				u.Cams = strings.Split(newCams, ",")
			}
		}
	}
	return u, false, nil
}

// forgetIngressUsers: the cached Home Assistant users are read again at the next request (after a change of rights).
func forgetIngressUsers() {
	ingressCache.Lock()
	ingressCache.m = map[string]haCached{}
	ingressCache.Unlock()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// haIsAdmin asks Home Assistant (websocket via the Supervisor) whether the user is an administrator.
func haIsAdmin(userID string) (bool, error) {
	var users []struct {
		ID       string   `json:"id"`
		GroupIDs []string `json:"group_ids"`
	}
	if err := haWS("config/auth/list", &users); err != nil {
		return false, err
	}
	for _, u := range users {
		if u.ID == userID {
			for _, g := range u.GroupIDs {
				if g == "system-admin" {
					return true, nil
				}
			}
			return false, nil
		}
	}
	return false, fmt.Errorf("unknown HA user %s", userID)
}

// haWS sends one command to HA's websocket through the Supervisor and decodes "result".
// Small websocket client of our own (only what is needed: text frames, masking, fragments).
func haWS(cmd string, out any) error { return haWSCmd(map[string]any{"type": cmd}, out) }

// haWSCmd sends one command with fields (e.g. {"type": "config_entries/get", "domain": "hassio"}).
func haWSCmd(msg map[string]any, out any) error {
	cmd, _ := msg["type"].(string)
	tok := os.Getenv("SUPERVISOR_TOKEN")
	if tok == "" {
		return errors.New("SUPERVISOR_TOKEN is missing (homeassistant_api must be enabled)")
	}
	conn, err := net.DialTimeout("tcp", "supervisor:80", 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(15 * time.Second))
	key := make([]byte, 16)
	rand.Read(key)
	fmt.Fprintf(conn, "GET /core/websocket HTTP/1.1\r\nHost: supervisor\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\nAuthorization: Bearer %s\r\n\r\n",
		hex.EncodeToString(key)[:22]+"==", tok)
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		return fmt.Errorf("websocket: HTTP %d", resp.StatusCode)
	}
	read := func() (map[string]json.RawMessage, error) {
		msg, err := wsRead(br)
		if err != nil {
			return nil, err
		}
		var m map[string]json.RawMessage
		return m, json.Unmarshal(msg, &m)
	}
	typ := func(m map[string]json.RawMessage) string { var t string; json.Unmarshal(m["type"], &t); return t }
	if m, err := read(); err != nil || typ(m) != "auth_required" {
		return fmt.Errorf("websocket: unexpected start (%v)", err)
	}
	b, _ := json.Marshal(map[string]string{"type": "auth", "access_token": tok})
	wsWrite(conn, b)
	if m, err := read(); err != nil || typ(m) != "auth_ok" {
		return fmt.Errorf("websocket: login refused (%v)", err)
	}
	msg["id"] = 1
	b, _ = json.Marshal(msg)
	wsWrite(conn, b)
	for {
		m, err := read()
		if err != nil {
			return err
		}
		if typ(m) != "result" {
			continue
		}
		var ok bool
		json.Unmarshal(m["success"], &ok)
		if !ok {
			return fmt.Errorf("%s: %s", cmd, m["error"])
		}
		return json.Unmarshal(m["result"], out)
	}
}

func wsWrite(w io.Writer, data []byte) error {
	hdr := []byte{0x81}
	n := len(data)
	switch {
	case n < 126:
		hdr = append(hdr, 0x80|byte(n))
	case n < 65536:
		hdr = append(hdr, 0x80|126, byte(n>>8), byte(n))
	default:
		hdr = append(hdr, 0x80|127)
		hdr = binary.BigEndian.AppendUint64(hdr, uint64(n))
	}
	mask := make([]byte, 4)
	rand.Read(mask)
	hdr = append(hdr, mask...)
	buf := make([]byte, n)
	for i := range data {
		buf[i] = data[i] ^ mask[i%4]
	}
	_, err := w.Write(append(hdr, buf...))
	return err
}

// wsRead reads one whole message (joins fragments, skips ping/pong).
func wsRead(r *bufio.Reader) ([]byte, error) {
	var msg []byte
	for {
		h := make([]byte, 2)
		if _, err := io.ReadFull(r, h); err != nil {
			return nil, err
		}
		fin, op := h[0]&0x80 != 0, h[0]&0x0f
		n := uint64(h[1] & 0x7f)
		switch n {
		case 126:
			b := make([]byte, 2)
			if _, err := io.ReadFull(r, b); err != nil {
				return nil, err
			}
			n = uint64(binary.BigEndian.Uint16(b))
		case 127:
			b := make([]byte, 8)
			if _, err := io.ReadFull(r, b); err != nil {
				return nil, err
			}
			n = binary.BigEndian.Uint64(b)
		}
		if n > 64<<20 {
			return nil, errors.New("websocket: message too large")
		}
		var mask []byte
		if h[1]&0x80 != 0 {
			mask = make([]byte, 4)
			if _, err := io.ReadFull(r, mask); err != nil {
				return nil, err
			}
		}
		p := make([]byte, n)
		if _, err := io.ReadFull(r, p); err != nil {
			return nil, err
		}
		for i := range p {
			if mask != nil {
				p[i] ^= mask[i%4]
			}
		}
		if op == 8 {
			return nil, errors.New("websocket closed")
		}
		if op >= 8 { // ping/pong
			continue
		}
		msg = append(msg, p...)
		if fin {
			return msg, nil
		}
	}
}

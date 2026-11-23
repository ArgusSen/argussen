package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"sort"
	"strings"
	"time"

	"argus/bc"
)

// Admin API for the app's settings: cameras and users. Everything requires admin.

func (s *Server) adminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/cameras", s.admin(s.listCams))
	mux.HandleFunc("POST /api/admin/cameras", s.admin(s.createCam))
	mux.HandleFunc("PUT /api/admin/cameras/{id}", s.admin(s.updateCam))
	mux.HandleFunc("PUT /api/admin/cameras/order", s.admin(s.orderCams))
	mux.HandleFunc("PUT /api/admin/cameras/{id}/enabled", s.admin(s.setCamEnabled))
	mux.HandleFunc("DELETE /api/admin/cameras/{id}", s.admin(s.deleteCam))
	mux.HandleFunc("POST /api/admin/cameras/test", s.admin(s.testCam))
	mux.HandleFunc("GET /api/admin/cameras/discover", s.admin(s.discoverCams))
	mux.HandleFunc("GET /api/admin/users", s.admin(s.listUsers))
	mux.HandleFunc("POST /api/admin/users", s.admin(s.createUser))
	mux.HandleFunc("PUT /api/admin/users/{name}", s.admin(s.updateUser))
	mux.HandleFunc("DELETE /api/admin/users/{name}", s.admin(s.deleteUser))
	mux.HandleFunc("POST /api/admin/users/{name}/logout", s.admin(s.logoutUser))
	mux.HandleFunc("GET /api/admin/storage", s.admin(s.getStorage))
	mux.HandleFunc("PUT /api/admin/storage", s.admin(s.putStorage))
	mux.HandleFunc("PUT /api/admin/language", s.admin(s.putLanguage))
	mux.HandleFunc("GET /api/admin/retention", s.admin(s.getRetention))
	mux.HandleFunc("PUT /api/admin/retention", s.admin(s.putRetention))
	mux.HandleFunc("PUT /api/admin/storage/limit", s.admin(s.putStorageLimit))
	mux.HandleFunc("GET /api/admin/ha", s.admin(s.getHAOptions))
	mux.HandleFunc("PUT /api/admin/ha", s.admin(s.putHAOptions))
}

func (s *Server) admin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if u := userOf(r); u == nil || !u.Admin {
			jsonErr(w, 403, "admin only")
			return
		}
		h(w, r)
	}
}

// ---------- cameras ----------

type camOut struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Host        string   `json:"host"`
	Port        int      `json:"port"`
	BCPort      int      `json:"bc_port"`
	Protos      string   `json:"protos"`
	Kind        string   `json:"kind"`
	OnvifPort   int      `json:"onvif_port"`
	MainPath    string   `json:"main_path"`
	SubPath     string   `json:"sub_path"`
	Enabled     bool     `json:"enabled"`
	Optional    bool     `json:"optional"`
	HAName      string   `json:"ha_name"`
	HAArea      string   `json:"ha_area"`
	User        string   `json:"user"`
	HasPassword bool     `json:"has_password"`
	Users       []string `json:"users"` // users who may see the camera (admins with "all" are shown as "name (all)")
	MainOK      bool     `json:"main_ok"`
	SubOK       bool     `json:"sub_ok"`
	MainVia     string   `json:"main_via"`
	SubVia      string   `json:"sub_via"`
	Error       string   `json:"error,omitempty"`
}

type camIn struct {
	Name      string   `json:"name"`
	Host      string   `json:"host"`
	Port      int      `json:"port"`
	BCPort    int      `json:"bc_port"`
	Protos    string   `json:"protos"`
	Kind      string   `json:"kind"`
	OnvifPort int      `json:"onvif_port"`
	MainPath  string   `json:"main_path"`
	SubPath   string   `json:"sub_path"`
	Enabled   *bool    `json:"enabled"`
	Optional  bool     `json:"optional"`
	HAName    string   `json:"ha_name"`
	HAArea    string   `json:"ha_area"`
	User      string   `json:"user"`
	Pass      string   `json:"pass"`
	Users     []string `json:"users"`
}

type userRow struct {
	ID    int64
	Name  string
	Admin bool
	Cams  []string
	HA    bool // logs in through Home Assistant (admin follows HA)
}

func (s *Server) userRows() []userRow {
	var out []userRow
	// the integration's internal user is not shown (integration.go)
	rows, err := s.auth.db.Query(`SELECT id,name,admin,cams,ha_id IS NOT NULL FROM users WHERE COALESCE(ha_id,'')<>'argussen-integration' ORDER BY name`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var u userRow
		var admin int
		var c string
		rows.Scan(&u.ID, &u.Name, &admin, &c, &u.HA)
		u.Admin = admin == 1
		if c != "" {
			u.Cams = strings.Split(c, ",")
		}
		out = append(out, u)
	}
	return out
}

func hasCam(list []string, id string) bool {
	for _, c := range list {
		if c == "*" || c == id {
			return true
		}
	}
	return false
}

func (s *Server) camToOut(c *Camera) camOut {
	kind := c.Kind
	if kind == "" {
		kind = "reolink"
	}
	o := camOut{ID: c.ID, Name: c.Name, Host: c.Host, Port: c.Port, BCPort: c.bcPort(), Protos: strings.Join(c.protos("main"), ","),
		Kind: kind, OnvifPort: c.OnvifPort, MainPath: c.Main, SubPath: c.Sub,
		Enabled: c.Enabled, Optional: c.Optional, HAName: c.HAName, HAArea: c.HAArea, User: c.user, HasPassword: c.pass != "",
		Users: []string{}}
	for _, u := range s.userRows() {
		if hasCam(u.Cams, c.ID) {
			n := u.Name
			if len(u.Cams) == 1 && u.Cams[0] == "*" {
				n += " (all)"
			}
			o.Users = append(o.Users, n)
		}
	}
	if rn := s.runner(c.ID); rn != nil {
		st := rn.State()
		o.MainOK, o.SubOK, o.MainVia, o.SubVia = st.MainOK, st.SubOK, st.MainVia, st.SubVia
		if !st.SubOK {
			o.Error = st.SubErr
		}
	}
	return o
}

func (s *Server) listCams(w http.ResponseWriter, r *http.Request) {
	out := []camOut{}
	for _, c := range cams.All() {
		out = append(out, s.camToOut(c))
	}
	writeJSON(w, out)
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(v); err != nil {
		jsonErr(w, 400, "invalid request")
		return false
	}
	return true
}

// applyCam puts the fields from the app onto a camera. Empty password = unchanged.
func applyCam(c *Camera, in camIn) error {
	in.Name, in.Host = strings.TrimSpace(in.Name), strings.TrimSpace(in.Host)
	if in.Name == "" || in.Host == "" {
		return errors.New("name and address must be filled in")
	}
	if !validHost(in.Host) {
		return errors.New("the address must be an IP address or a host name (without http:// or a port)")
	}
	c.Name, c.Host = in.Name, in.Host
	if in.Port > 0 {
		c.Port = in.Port
	}
	if in.BCPort > 0 && in.BCPort != 9000 {
		c.BCPort = in.BCPort
	} else {
		c.BCPort = 0
	}
	switch in.Kind {
	case "", "reolink":
		c.Kind = ""
	case "onvif", "rtsp":
		c.Kind = in.Kind
	default:
		return errors.New("unknown camera type")
	}
	if c.reolink() {
		if in.Protos != "" {
			tmp := Camera{MainProto: in.Protos}
			p := strings.Join(tmp.protos("main"), ",")
			c.MainProto, c.SubProto = p, p
		}
		c.Main, c.Sub, c.OnvifPort = "", "", 0 // Reolink's own paths (normalizeCam)
	} else {
		c.MainProto, c.SubProto, c.BCPort = "rtsp", "rtsp", 0
		c.Main, c.Sub = strings.TrimSpace(in.MainPath), strings.TrimSpace(in.SubPath)
		c.OnvifPort = 0
		if c.Kind == "onvif" {
			c.OnvifPort = in.OnvifPort
			if c.OnvifPort <= 0 {
				c.OnvifPort = 80
			}
		}
		for _, p := range []string{c.Main, c.Sub} {
			if !validStreamPath(p) {
				return errors.New("the stream address must be a path or an rtsp:// address, without spaces or a login – fill in username and password instead")
			}
		}
		if c.Kind == "rtsp" && (c.Main == "" || c.Sub == "") {
			return errors.New("fill in both the main stream and the sub stream")
		}
	}
	if in.Enabled != nil {
		c.Enabled = *in.Enabled
	}
	c.Optional, c.HAName, c.HAArea = in.Optional, strings.TrimSpace(in.HAName), strings.TrimSpace(in.HAArea)
	if u := strings.TrimSpace(in.User); u != "" {
		c.user = u
	}
	if in.Pass != "" {
		c.pass = in.Pass
	}
	if c.reolink() && (c.user == "" || c.pass == "") {
		return errors.New("the camera's username and password must be filled in")
	}
	return nil
}

// resolveOnvif fills in the RTSP addresses of an ONVIF camera (if they are not known yet).
func resolveOnvif(ctx context.Context, c *Camera, force bool) (*onvifInfo, error) {
	if c.Kind != "onvif" || (!force && c.Main != "" && c.Sub != "") {
		return nil, nil
	}
	info, err := onvifStreams(ctx, c.Host, c.OnvifPort, c.user, c.pass)
	if err != nil {
		return nil, err
	}
	if info.Sub == "" {
		return info, errors.New("the camera has only one stream – ArgusSen needs a sub stream (low resolution) as well")
	}
	if !validStreamPath(info.Main) || !validStreamPath(info.Sub) {
		return info, errors.New("ONVIF: the camera reported an address that is not rtsp://")
	}
	c.Main, c.Sub = info.Main, info.Sub
	return info, nil
}

// setCamUsers: exactly these users (besides those with "all") may see the camera.
func (s *Server) setCamUsers(id string, names []string) error {
	want := map[string]bool{}
	for _, n := range names {
		want[strings.ToLower(strings.TrimSuffix(n, " (all)"))] = true
	}
	users := s.userRows() // read before the transaction: the database has one connection, which the transaction holds
	defer forgetIngressUsers()
	return withTx(s.auth.db, func(tx *sql.Tx) error {
		for _, u := range users {
			if len(u.Cams) == 1 && u.Cams[0] == "*" {
				continue
			}
			has := hasCam(u.Cams, id)
			want2 := want[strings.ToLower(u.Name)]
			if has == want2 {
				continue
			}
			var kept []string
			for _, c := range u.Cams {
				if c != id {
					kept = append(kept, c)
				}
			}
			if want2 {
				kept = append(kept, id)
			}
			if _, err := tx.Exec(`UPDATE users SET cams=? WHERE id=?`, strings.Join(kept, ","), u.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Server) haRefresh(deleted string) {
	hub.publish("cameras", "", map[string]bool{"changed": true}) // the integration reads the camera list again
	if s.ha == nil {
		return
	}
	if deleted != "" {
		s.ha.pub(s.ha.prefix+"/device/argussen_"+deleted+"/config", []byte{})
	}
	s.ha.discovery()
	s.ha.states(true)
}

func (s *Server) createCam(w http.ResponseWriter, r *http.Request) {
	var in camIn
	if !readJSON(w, r, &in) {
		return
	}
	c := &Camera{Enabled: true}
	if in.Protos == "" {
		in.Protos = "auto"
	}
	if err := applyCam(c, in); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if _, err := resolveOnvif(r.Context(), c, false); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	normalizeCam(c)
	c.ID = cams.newID(c.Name)
	if err := cams.Save(c); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	if err := s.setCamUsers(c.ID, in.Users); err != nil {
		log.Printf("Camera %s: who may see it could not be saved: %v", c.ID, err)
	}
	s.startCam(c)
	s.haRefresh("")
	log.Printf("Camera %s (%s, %s) created by %s", c.ID, c.Name, c.Host, userOf(r).Name)
	writeJSON(w, s.camToOut(c))
}

func (s *Server) updateCam(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	old := cams.Get(id)
	if old == nil {
		jsonErr(w, 404, "the camera does not exist")
		return
	}
	var in camIn
	if !readJSON(w, r, &in) {
		return
	}
	c := *old // copy – the running camera uses the old one until it is restarted
	if err := applyCam(&c, in); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if _, err := resolveOnvif(r.Context(), &c, false); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	normalizeCam(&c)
	if err := cams.Save(&c); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	if in.Users != nil {
		if err := s.setCamUsers(id, in.Users); err != nil {
			jsonErr(w, 500, err.Error())
			return
		}
	}
	// restart only if something that affects the connection has changed
	connChanged := old.Host != c.Host || old.Port != c.Port || old.BCPort != c.BCPort || old.MainProto != c.MainProto ||
		old.Kind != c.Kind || old.Main != c.Main || old.Sub != c.Sub ||
		old.user != c.user || old.pass != c.pass || old.Enabled != c.Enabled
	if connChanged || s.runner(id) == nil {
		s.stopCam(id)
		s.startCam(&c)
	} else if rn := s.runner(id); rn != nil {
		rn.cam.Name, rn.cam.Optional, rn.cam.HAName, rn.cam.HAArea = c.Name, c.Optional, c.HAName, c.HAArea
	}
	s.haRefresh("")
	log.Printf("Camera %s changed by %s", id, userOf(r).Name)
	writeJSON(w, s.camToOut(&c))
}

// PUT /api/admin/cameras/{id}/enabled {"enabled": bool}: only switches a camera on or off ("Active" in the app) –
// used by the Home Assistant integration's switch, so automations can turn cameras on and off.
func (s *Server) setCamEnabled(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	old := cams.Get(id)
	if old == nil {
		jsonErr(w, 404, "the camera does not exist")
		return
	}
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Enabled == nil {
		jsonErr(w, 400, "invalid request")
		return
	}
	if old.Enabled == *in.Enabled {
		writeJSON(w, s.camToOut(old))
		return
	}
	c := *old
	c.Enabled = *in.Enabled
	if err := cams.Save(&c); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	s.stopCam(id)
	s.startCam(&c)
	s.haRefresh("")
	if c.Enabled {
		log.Printf("Camera %s switched on by %s", id, userOf(r).Name)
	} else {
		log.Printf("Camera %s switched off by %s", id, userOf(r).Name)
	}
	writeJSON(w, s.camToOut(&c))
}

// PUT /api/admin/cameras/order {"ids": [...]}: the order of the cameras everywhere (overview, live, timeline).
func (s *Server) orderCams(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []string `json:"ids"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if err := cams.Reorder(in.IDs); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	s.mu.Lock()
	ids := s.ids[:0:0]
	for _, c := range cams.All() {
		if s.cams[c.ID] != nil {
			ids = append(ids, c.ID)
		}
	}
	s.ids = ids
	s.mu.Unlock()
	s.haRefresh("")
	log.Printf("Camera order changed by %s", userOf(r).Name)
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) deleteCam(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if cams.Get(id) == nil {
		jsonErr(w, 404, "the camera does not exist")
		return
	}
	s.stopCam(id)
	if err := cams.Delete(id); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	if err := s.setCamUsers(id, nil); err != nil {
		log.Printf("Camera %s: who may see it could not be saved: %v", id, err)
	}
	s.haRefresh(id)
	log.Printf("Camera %s deleted by %s (recordings stay until cleanup)", id, userOf(r).Name)
	writeJSON(w, map[string]bool{"ok": true})
}

// testCam tries bc, flv and rtsp on the sub stream and returns an image from the first one that works.
func (s *Server) testCam(w http.ResponseWriter, r *http.Request) {
	var in struct {
		camIn
		ID string `json:"id"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	c := &Camera{}
	if old := cams.Get(in.ID); old != nil {
		*c = *old
		// the saved password is only used against the camera's own address – never sent to a new one
		if !strings.EqualFold(strings.TrimSpace(in.Host), old.Host) {
			c.pass = ""
		}
	}
	if in.Protos == "" {
		in.Protos = "auto"
	}
	if err := applyCam(c, in.camIn); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	normalizeCam(c)
	type res struct {
		Proto string `json:"proto"`
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
		Size  string `json:"size,omitempty"`
		Ms    int64  `json:"ms"`
	}
	var results []res
	var img []byte
	if !c.reolink() {
		s.testGeneric(w, r, c)
		return
	}
	var working []string
	for i, p := range []string{"bc", "flv", "rtsp"} {
		if i > 0 {
			time.Sleep(2 * time.Second) // Reolink does not like many connections right after each other
		}
		t0 := time.Now()
		jpg, size, err := snapshotVia(r.Context(), c, p)
		rr := res{Proto: p, OK: err == nil, Size: size, Ms: time.Since(t0).Milliseconds()}
		if err != nil {
			rr.Error = err.Error()
		} else {
			working = append(working, p)
			if img == nil {
				img = jpg
			}
		}
		results = append(results, rr)
	}
	out := map[string]any{"results": results, "protos": strings.Join(working, ",")}
	if img != nil {
		out["image"] = "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(img)
	}
	writeJSON(w, out)
}

// testGeneric tests an ONVIF or RTSP camera: finds the addresses (ONVIF), checks the main stream and takes a picture
// from the sub stream. The steps are shown one by one in the app (proto = "onvif", "main" or "sub").
func (s *Server) testGeneric(w http.ResponseWriter, r *http.Request, c *Camera) {
	type res struct {
		Proto string `json:"proto"`
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
		Size  string `json:"size,omitempty"`
		Ms    int64  `json:"ms"`
	}
	out := map[string]any{}
	var results []res
	defer func() { out["results"] = results; writeJSON(w, out) }()
	ctx := r.Context()
	if c.Kind == "onvif" {
		t0 := time.Now()
		info, err := resolveOnvif(ctx, c, true)
		rr := res{Proto: "onvif", OK: err == nil, Ms: time.Since(t0).Milliseconds()}
		if info != nil {
			rr.Size = strings.TrimSpace(info.Manufacturer + " " + info.Model)
			out["model"] = info.Model
		}
		if err != nil {
			rr.Error = err.Error()
			results = append(results, rr)
			return
		}
		results = append(results, rr)
	}
	out["main_path"], out["sub_path"] = c.Main, c.Sub
	var warn []string
	for _, stream := range []string{"main", "sub"} {
		t0 := time.Now()
		wd, ht, codec, err := probeStream(ctx, c.urlFor(stream, "rtsp"))
		rr := res{Proto: stream, OK: err == nil, Ms: time.Since(t0).Milliseconds()}
		if err != nil {
			rr.Error = "no picture – check the address, username and password"
		} else {
			rr.Size = fmt.Sprintf("%dx%d %s", wd, ht, strings.ToUpper(codec))
			if codec != "h264" {
				warn = append(warn, codec)
			}
			if stream == "sub" && subTooBig(wd, ht) {
				rr.OK, rr.Error = false, fmt.Sprintf("the sub stream is %dx%d – choose a lower resolution (at most about 1.3 megapixels) for it in the camera", wd, ht)
			}
		}
		results = append(results, rr)
		time.Sleep(time.Second)
	}
	if len(results) > 0 && results[len(results)-1].OK {
		if jpg, _, err := snapshotVia(ctx, c, "rtsp"); err == nil {
			out["image"] = "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpg)
		}
	}
	if len(warn) > 0 {
		out["warning"] = "not_h264"
	}
}

// snapshotVia fetches one image from the sub stream with a given method.
func snapshotVia(ctx context.Context, c *Camera, proto string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	jpgArgs := []string{"-frames:v", "1", "-vf", "scale=640:-2", "-f", "image2", "-c:v", "mjpeg", "-q:v", "5", "pipe:1"}
	var cmd *exec.Cmd
	var stdin []byte
	size := ""
	if proto == "bc" {
		var key []byte
		st := &bc.Stats{}
		cctx, ccancel := context.WithCancel(ctx)
		err := bc.Stream(cctx, fmt.Sprintf("%s:%d", c.Host, c.bcPort()), c.user, c.pass, "subStream", func(f bc.Frame) {
			if f.Key && key == nil {
				key = f.Data
				ccancel()
			}
		}, st)
		ccancel()
		if key == nil {
			if err == nil {
				err = errors.New("no image")
			}
			return nil, "", err
		}
		size = fmt.Sprintf("%dx%d", st.Width, st.Height)
		stdin = key
		cmd = exec.CommandContext(ctx, "ffmpeg", append([]string{"-nostdin", "-v", "error", "-f", "h264", "-i", "pipe:0"}, jpgArgs...)...)
		cmd.Stdin = bytes.NewReader(stdin)
	} else {
		args := inputArgs(c.urlFor("sub", proto))
		cmd = exec.CommandContext(ctx, "ffmpeg", append(args, jpgArgs...)...)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil || out.Len() == 0 {
		m := strings.TrimSpace(errb.String())
		m = hidePass(m, c.pass)
		if i := strings.LastIndex(m, "\n"); i >= 0 {
			m = m[i+1:]
		}
		if m == "" {
			m = "no answer"
		}
		if ctx.Err() != nil {
			m = "no answer within 20 s"
		}
		return nil, "", errors.New(m)
	}
	return out.Bytes(), size, nil
}

// ---------- users ----------

type userOut struct {
	Name    string   `json:"name"`
	Admin   bool     `json:"admin"`
	Cams    []string `json:"cams"` // ["*"] = all
	Devices int      `json:"devices"`
	Self    bool     `json:"self"`
	HA      bool     `json:"ha"` // a Home Assistant user: admin follows HA, and the user comes back when it opens ArgusSen
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	me := userOf(r)
	out := []userOut{}
	for _, u := range s.userRows() {
		var n int
		s.auth.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id=?`, u.ID).Scan(&n)
		c := u.Cams
		if c == nil {
			c = []string{}
		}
		out = append(out, userOut{Name: u.Name, Admin: u.Admin, Cams: c, Devices: n, Self: u.ID == me.ID, HA: u.HA})
	}
	writeJSON(w, out)
}

// GET/PUT /api/admin/ha {all_cams, images}: whether new Home Assistant users (not administrators) see all cameras,
// and whether camera images are sent to Home Assistant via MQTT (everybody with access to the broker can read them).
type haOptions struct {
	AllCams *bool `json:"all_cams"`
	Images  *bool `json:"images"`
}

func (s *Server) haOptionsOut() haOptions {
	all, img := s.ev.settings.GetKV("ha_new_user_cams") == "*", s.ev.settings.GetKV("ha_images") != "off"
	return haOptions{AllCams: &all, Images: &img}
}

func (s *Server) getHAOptions(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.haOptionsOut()) }

func (s *Server) putHAOptions(w http.ResponseWriter, r *http.Request) {
	var in haOptions
	if !readJSON(w, r, &in) {
		return
	}
	if in.AllCams != nil {
		s.ev.settings.SetKV("ha_new_user_cams", map[bool]string{true: "*", false: ""}[*in.AllCams])
	}
	if in.Images != nil {
		s.ev.settings.SetKV("ha_images", map[bool]string{true: "on", false: "off"}[*in.Images])
		s.haRefresh("") // the image entities are added or removed in Home Assistant
	}
	writeJSON(w, s.haOptionsOut())
}

type userIn struct {
	Name     string    `json:"name"`
	Password string    `json:"password"`
	Admin    *bool     `json:"admin"`
	Cams     *[]string `json:"cams"`
}

func cleanCams(in []string) (string, error) {
	var out []string
	for _, c := range in {
		c = strings.TrimSpace(c)
		if c == "*" {
			return "*", nil
		}
		if cams.Get(c) == nil {
			return "", fmt.Errorf("unknown camera %q", c)
		}
		out = append(out, c)
	}
	sort.Strings(out)
	return strings.Join(out, ","), nil
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var in userIn
	if !readJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || strings.ContainsAny(in.Name, " :/") {
		jsonErr(w, 400, "the username must not be empty or contain spaces")
		return
	}
	if len([]rune(in.Password)) < 8 {
		jsonErr(w, 400, "the password must be at least 8 characters")
		return
	}
	camsStr := ""
	if in.Cams != nil {
		var err error
		if camsStr, err = cleanCams(*in.Cams); err != nil {
			jsonErr(w, 400, err.Error())
			return
		}
	}
	admin := 0
	if in.Admin != nil && *in.Admin {
		admin = 1
	}
	if _, err := s.auth.db.Exec(`INSERT INTO users(name,pw,admin,cams) VALUES(?,?,?,?)`, in.Name, hashPassword(in.Password), admin, camsStr); err != nil {
		jsonErr(w, 400, "the username already exists")
		return
	}
	log.Printf("User %s created by %s", in.Name, userOf(r).Name)
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) findUser(name string) *userRow {
	for _, u := range s.userRows() {
		if strings.EqualFold(u.Name, name) {
			return &u
		}
	}
	return nil
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	u := s.findUser(r.PathValue("name"))
	if u == nil {
		jsonErr(w, 404, "the user does not exist")
		return
	}
	var in userIn
	if !readJSON(w, r, &in) {
		return
	}
	me := userOf(r)
	// check everything first, then save it all in one transaction (no half changes)
	if in.Admin != nil && u.ID == me.ID && !*in.Admin {
		jsonErr(w, 400, "you cannot remove your own admin rights")
		return
	}
	if in.Admin != nil && u.HA && *in.Admin != u.Admin {
		jsonErr(w, 400, "the admin rights of a Home Assistant user are set in Home Assistant")
		return
	}
	camsStr := ""
	if in.Cams != nil {
		var err error
		if camsStr, err = cleanCams(*in.Cams); err != nil {
			jsonErr(w, 400, err.Error())
			return
		}
	}
	if in.Password != "" && len([]rune(in.Password)) < 8 {
		jsonErr(w, 400, "the password must be at least 8 characters")
		return
	}
	err := withTx(s.auth.db, func(tx *sql.Tx) error {
		if in.Admin != nil {
			if _, err := tx.Exec(`UPDATE users SET admin=? WHERE id=?`, boolInt(*in.Admin), u.ID); err != nil {
				return err
			}
		}
		if in.Cams != nil {
			if _, err := tx.Exec(`UPDATE users SET cams=? WHERE id=?`, camsStr, u.ID); err != nil {
				return err
			}
		}
		if in.Password != "" {
			if _, err := tx.Exec(`UPDATE users SET pw=? WHERE id=?`, hashPassword(in.Password), u.ID); err != nil {
				return err
			}
			if u.ID != me.ID { // new password → log the user's phones out
				if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id=?`, u.ID); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	forgetIngressUsers()
	log.Printf("User %s changed by %s", u.Name, me.Name)
	writeJSON(w, map[string]bool{"ok": true})
}

// withTx runs f in a transaction: everything is saved, or nothing.
func withTx(db *sql.DB, f func(*sql.Tx) error) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if err := f(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	u := s.findUser(r.PathValue("name"))
	if u == nil {
		jsonErr(w, 404, "the user does not exist")
		return
	}
	if u.ID == userOf(r).ID {
		jsonErr(w, 400, "you cannot delete yourself")
		return
	}
	err := withTx(s.auth.db, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id=?`, u.ID); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM users WHERE id=?`, u.ID)
		return err
	})
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	forgetIngressUsers()
	log.Printf("User %s deleted by %s", u.Name, userOf(r).Name)
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) logoutUser(w http.ResponseWriter, r *http.Request) {
	u := s.findUser(r.PathValue("name"))
	if u == nil {
		jsonErr(w, 404, "the user does not exist")
		return
	}
	if _, err := s.auth.db.Exec(`DELETE FROM sessions WHERE user_id=?`, u.ID); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	log.Printf("User %s logged out on all devices by %s", u.Name, userOf(r).Name)
	writeJSON(w, map[string]bool{"ok": true})
}

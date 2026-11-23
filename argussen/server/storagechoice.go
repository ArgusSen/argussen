package main

// Storage choice in the Home Assistant app: where recordings are stored – Home Assistant's own disk or a network drive
// added in HA (Settings → System → Storage, usage "Media", shown under /media/<name>).
// The choice is stored in <data>/storage.json and takes effect when the app restarts (it does that itself).
// If the storage is on a network drive, it is watched that the drive is mounted. If it is not, recording
// falls back to local storage or pauses – otherwise the empty folder on HA's own disk would fill up without anyone noticing.
// The storage choice exists only in the Home Assistant app ("ingress": true). The watch also works on an ordinary server
// with "storage_mount" in the configuration (e.g. "/mnt/nas").

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const localStorageDir = "/media/argus"

type storageChoice struct {
	Path     string   `json:"path"`
	Mount    string   `json:"mount"`    // the network drive's mount point ("" = HA's own disk)
	Previous []string `json:"previous"` // earlier places: their recordings are still played back and cleaned up by age
}

type storagePlace struct {
	Path    string  `json:"path"`
	Name    string  `json:"name"`
	Type    string  `json:"type"` // "local" or "network"
	Mounted bool    `json:"mounted"`
	Current bool    `json:"current"`
	FreeGB  float64 `json:"free_gb"`
	TotalGB float64 `json:"total_gb"`
}

func storageChoiceFile(cfg *Config) string {
	p := filepath.Join(cfg.Data, "storage.json")
	if _, err := os.Stat(p); err != nil { // before 2026-11-20 the file was called lager.json
		if old := filepath.Join(cfg.Data, "lager.json"); fileExists(old) {
			os.Rename(old, p)
		}
	}
	return p
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// applyStorageChoice reads the stored choice (Home Assistant app) and sets cfg.Storage/StorageMount.
func applyStorageChoice(cfg *Config) {
	b, err := os.ReadFile(storageChoiceFile(cfg))
	if err != nil {
		return
	}
	var v storageChoice
	if json.Unmarshal(b, &v) == nil && v.Path != "" {
		cfg.Storage, cfg.StorageMount, cfg.PreviousStorage = v.Path, v.Mount, v.Previous
	}
}

// netMounts: network drives (cifs/nfs) mounted directly under /media.
func netMounts() map[string]string {
	out := map[string]string{}
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		p := strings.Fields(sc.Text())
		if len(p) < 3 {
			continue
		}
		mp := strings.ReplaceAll(p[1], `\040`, " ")
		switch p[2] {
		case "cifs", "smb3", "nfs", "nfs4":
			if filepath.Dir(mp) == "/media" {
				out[mp] = p[2]
			}
		}
	}
	return out
}

// storageMounted: is the network drive (cifs/nfs) mounted on mount? Used both in the HA app (/media/<drive>)
// and on an ordinary server ("storage_mount": "/mnt/nas"). "" = nothing to check.
func storageMounted(mount string) bool {
	if mount == "" {
		return true
	}
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return true // cannot check – do not stop the recording
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		p := strings.Fields(sc.Text())
		if len(p) >= 3 && strings.ReplaceAll(p[1], `\040`, " ") == mount {
			switch p[2] {
			case "cifs", "smb3", "nfs", "nfs4":
				return true
			}
		}
	}
	return false
}

func diskGB(p string) (free, total float64) {
	var st syscall.Statfs_t
	if syscall.Statfs(p, &st) != nil {
		return 0, 0
	}
	return float64(st.Bavail) * float64(st.Bsize) / 1e9, float64(st.Blocks) * float64(st.Bsize) / 1e9
}

func (s *Server) storagePlaces() []storagePlace {
	if !s.cfg.Ingress { // the standalone server: the folder is chosen outside ArgusSen (a Docker volume)
		typ := "local"
		if s.cfg.StorageMount != "" || isNetwork(s.cfg.Storage) {
			typ = "network"
		}
		p := storagePlace{Path: s.cfg.Storage, Name: "The recordings folder", Type: typ, Current: true, Mounted: true}
		p.FreeGB, p.TotalGB = diskGB(s.cfg.Storage)
		return []storagePlace{p}
	}
	places := []storagePlace{{Path: localStorageDir, Name: "Home Assistant's own disk", Type: "local", Mounted: true}}
	for mp := range netMounts() {
		places = append(places, storagePlace{Path: filepath.Join(mp, "argus"), Name: "Network drive " + filepath.Base(mp), Type: "network", Mounted: true})
	}
	found := false
	for i := range places {
		if places[i].Path == s.cfg.Storage {
			places[i].Current, found = true, true
		}
	}
	if !found { // the current location no longer exists (e.g. network drive not mounted)
		typ, placeName := "local", "Current storage"
		if s.cfg.StorageMount != "" {
			typ, placeName = "network", "Network drive "+filepath.Base(s.cfg.StorageMount)
		}
		places = append(places, storagePlace{Path: s.cfg.Storage, Name: placeName, Type: typ, Current: true, Mounted: storageMounted(s.cfg.StorageMount)})
	}
	for i := range places {
		p := places[i].Path
		if places[i].Type == "local" {
			p = "/media"
		} else if !places[i].Mounted {
			continue
		} else {
			p = filepath.Dir(p)
		}
		places[i].FreeGB, places[i].TotalGB = diskGB(p)
	}
	return places
}

func (s *Server) getStorage(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Ingress && !s.cfg.Standalone {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, map[string]any{"current": s.cfg.Storage, "ok": !s.storagePaused.Load(), "places": s.storagePlaces(),
		"retention": s.ev.settings.Retention(), "estimate_gb": s.estimateGB(s.ev.settings.Retention()), "min_free_gb": minFreeFor(s.cfg, store.main),
		"retention_default": s.ev.settings.RetentionIsDefault(), "default_retention": s.ev.settings.DefaultRetention(),
		"used_gb": currentUsedGB(), "max_gb": s.ev.settings.MaxGB()})
}

// usedGB: the space ArgusSen's recordings and thumbnails take in root (-1 if it takes longer than limit,
// e.g. on a slow network drive).
func usedGB(root string, limit time.Duration) float64 {
	b := usedBytes(root, limit)
	if b < 0 {
		return -1
	}
	return math.Round(float64(b)/1e9*10) / 10
}

// currentUsedGB: ArgusSen's use of the main storage – the background measurement when there is one.
func currentUsedGB() float64 {
	if b := usage.Load(); b >= 0 {
		return math.Round(float64(b)/1e9*10) / 10
	}
	return usedGB(store.main, 5*time.Second)
}

// usedBytes: the bytes ArgusSen's recordings and thumbnails take in root (-1 if it takes longer than limit).
func usedBytes(root string, limit time.Duration) int64 {
	deadline := time.Now().Add(limit)
	var total int64
	stop := errors.New("stop")
	for _, d := range []string{"rec", "events"} {
		err := filepath.WalkDir(filepath.Join(root, d), func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if time.Now().After(deadline) {
				return stop
			}
			if !e.IsDir() {
				if fi, err := e.Info(); err == nil {
					total += fi.Size()
				}
			}
			return nil
		})
		if err == stop {
			return -1
		}
	}
	return total
}

// estimateGB: the space the recordings will take with the retention rt, from the size of each camera's newest
// complete one-minute files (SD for sub_days, HD for main_hours). HD around events comes on top.
func (s *Server) estimateGB(rt Retention) float64 {
	var total float64
	for _, id := range s.camIDs() {
		rn := s.runner(id)
		if rn == nil {
			continue
		}
		sub, main := float64(lastCompleteSize(rn.segDir("sub"))), float64(lastCompleteSize(rn.segDir("main")))
		total += sub*1440*float64(rt.SubDays) + main*60*float64(rt.MainHours)
	}
	return math.Round(total/1e9*10) / 10
}

// GET/PUT /api/admin/retention: how long recordings are kept (and the space it will take).
func (s *Server) retentionOut() map[string]any {
	rt := s.ev.settings.Retention()
	return map[string]any{"retention": rt, "estimate_gb": s.estimateGB(rt), "retention_default": s.ev.settings.RetentionIsDefault(),
		"default_retention": s.ev.settings.DefaultRetention(), "max_gb": s.ev.settings.MaxGB()}
}

func (s *Server) getRetention(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.retentionOut()) }

// PUT {sub_days, main_hours, event_days} saves a choice; {"default": true} goes back to the default for the storage.
func (s *Server) putRetention(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Retention
		Default bool `json:"default"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Default {
		if err := s.ev.settings.ResetRetention(); err != nil {
			jsonErr(w, 500, err.Error())
			return
		}
		rt := s.ev.settings.Retention()
		log.Printf("Recordings are kept: SD %d days, HD %d hours, events %d days (set by %s)", rt.SubDays, rt.MainHours, rt.EventDays, userOf(r).Name)
		writeJSON(w, s.retentionOut())
		return
	}
	rt := in.Retention
	if err := s.ev.settings.SetRetention(rt); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	log.Printf("Recordings are kept: SD %d days, HD %d hours, events %d days (set by %s)", rt.SubDays, rt.MainHours, rt.EventDays, userOf(r).Name)
	writeJSON(w, s.retentionOut())
}

func (s *Server) putStorage(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Ingress {
		http.NotFound(w, r)
		return
	}
	var in struct{ Path string }
	if !readJSON(w, r, &in) {
		return
	}
	var chosen *storagePlace
	for _, st := range s.storagePlaces() {
		if st.Path == in.Path {
			chosen = &st
			break
		}
	}
	if chosen == nil || !chosen.Mounted {
		jsonErr(w, 400, "that location does not exist or is not available")
		return
	}
	if chosen.Path == s.cfg.Storage {
		writeJSON(w, map[string]any{"ok": true, "restart": false})
		return
	}
	v := storageChoice{Path: chosen.Path}
	for _, p := range append(append([]string{}, s.cfg.PreviousStorage...), s.cfg.Storage) {
		if p != chosen.Path && !contains(v.Previous, p) {
			v.Previous = append(v.Previous, p)
		}
	}
	if chosen.Type == "network" {
		v.Mount = filepath.Dir(chosen.Path)
	}
	if err := os.MkdirAll(v.Path, 0o755); err != nil {
		jsonErr(w, 500, "cannot write there: "+err.Error())
		return
	}
	b, _ := json.Marshal(v)
	if err := os.WriteFile(storageChoiceFile(s.cfg), b, 0o644); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	log.Printf("Storage changed to %s (%s) by %s – ArgusSen restarts", v.Path, chosen.Name, userOf(r).Name)
	writeJSON(w, map[string]any{"ok": true, "restart": true})
	go func() {
		time.Sleep(time.Second)
		restartSelf()
	}()
}

// restartSelf asks the Supervisor to restart the app (requires hassio_api). If that fails, the program exits.
func restartSelf() {
	req, _ := http.NewRequest("POST", "http://supervisor/addons/self/restart", nil)
	req.Header.Set("Authorization", "Bearer "+os.Getenv("SUPERVISOR_TOKEN"))
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode == 200 {
			return
		}
		log.Printf("restart: Supervisor answered %d", resp.StatusCode)
	} else {
		log.Printf("restart: %v", err)
	}
	os.Exit(1)
}

// watchStorage keeps an eye on the NAS (network drive). With a local fallback (fallback_storage) recording continues locally
// while the NAS is gone, and the local files are moved over afterwards; without a fallback recording is paused.
// It only switches after 2 equal measurements in a row (40 s), so a single slow answer does not restart the cameras.
func (s *Server) watchStorage() {
	if s.cfg.StorageMount == "" {
		return
	}
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	var bad, good int
	for {
		if s.mainOK() {
			good, bad = good+1, 0
		} else {
			bad, good = bad+1, 0
		}
		if store.local != "" {
			switch {
			case bad >= 2 && !store.useLocal.Load():
				log.Printf("ERROR: the NAS (%s) is not responding – recording locally in %s until it is back", s.cfg.Storage, store.local)
				s.switchStorage(true)
			case good >= 2 && store.useLocal.Load():
				log.Printf("The NAS (%s) is back – recording to the NAS again and moving the local recordings over", s.cfg.Storage)
				s.switchStorage(false)
				go moveLocal()
			case good >= 1 && !store.useLocal.Load():
				go moveLocal() // leftovers from an earlier outage (e.g. if ArgusSen was restarted meanwhile)
			}
		} else {
			paused := s.storagePaused.Load()
			switch {
			case bad >= 2 && !paused:
				s.storagePaused.Store(true)
				log.Printf("ERROR: storage %s is not available (network drive not responding) – recording paused", s.cfg.Storage)
				for _, id := range s.camIDs() {
					s.stopCam(id)
				}
			case good >= 1 && paused:
				os.MkdirAll(s.cfg.Storage, 0o755)
				s.storagePaused.Store(false)
				log.Printf("Storage %s is available again – recording resumed", s.cfg.Storage)
				for _, c := range cams.All() {
					s.startCam(c)
				}
			}
		}
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
		}
	}
}

// PUT /api/admin/storage/limit {max_gb}: the most space ArgusSen may take (0 = no limit).
func (s *Server) putStorageLimit(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MaxGB float64 `json:"max_gb"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if err := s.ev.settings.SetMaxGB(math.Round(in.MaxGB)); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if v := s.ev.settings.MaxGB(); v > 0 {
		log.Printf("Storage limit for ArgusSen: %.0f GB (set by %s)", v, userOf(r).Name)
	} else {
		log.Printf("Storage limit for ArgusSen: none (set by %s)", userOf(r).Name)
	}
	writeJSON(w, s.retentionOut())
}

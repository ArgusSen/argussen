package main

// Storage with a local fallback: recordings normally live on the NAS (cfg.Storage). If the NAS stops responding
// (not mounted, or no answer within 5 s), recording continues locally instead (cfg.FallbackStorage), and when the NAS
// is back, the local files are moved over. Playback, timeline and thumbnails read from both places.
// If there is no room locally, the oldest HD files (main) are deleted first and then the oldest SD files (sub), so
// at least one day of SD can be kept (depends on the disk size). Without "fallback_storage" = pause as before.

import (
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type Store struct {
	main, local string
	previous    []string // earlier storage places (after a change of storage): still played back and cleaned up
	useLocal    atomic.Bool
	moveMu      sync.Mutex
	minFreeGB   float64
}

var store = &Store{}

func initStore(cfg *Config) {
	store.main, store.local = cfg.Storage, cfg.FallbackStorage
	store.previous = cfg.PreviousStorage
	store.minFreeGB = cfg.FallbackMinFreeGB
	if store.minFreeGB <= 0 {
		store.minFreeGB = 3
	}
	if store.local != "" {
		os.MkdirAll(store.local, 0o755)
	}
}

// Active: where recording happens right now.
func (st *Store) Active() string {
	if st.useLocal.Load() {
		return st.local
	}
	return st.main
}

// Roots: all places where recordings may be (the active one first, earlier storage places last).
func (st *Store) Roots() []string {
	var out []string
	switch {
	case st.local == "":
		out = []string{st.main}
	case st.useLocal.Load():
		out = []string{st.local, st.main}
	default:
		out = []string{st.main, st.local}
	}
	for _, p := range st.previous {
		if fi, err := os.Stat(filepath.Join(p, "rec")); err == nil && fi.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

// Find: the full path of a file (relative to the storage root), where it exists.
func (st *Store) Find(rel string) string {
	roots := st.Roots()
	for _, r := range roots {
		p := filepath.Join(r, rel)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return filepath.Join(roots[0], rel)
}

// Remove removes a file everywhere.
func (st *Store) Remove(rel string) {
	for _, r := range st.Roots() {
		os.Remove(filepath.Join(r, rel))
	}
}

// statTimeout: does the path answer within the time? (a hanging NAS must not lock ArgusSen)
func statTimeout(p string, d time.Duration) bool {
	ch := make(chan bool, 1)
	go func() { _, err := os.Stat(p); ch <- err == nil }()
	select {
	case ok := <-ch:
		return ok
	case <-time.After(d):
		return false
	}
}

// mainOK: is the NAS mounted and does it answer?
// ARGUS_TEST_NAS_DOWN=<file>: for testing only – if the file exists, act as if the NAS is gone.
func (s *Server) mainOK() bool {
	if p := os.Getenv("ARGUS_TEST_NAS_DOWN"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return false
		}
	}
	return storageMounted(s.cfg.StorageMount) && statTimeout(s.cfg.Storage, 5*time.Second)
}

// switchStorage switches between NAS and local storage; the cameras restart so ffmpeg writes to the new place.
func (s *Server) switchStorage(local bool) {
	store.useLocal.Store(local)
	for _, id := range s.camIDs() {
		s.stopCam(id)
	}
	os.MkdirAll(store.Active(), 0o755)
	for _, c := range cams.All() {
		s.startCam(c)
	}
}

// moveLocal moves local recordings and thumbnails to the NAS (after an outage). Stops on errors.
func moveLocal() {
	if store.local == "" || store.useLocal.Load() || !store.moveMu.TryLock() {
		return
	}
	defer store.moveMu.Unlock()
	var n int
	var bytes int64
	var moveErr error
	filepath.WalkDir(store.local, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || moveErr != nil {
			return nil
		}
		if store.useLocal.Load() { // the NAS disappeared again
			moveErr = os.ErrClosed
			return filepath.SkipAll
		}
		rel, _ := filepath.Rel(store.local, p)
		dst := filepath.Join(store.main, rel)
		if fi, err := os.Stat(dst); err == nil && fi.Size() >= info(d).Size() {
			os.Remove(p)
			return nil
		}
		if err := copyFile(p, dst); err != nil {
			moveErr = err
			return filepath.SkipAll
		}
		bytes += info(d).Size()
		n++
		os.Remove(p)
		return nil
	})
	removeEmptyDirs(store.local)
	if n > 0 || moveErr != nil {
		msg := ""
		if moveErr != nil {
			msg = " – stopped: " + moveErr.Error() + " (will retry later)"
		}
		log.Printf("Local storage: %d files (%.1f GB) moved to the NAS%s", n, float64(bytes)/1e9, msg)
	}
}

func info(d fs.DirEntry) fs.FileInfo {
	fi, err := d.Info()
	if err != nil {
		return zeroInfo{}
	}
	return fi
}

type zeroInfo struct{ fs.FileInfo }

func (zeroInfo) Size() int64 { return 0 }

func copyFile(src, dst string) error {
	os.MkdirAll(filepath.Dir(dst), 0o755)
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".moving"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func removeEmptyDirs(root string) {
	var dirs []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() && p != root {
			dirs = append(dirs, p)
		}
		return nil
	})
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) }) // deepest first
	for _, d := range dirs {
		os.Remove(d) // only if empty
	}
}

func freeGB(p string) float64 {
	var st syscall.Statfs_t
	if syscall.Statfs(p, &st) != nil {
		return -1
	}
	return float64(st.Bavail) * float64(st.Bsize) / 1e9
}

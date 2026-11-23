package main

// The disk guard keeps free space on every storage place ArgusSen records to, so a full disk never stops Home
// Assistant (its database, updates and backups live on the same disk when recordings are stored there).
// Every 30 seconds: if a place has less free space than its limit, the oldest recordings are deleted until there is
// a little more than the limit, in this order:
//  1. full resolution (HD) outside events
//  2. low resolution (SD) outside events
//  3. HD and SD around events that are not protected
//
// Recordings around protected events and the newest 10 minutes are never deleted. If there is still too little
// space, the status page and Home Assistant show a warning.
//
// The same happens when ArgusSen's recordings pass the limit chosen in Settings → Storage ("max_gb"). How much they
// take is measured in the background every 10 minutes (going through all files is slow on a network drive).

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// usage: the space ArgusSen's recordings and thumbnails take on the main storage (bytes; -1 = not measured yet).
var usage = func() *atomic.Int64 { v := &atomic.Int64{}; v.Store(-1); return v }()

// measureUsage measures usage at the start and every 10 minutes (not while the network drive is away).
func (s *Server) measureUsage(ctx context.Context) {
	for {
		if !store.useLocal.Load() && !s.storagePaused.Load() {
			if b := usedBytes(store.main, 10*time.Minute); b >= 0 {
				usage.Store(b)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Minute):
		}
	}
}

// minFreeFor: the free space (GB) to keep on a storage place. Home Assistant's own disk: 10 % (at least 10 GB),
// because Home Assistant needs room for its database, updates and backups. A network drive: 1 % (at least 5 GB).
// "min_free_gb" in the configuration overrides it for the main storage.
func minFreeFor(cfg *Config, root string) float64 {
	if root == store.local {
		return store.minFreeGB
	}
	if cfg.MinFreeGB > 0 && root == store.main {
		return cfg.MinFreeGB
	}
	total := totalGB(root)
	if isNetwork(root) || (root == store.main && cfg.StorageMount != "") {
		return max(5, total*0.01)
	}
	return max(10, total*0.10)
}

// isNetwork: whether a folder is on a network drive (cifs/nfs).
func isNetwork(p string) bool {
	for mp := range netMounts() {
		if p == mp || strings.HasPrefix(p, mp+"/") {
			return true
		}
	}
	return false
}

func totalGB(p string) float64 {
	var st syscall.Statfs_t
	if syscall.Statfs(p, &st) != nil {
		return 0
	}
	return float64(st.Blocks) * float64(st.Bsize) / 1e9
}

// recording: one recording file (rec/<camera>/<stream>/<day>/<HHMMSS>.ts).
type recording struct {
	p      string
	cam    string
	t      time.Time
	size   int64
	stream string
}

func recordings(root, stream string) []recording {
	var out []recording
	cams, _ := os.ReadDir(filepath.Join(root, "rec"))
	for _, c := range cams {
		sdir := filepath.Join(root, "rec", c.Name(), stream)
		days, _ := os.ReadDir(sdir)
		for _, d := range days {
			files, _ := os.ReadDir(filepath.Join(sdir, d.Name()))
			for _, f := range files {
				t, err := time.Parse("20060102150405", d.Name()+strings.TrimSuffix(f.Name(), ".ts"))
				if err != nil {
					continue
				}
				out = append(out, recording{filepath.Join(sdir, d.Name(), f.Name()), c.Name(), t, info(f).Size(), stream})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].t.Before(out[j].t) })
	return out
}

// makeRoomIn deletes the oldest recordings in root until minFree GB (+1) is free. It returns whether there is
// still too little space afterwards, and how many files were deleted.
func makeRoomIn(root string, minFree float64, ev *EventManager) (bool, int) {
	free := freeGB(root)
	if free < 0 || free >= minFree {
		return false, 0
	}
	need := int64(min(minFree+1-free, 1e6) * 1e9) // a little extra, so it does not happen every 30 seconds
	freed, hd, sd := deleteOldest(root, need, ev)
	if hd+sd > 0 {
		log.Printf("Storage %s: low on space (%.1f GB free) – deleted the oldest %d HD and %d SD files (%.1f GB)",
			root, free, hd, sd, float64(freed)/1e9)
	}
	if root == store.main && freed > 0 && usage.Load() >= 0 {
		usage.Add(-freed)
	}
	return freeGB(root) < minFree, hd + sd
}

// keepWithin deletes the oldest recordings on the main storage when ArgusSen's recordings take more than maxGB
// (down to 98 % of it). It returns whether they still take too much, and how many files were deleted.
func keepWithin(root string, maxGB float64, ev *EventManager) (bool, int) {
	used := usage.Load()
	limit := int64(maxGB * 1e9)
	if maxGB <= 0 || used < 0 || used <= limit {
		return false, 0
	}
	freed, hd, sd := deleteOldest(root, used-int64(maxGB*0.98*1e9), ev)
	if hd+sd > 0 {
		log.Printf("Storage %s: ArgusSen's limit of %.0f GB is reached – deleted the oldest %d HD and %d SD files (%.1f GB)",
			root, maxGB, hd, sd, float64(freed)/1e9)
	}
	usage.Add(-freed)
	return usage.Load() > limit, hd + sd
}

// deleteOldest deletes at least need bytes of the oldest recordings in root, in the order described at the top.
// It returns the bytes freed and the number of HD and SD files deleted.
func deleteOldest(root string, need int64, ev *EventManager) (int64, int, int) {
	events := ev.keepRanges(time.Time{}) // all events (incl. protected)
	prot := ev.protRanges()
	cut := time.Now().UTC().Add(-10 * time.Minute)
	main, sub := recordings(root, "main"), recordings(root, "sub")
	var freed int64
	n := map[string]int{}
	pass := func(files []recording, skip map[string][][2]time.Time) {
		for _, f := range files {
			if freed >= need {
				return
			}
			if !f.t.Before(cut) || overlaps(prot[f.cam], f.t, f.t.Add(90*time.Second)) ||
				(skip != nil && overlaps(skip[f.cam], f.t, f.t.Add(90*time.Second))) {
				continue
			}
			if os.Remove(f.p) == nil {
				freed += f.size
				n[f.stream]++
			}
		}
	}
	pass(main, events) // 1. HD outside events
	pass(sub, events)  // 2. SD outside events
	pass(main, nil)    // 3. HD around events that are not protected
	pass(sub, nil)     //    … and SD
	return freed, n["main"], n["sub"]
}

// guardDisks runs the disk guard every 30 seconds.
func (s *Server) guardDisks(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	// a place with too little space where nothing could be deleted (only protected recordings left) is checked again
	// after 10 minutes – going through all files every 30 seconds would load a NAS hard
	stillLow := map[string]time.Time{}
	for {
		low := false
		for _, root := range store.Roots() {
			if root == store.main && (store.useLocal.Load() || s.storagePaused.Load()) {
				continue // the network drive is not responding – do not touch it
			}
			if until, ok := stillLow[root]; ok && time.Now().Before(until) {
				low = true
				continue
			}
			l, deleted := makeRoomIn(root, minFreeFor(s.cfg, root), s.ev)
			if root == store.main && !l {
				l, deleted = keepWithin(root, s.ev.settings.MaxGB(), s.ev)
			}
			if l {
				low = true
			}
			if l && deleted == 0 {
				stillLow[root] = time.Now().Add(10 * time.Minute)
			} else {
				delete(stillLow, root)
			}
		}
		if low && !s.storageLow.Load() {
			log.Printf("ERROR: the storage is almost full and only protected recordings are left – protect fewer events or free space")
		}
		s.storageLow.Store(low)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

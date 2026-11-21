package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Cleanup: sub stream after sub_days, full resolution after main_hours (except around events),
// events and their clips after event_days (Settings.Retention). Only deletes ArgusSen's own folders rec/ and events/.
func runRetention(ctx context.Context, cfg *Config, ev *EventManager) {
	for {
		cleanOnce(cfg, ev)
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Minute):
		}
	}
}

func dayOf(name string) (time.Time, bool) {
	t, err := time.Parse("20060102", name)
	return t, err == nil
}

func cleanOnce(cfg *Config, ev *EventManager) {
	now := time.Now().UTC()
	rt := ev.settings.Retention()
	subCut := now.Add(-time.Duration(rt.SubDays) * 24 * time.Hour)
	mainCut := now.Add(-time.Duration(rt.MainHours) * time.Hour)
	evCut := now.Add(-time.Duration(rt.EventDays) * 24 * time.Hour)
	keep := ev.keepRanges(evCut.Add(-time.Hour))
	prot := ev.protRanges()
	removed := 0
	// deleteFiles: removes minute files in a day folder, except those that overlap rs; an empty folder is removed.
	deleteFiles := func(ddir, dayName string, rs [][2]time.Time, onlyBefore time.Time) {
		files, _ := os.ReadDir(ddir)
		for _, f := range files {
			t, err := time.Parse("20060102150405", dayName+strings.TrimSuffix(f.Name(), ".ts"))
			if err != nil || !t.Before(onlyBefore) || overlaps(rs, t, t.Add(90*time.Second)) {
				continue
			}
			if os.Remove(filepath.Join(ddir, f.Name())) == nil {
				removed++
			}
		}
		if left, _ := os.ReadDir(ddir); len(left) == 0 && day0(dayName).Add(24*time.Hour).Before(now) {
			os.Remove(ddir)
		}
	}

	for _, root := range store.Roots() { // NAS and possibly the local fallback
		if root == store.main && store.useLocal.Load() {
			continue // the NAS is not responding – do not touch it
		}
		rec := filepath.Join(root, "rec")
		cams, _ := os.ReadDir(rec)
		for _, c := range cams {
			for _, stream := range []string{"sub", "main"} {
				sdir := filepath.Join(rec, c.Name(), stream)
				days, _ := os.ReadDir(sdir)
				for _, d := range days {
					day, ok := dayOf(d.Name())
					if !ok {
						continue
					}
					dayEnd := day.Add(24 * time.Hour)
					ddir := filepath.Join(sdir, d.Name())
					// protected events: the day is cleaned file by file instead of the whole folder
					protected := overlaps(prot[c.Name()], day, dayEnd)
					if stream == "sub" {
						if dayEnd.Before(subCut) {
							if protected {
								deleteFiles(ddir, d.Name(), prot[c.Name()], dayEnd)
							} else {
								os.RemoveAll(ddir)
								removed++
							}
						}
						continue
					}
					if dayEnd.Before(evCut) {
						if protected {
							deleteFiles(ddir, d.Name(), prot[c.Name()], dayEnd)
						} else {
							os.RemoveAll(ddir)
							removed++
						}
						continue
					}
					if !day.Before(mainCut) {
						continue
					}
					files, _ := os.ReadDir(ddir)
					for _, f := range files {
						n := strings.TrimSuffix(f.Name(), ".ts")
						t, err := time.Parse("20060102150405", d.Name()+n)
						if err != nil || !t.Before(mainCut) {
							continue
						}
						segEnd := t.Add(90 * time.Second) // segments are about 60 s
						if overlaps(keep[c.Name()], t, segEnd) {
							continue
						}
						if os.Remove(filepath.Join(ddir, f.Name())) == nil {
							removed++
						}
					}
					if left, _ := os.ReadDir(ddir); len(left) == 0 && dayEnd.Before(now) {
						os.Remove(ddir)
					}
				}
			}
		}

		// events and thumbnails
		thumbs := filepath.Join(root, "events")
		days, _ := os.ReadDir(thumbs)
		kt := ev.keepThumbs()
		for _, d := range days {
			if day, ok := dayOf(d.Name()); ok && day.Add(24*time.Hour).Before(evCut) {
				files, _ := os.ReadDir(filepath.Join(thumbs, d.Name()))
				for _, f := range files {
					if !kt[filepath.Join("events", d.Name(), f.Name())] {
						os.Remove(filepath.Join(thumbs, d.Name(), f.Name()))
					}
				}
				os.Remove(filepath.Join(thumbs, d.Name())) // only if empty
			}
		}
	}
	if r, err := ev.db.Exec(`DELETE FROM events WHERE start < ? AND keep=0`, evCut.UnixMilli()); err == nil {
		if n, _ := r.RowsAffected(); n > 0 {
			log.Printf("cleanup: %d old events deleted", n)
		}
	}
	logDB.clean()
	var st syscall.Statfs_t
	free := ""
	if syscall.Statfs(store.Active(), &st) == nil {
		free = fmt.Sprintf(", %.0f GB free on "+map[bool]string{true: "local storage", false: "the NAS"}[store.useLocal.Load()], float64(st.Bavail)*float64(st.Bsize)/1e9)
	}
	if removed > 0 {
		log.Printf("Cleanup: %d old files/folders deleted%s", removed, free)
	} else if now.Minute() < 10 && now.Hour()%6 == 0 {
		log.Printf("Cleanup: nothing to do%s", free)
	}
}

func day0(dayName string) time.Time { t, _ := time.Parse("20060102", dayName); return t }

func overlaps(rs [][2]time.Time, a, b time.Time) bool {
	for _, r := range rs {
		if a.Before(r[1]) && b.After(r[0]) {
			return true
		}
	}
	return false
}

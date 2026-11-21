package main

import (
	"crypto/subtle"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata"
)

func subtleEq(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

var startTime = time.Now()

var cpuMu sync.Mutex
var cpuPrev [2]uint64 // busy, total
var cpuPct float64

// cpuSample reads /proc/stat (LXCFS shows the container's own numbers) and updates the usage.
func cpuSample() {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return
	}
	line := strings.SplitN(string(b), "\n", 2)[0]
	f := strings.Fields(line)
	if len(f) < 8 || f[0] != "cpu" {
		return
	}
	var total, idle uint64
	for i, v := range f[1:] {
		n, _ := strconv.ParseUint(v, 10, 64)
		total += n
		if i == 3 || i == 4 { // idle + iowait
			idle += n
		}
	}
	cpuMu.Lock()
	if cpuPrev[1] > 0 && total > cpuPrev[1] {
		busy := (total - idle) - cpuPrev[0]
		cpuPct = 100 * float64(busy) / float64(total-cpuPrev[1])
	}
	cpuPrev = [2]uint64{total - idle, total}
	cpuMu.Unlock()
}

func init() {
	go func() {
		for {
			cpuSample()
			time.Sleep(5 * time.Second)
		}
	}()
}

func memPct() float64 {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	vals := map[string]float64{}
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 {
			n, _ := strconv.ParseFloat(f[1], 64)
			vals[strings.TrimSuffix(f[0], ":")] = n
		}
	}
	if vals["MemTotal"] == 0 {
		return 0
	}
	return 100 * (vals["MemTotal"] - vals["MemAvailable"]) / vals["MemTotal"]
}

func (s *Server) sysStats() map[string]any {
	var st syscall.Statfs_t
	nas := map[string]any{}
	if syscall.Statfs(store.Active(), &st) == nil {
		total := float64(st.Blocks) * float64(st.Bsize)
		free := float64(st.Bavail) * float64(st.Bsize)
		nas = map[string]any{"total_gb": total / 1e9, "free_gb": free / 1e9, "used_pct": 100 * (total - free) / total}
	}
	var today int
	n := time.Now().In(localTZ)
	midnight := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, localTZ)
	s.ev.db.QueryRow(`SELECT COUNT(*) FROM events WHERE start >= ?`, midnight.UnixMilli()).Scan(&today)
	cpuMu.Lock()
	cpu := cpuPct
	cpuMu.Unlock()
	return map[string]any{"cpu_pct": cpu, "mem_pct": memPct(), "nas": nas, "storage_ok": !s.storagePaused.Load(), "storage_low": s.storageLow.Load(), "ai_note": aiNote(s.cfg), "storage_local": store.useLocal.Load(),
		"uptime_s": int64(time.Since(startTime).Seconds()), "events_today": today}
}

// aiNote: "experimental" when the built-in AI runs on ARM (e.g. Raspberry Pi) – built, but not tested there yet.
func aiNote(cfg *Config) string {
	if runtime.GOARCH == "arm64" && strings.Contains(cfg.BlueOnyx, "127.0.0.1") {
		return "experimental"
	}
	return ""
}

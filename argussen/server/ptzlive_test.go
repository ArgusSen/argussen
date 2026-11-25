package main

import (
	"bufio"
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Only run by hand against a real camera: ARGUS_PTZ_TEST=<camera> ARGUS_PTZ_HOST=<ip> ARGUS_PTZ_LOGINS=<file> go test -run TestPTZLive
func TestPTZLive(t *testing.T) {
	name := os.Getenv("ARGUS_PTZ_TEST")
	if name == "" {
		t.Skip("no camera")
	}
	c := &Camera{ID: name, Host: os.Getenv("ARGUS_PTZ_HOST")}
	f, err := os.Open(os.Getenv("ARGUS_PTZ_LOGINS"))
	if err != nil {
		t.Fatal("no login file")
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if p := strings.SplitN(strings.TrimSpace(sc.Text()), ":", 3); len(p) == 3 && p[0] == name {
			c.user, c.pass = p[1], p[2]
		}
	}
	p := ptzFor(c)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	t0 := time.Now()
	p.probe(ctx, c)
	t.Logf("probe %v: ok=%v zoom=%v err=%q profile=%q addr=%s", time.Since(t0).Round(time.Millisecond), p.ok, p.zoom, p.err, p.profile, p.addr)
	if !p.ok {
		t.Fatal("no PTZ")
	}
	ps, err2 := p.presets(ctx)
	t.Logf("presets: %v %v", ps, err2)
	for _, d := range [][2]float64{{0.3, 0}, {-0.3, 0}} {
		t0 = time.Now()
		if err := p.move(ctx, d[0], d[1], 0); err != nil {
			t.Fatal(err)
		}
		time.Sleep(600 * time.Millisecond)
		if err := p.stop(ctx); err != nil {
			t.Fatal(err)
		}
		t.Logf("move %v + stop: %v", d, time.Since(t0).Round(time.Millisecond))
		time.Sleep(400 * time.Millisecond)
	}
	// the watchdog: a move without stop must stop by itself after about a second
	p.mu.Lock()
	err = p.move(ctx, 0.3, 0, 0)
	p.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	t.Logf("after watchdog: moving=%v", p.moving)
	p.mu.Lock()
	p.move(ctx, -0.3, 0, 0)
	p.mu.Unlock()
	time.Sleep(1500 * time.Millisecond)
	t.Logf("back, moving=%v", p.moving)
	if err := p.savePreset(ctx, "ArgusSen test"); err != nil {
		t.Fatal("save:", err)
	}
	ps, _ = p.presets(ctx)
	t.Logf("after save: %v", ps)
	var tok string
	for _, x := range ps {
		if x.Name == "ArgusSen test" {
			tok = x.Token
		}
	}
	p.mu.Lock()
	p.move(ctx, 0.3, 0, 0)
	p.mu.Unlock()
	time.Sleep(700 * time.Millisecond)
	p.stop(ctx)
	if err := p.gotoPreset(ctx, tok); err != nil {
		t.Fatal("goto:", err)
	}
	time.Sleep(2 * time.Second)
	if err := p.removePreset(ctx, tok); err != nil {
		t.Fatal("remove:", err)
	}
	ps, _ = p.presets(ctx)
	t.Logf("after remove: %v", ps)
}

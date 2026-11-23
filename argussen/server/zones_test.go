package main

import (
	"testing"
	"time"
)

var square = [][2]float64{{0.2, 0.2}, {0.6, 0.2}, {0.6, 0.6}, {0.2, 0.6}}

func TestPointIn(t *testing.T) {
	tri := [][2]float64{{0, 0}, {1, 0}, {0, 1}}
	for _, c := range []struct {
		poly [][2]float64
		x, y float64
		in   bool
	}{{square, 0.4, 0.4, true}, {square, 0.1, 0.4, false}, {square, 0.7, 0.7, false}, {tri, 0.2, 0.2, true}, {tri, 0.8, 0.8, false}} {
		if pointIn(c.poly, c.x, c.y) != c.in {
			t.Errorf("%v (%.1f,%.1f): want %v", c.poly, c.x, c.y, c.in)
		}
	}
}

func TestCleanZones(t *testing.T) {
	valid := map[string]bool{"person": true, "car": true, "motion": true}
	z, err := cleanZones([]Zone{
		{ID: "z2", Name: " Driveway ", Kind: "zone", Points: square, Objects: []string{"car"}},
		{Name: "Path", Kind: "zone", Points: square},
		{Kind: "ignore", Points: square, Objects: []string{"car"}},
	}, valid)
	if err != nil {
		t.Fatal(err)
	}
	if z[0].ID != "z2" || z[0].Name != "Driveway" || z[1].ID != "z1" || z[2].ID != "i1" || len(z[2].Objects) != 0 {
		t.Errorf("ids/names: %+v", z)
	}
	bad := [][]Zone{
		{{Kind: "zone", Points: square}},                                          // no name
		{{Name: "A", Kind: "zone", Points: square[:2]}},                           // too few points
		{{Name: "A", Kind: "zone", Points: [][2]float64{{0, 0}, {2, 0}, {0, 1}}}}, // outside the picture
		{{Name: "A", Kind: "zone", Points: square, Objects: []string{"cow"}}},
		{{Name: "A", Kind: "other", Points: square}},
		{{Name: "A", Kind: "zone", Points: square}, {Name: "B", Kind: "zone", Points: square}, {Name: "C", Kind: "zone", Points: square}, {Name: "D", Kind: "zone", Points: square}},
	}
	for i, b := range bad {
		if _, err := cleanZones(b, valid); err == nil {
			t.Errorf("bad zones %d accepted", i)
		}
	}
}

func TestZoneMasksAndHits(t *testing.T) {
	zones := []Zone{
		{ID: "z1", Name: "Driveway", Kind: "zone", Points: square, Objects: []string{"car"}},
		{ID: "z2", Name: "All", Kind: "zone", Points: [][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}}},
		{ID: "i1", Kind: "ignore", Points: [][2]float64{{0.8, 0.8}, {1, 0.8}, {1, 1}, {0.8, 1}}},
	}
	m := buildMasks(zones, 10, 10)
	if !m.ignore[9*10+9] || m.ignore[0] {
		t.Error("ignore mask")
	}
	moved := make([]bool, 100)
	moved[4*10+4] = true // inside the square
	got := m.motionZones(moved)
	if len(got) != 1 || got[0] != "z2" { // the driveway only counts cars, not motion
		t.Errorf("motion zones: %v", got)
	}
	if ign, in := hitZones(zones, 0.4, 0.5, "car"); ign || len(in) != 2 {
		t.Errorf("car in the driveway: %v %v", ign, in)
	}
	if _, in := hitZones(zones, 0.4, 0.5, "person"); len(in) != 1 || in[0] != "z2" {
		t.Errorf("person: %v", in)
	}
	if ign, _ := hitZones(zones, 0.9, 0.9, "person"); !ign {
		t.Error("a hit in the area to ignore was not dropped")
	}
}

// An area to ignore is left out of the motion detection: motion only there is no motion.
func TestMotionIgnore(t *testing.T) {
	w, h := 80, 80 // 10 × 10 blocks
	cfg := MotionConfig{Threshold: 10, MinArea: 0.005, MaxArea: 0.9, HoldSec: 1}
	run := func(ignore []bool) bool {
		md := newMotionDetector(w, h, cfg, nil)
		md.ignore = func() []bool { return ignore }
		frame := make([]byte, w*h)
		for i := 0; i < 4; i++ {
			md.Feed(frame)
		}
		lit := make([]byte, w*h)
		for y := 64; y < 80; y++ { // the bottom right corner changes
			for x := 64; x < 80; x++ {
				lit[y*w+x] = 200
			}
		}
		moving := false
		for i := 0; i < 3; i++ {
			moving = md.Feed(lit)
		}
		return moving
	}
	if !run(nil) {
		t.Fatal("no motion without areas to ignore")
	}
	ign := buildMasks([]Zone{{ID: "i1", Kind: "ignore", Points: [][2]float64{{0.75, 0.75}, {1, 0.75}, {1, 1}, {0.75, 1}}}}, 10, 10).ignore
	if run(ign) {
		t.Error("motion in an area to ignore")
	}
}

// Zones are saved with the camera's settings, and a motion event gets the zones the motion was in.
func TestZonesSettingsAndMotionEvent(t *testing.T) {
	s := newTestServer(t)
	cs := s.ev.settings.Get("a")
	cs.Objects = []string{"motion"}
	cs.Zones = []Zone{{Name: "Path", Kind: "zone", Points: square}}
	if err := s.ev.settings.Set("a", cs); err != nil {
		t.Fatal(err)
	}
	if z := s.ev.settings.Get("a").Zones; len(z) != 1 || z[0].ID != "z1" {
		t.Fatalf("saved zones: %+v", z)
	}
	if s.ev.settings.masks("a", 10, 10) == nil || s.ev.settings.masks("b", 10, 10) != nil {
		t.Error("masks")
	}
	cam := &Camera{ID: "a", Name: "A"}
	now := time.Now()
	for i := 0; i < 15; i++ { // 3 seconds of motion in the zone
		s.ev.Feed(cam, &Frame{W: 8, H: 8, YUV: make([]byte, 96), At: now.Add(time.Duration(i) * 200 * time.Millisecond)}, true, []string{"z1"})
	}
	evs, _ := s.ev.ListEvents([]string{"a"}, time.Now().Add(time.Hour), 10)
	if len(evs) != 1 || len(evs[0].Zones) != 1 || evs[0].Zones[0] != "z1" {
		t.Errorf("event zones: %+v", evs)
	}
	// new zones replace the masks at once
	cs.Zones = nil
	s.ev.settings.Set("a", cs)
	if s.ev.settings.masks("a", 10, 10) != nil {
		t.Error("old masks after the zones were removed")
	}
}

package main

// Zones (Settings → camera → Zones): up to 3 named areas per camera, and up to 3 areas to ignore. They are drawn as
// polygons in fractions of the picture (0–1), so they fit whatever the resolution, in the camera's own orientation
// (the app turns them when the picture is shown rotated).
//
// An event gets the zones it has been in: an AI hit counts in a zone when its foot point (the middle of the box's
// bottom edge – feet and wheels) is inside; motion counts where the moving 8×8 blocks are. Areas to ignore remove
// both: hits there are dropped, and the blocks are left out of the motion detection (fewer false triggers, fewer AI
// calls). Without zones nothing changes.
//
// CPU: the polygons are turned into masks of the motion detector's blocks once (cached until the zones change); per
// frame it is a lookup per block. AI hits cost one point-in-polygon test each (about once a second, while there is motion).

import (
	"fmt"
	"strconv"
	"strings"
)

type Zone struct {
	ID      string       `json:"id"`                // z1–z3 (zones) or i1–i3 (areas to ignore) – stable, used by Home Assistant
	Name    string       `json:"name"`              // e.g. "Driveway"
	Kind    string       `json:"kind"`              // "zone" or "ignore"
	Points  [][2]float64 `json:"points"`            // 3–8 corners, x and y as fractions of the picture
	Objects []string     `json:"objects,omitempty"` // zones: the objects that count there (empty = all)
}

const maxZones, maxIgnore, maxZonePoints = 3, 3, 8

// cleanZones checks the zones from the app and gives every zone a stable id (kept if it is valid and free).
func cleanZones(in []Zone, valid map[string]bool) ([]Zone, error) {
	used := map[string]bool{}
	count := map[string]int{}
	for _, z := range in {
		count[z.Kind]++
	}
	if count["zone"] > maxZones || count["ignore"] > maxIgnore {
		return nil, fmt.Errorf("at most %d zones and %d areas to ignore per camera", maxZones, maxIgnore)
	}
	out := []Zone{}
	for _, z := range in {
		prefix := map[string]string{"zone": "z", "ignore": "i"}[z.Kind]
		if prefix == "" {
			return nil, fmt.Errorf("unknown kind of zone %q", z.Kind)
		}
		z.Name = strings.TrimSpace(z.Name)
		if z.Kind == "zone" && z.Name == "" {
			return nil, fmt.Errorf("give every zone a name")
		}
		if len([]rune(z.Name)) > 30 {
			return nil, fmt.Errorf("a zone name can be at most 30 characters")
		}
		if len(z.Points) < 3 || len(z.Points) > maxZonePoints {
			return nil, fmt.Errorf("a zone needs 3–%d points", maxZonePoints)
		}
		for _, p := range z.Points {
			if p[0] < 0 || p[0] > 1 || p[1] < 0 || p[1] > 1 {
				return nil, fmt.Errorf("a zone must be inside the picture")
			}
		}
		if z.Kind == "ignore" {
			z.Objects = nil
		}
		objs := []string{}
		for _, o := range z.Objects {
			if !valid[o] {
				return nil, fmt.Errorf("unknown object %q", o)
			}
			objs = append(objs, o)
		}
		z.Objects = objs
		max := map[string]int{"z": maxZones, "i": maxIgnore}[prefix]
		if n, err := strconv.Atoi(strings.TrimPrefix(z.ID, prefix)); err != nil || !strings.HasPrefix(z.ID, prefix) || n < 1 || n > max || used[z.ID] {
			z.ID = ""
		}
		if z.ID != "" {
			used[z.ID] = true
		}
		out = append(out, z)
	}
	for i := range out { // new zones get the first free id
		if out[i].ID != "" {
			continue
		}
		prefix := map[string]string{"zone": "z", "ignore": "i"}[out[i].Kind]
		for n := 1; ; n++ {
			if id := prefix + strconv.Itoa(n); !used[id] {
				out[i].ID, used[id] = id, true
				break
			}
		}
	}
	return out, nil
}

// pointIn: whether (x, y) is inside the polygon (ray casting).
func pointIn(poly [][2]float64, x, y float64) bool {
	in := false
	for i, j := 0, len(poly)-1; i < len(poly); j, i = i, i+1 {
		xi, yi, xj, yj := poly[i][0], poly[i][1], poly[j][0], poly[j][1]
		if (yi > y) != (yj > y) && x < (xj-xi)*(y-yi)/(yj-yi)+xi {
			in = !in
		}
	}
	return in
}

// camMasks: the zones as masks of the motion detector's blocks (bw × bh).
type camMasks struct {
	ignore []bool            // nil = nothing ignored
	zones  map[string][]bool // zone id → its blocks
	objs   map[string][]string
}

func buildMasks(zones []Zone, bw, bh int) *camMasks {
	m := &camMasks{zones: map[string][]bool{}, objs: map[string][]string{}}
	for _, z := range zones {
		mask := make([]bool, bw*bh)
		for by := 0; by < bh; by++ {
			for bx := 0; bx < bw; bx++ {
				if pointIn(z.Points, (float64(bx)+0.5)/float64(bw), (float64(by)+0.5)/float64(bh)) {
					mask[by*bw+bx] = true
				}
			}
		}
		if z.Kind == "ignore" {
			if m.ignore == nil {
				m.ignore = make([]bool, bw*bh)
			}
			for i, v := range mask {
				m.ignore[i] = m.ignore[i] || v
			}
		} else {
			m.zones[z.ID], m.objs[z.ID] = mask, z.Objects
		}
	}
	return m
}

// motionZones: the zones (that count motion) with moving blocks.
func (m *camMasks) motionZones(moved []bool) []string {
	var out []string
	for id, mask := range m.zones {
		if !countsIn(m.objs[id], "motion") {
			continue
		}
		for i, v := range moved {
			if v && mask[i] {
				out = append(out, id)
				break
			}
		}
	}
	return out
}

func countsIn(objects []string, label string) bool {
	return len(objects) == 0 || contains(objects, label)
}

// hitZones: for an AI hit with its foot point at (x, y) – whether it is in an area to ignore, and the zones it is in.
func hitZones(zones []Zone, x, y float64, label string) (ignored bool, in []string) {
	for _, z := range zones {
		if !pointIn(z.Points, x, y) {
			continue
		}
		if z.Kind == "ignore" {
			return true, nil
		}
		if countsIn(z.Objects, label) {
			in = append(in, z.ID)
		}
	}
	return false, in
}

// addZones adds zone ids to a list; it returns whether the list grew.
func addZones(list *[]string, ids []string) bool {
	grew := false
	for _, id := range ids {
		if !contains(*list, id) {
			*list = append(*list, id)
			grew = true
		}
	}
	return grew
}

func zonesOrEmpty(z []Zone) []Zone {
	if z == nil {
		return []Zone{}
	}
	return z
}

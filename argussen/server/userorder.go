package main

// Each user's own order of the cameras (the overview, live and the timeline). The administrators' order
// (Settings → Cameras → Change order) is the default for everybody who has not made their own, and for new users.
// A user's order is kept in the table kv as "order:<user id>" (a JSON list) and remembered in memory, because
// /api/cams is asked every few seconds. Cameras missing from a user's own list (e.g. new ones) come last, in the
// default order. Home Assistant and MQTT always use the default.

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"sync"
)

type orderCache struct {
	mu sync.Mutex
	m  map[int64][]string // nil list = no own order (asked once)
}

func orderKey(id int64) string { return fmt.Sprintf("order:%d", id) }

// userOrder: the user's own order, or nil.
func (s *Server) userOrder(id int64) []string {
	if id <= 0 {
		return nil
	}
	s.orders.mu.Lock()
	defer s.orders.mu.Unlock()
	if s.orders.m == nil {
		s.orders.m = map[int64][]string{}
	}
	if l, ok := s.orders.m[id]; ok {
		return l
	}
	var l []string
	if v := s.ev.settings.GetKV(orderKey(id)); v != "" {
		json.Unmarshal([]byte(v), &l)
	}
	s.orders.m[id] = l
	return l
}

func (s *Server) setUserOrder(id int64, l []string) {
	s.orders.mu.Lock()
	defer s.orders.mu.Unlock()
	if s.orders.m == nil {
		s.orders.m = map[int64][]string{}
	}
	if len(l) == 0 {
		s.ev.settings.DelKV(orderKey(id))
		s.orders.m[id] = nil
		return
	}
	b, _ := json.Marshal(l)
	s.ev.settings.SetKV(orderKey(id), string(b))
	s.orders.m[id] = l
}

// orderedFor: the cameras (already in the default order) in the user's own order.
func (s *Server) orderedFor(u *User, ids []string) []string {
	own := s.userOrder(u.ID)
	if len(own) == 0 {
		return ids
	}
	pos := make(map[string]int, len(own))
	for i, id := range own {
		pos[id] = i
	}
	out := append([]string(nil), ids...)
	sort.SliceStable(out, func(a, b int) bool {
		pa, oka := pos[out[a]]
		pb, okb := pos[out[b]]
		switch {
		case oka && okb:
			return pa < pb
		default:
			return oka && !okb // own ones first; the rest keep the default order after them
		}
	})
	return out
}

// putMyOrder: PUT /api/me/order {"ids": [...]} – the user's own order; an empty list = use the default again.
// Only cameras the user may see are kept (nobody learns about other cameras through it).
func (s *Server) putMyOrder(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	if u.ID <= 0 {
		jsonErr(w, 403, "this login has no order of its own")
		return
	}
	var in struct {
		IDs []string `json:"ids"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if len(in.IDs) > 1000 {
		jsonErr(w, 400, "too many cameras")
		return
	}
	seen := map[string]bool{}
	var l []string
	for _, id := range in.IDs {
		if !seen[id] && u.CanSee(id) && (s.runner(id) != nil || cams.Get(id) != nil) {
			seen[id] = true
			l = append(l, id)
		}
	}
	s.setUserOrder(u.ID, l)
	if len(l) == 0 {
		log.Printf("%s uses the default camera order again", u.Name)
	} else {
		log.Printf("%s changed their own camera order", u.Name)
	}
	writeJSON(w, map[string]any{"ok": true, "own_order": len(l) > 0})
}

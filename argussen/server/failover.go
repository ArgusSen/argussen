package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"argus/bc"
)

// Fallback methods: each stream has a prioritised list (e.g. bc → flv → rtsp). ArgusSen uses the first one that works,
// switches to the next if the stream cannot keep running for 5 min (or fails immediately 3 times in a row –
// typically because the camera does not support the method), and quietly tries every 30 min whether the preferred one works again.

// bc logs in without sending the password; RTSP usually uses digest login; FLV sends the password in the address over
// HTTPS without a certificate check (Reolink cameras have self-signed certificates) – so it comes last.
var defaultProtos = []string{"bc", "rtsp", "flv"}

const (
	switchAfter   = 5 * time.Minute
	fastFail      = 20 * time.Second
	fastFailLimit = 3
	stableRun     = 2 * time.Minute
)

// protos: "bc,flv,rtsp" or one method; empty/"auto" = the default list.
func (c *Camera) protos(stream string) []string {
	if !c.reolink() {
		return []string{"rtsp"} // bc and flv are Reolink's own
	}
	v := c.MainProto
	if stream == "sub" {
		v = c.SubProto
	}
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" || v == "auto" {
		return defaultProtos
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		p = strings.TrimSpace(p)
		if p == "bc" || p == "flv" || p == "rtsp" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return []string{"rtsp"}
	}
	return out
}

type protoState struct {
	list    []string
	idx     int
	pending int // >= 0: switch to this index at the next restart
}

func (r *CamRunner) initProtos() {
	r.protoMu.Lock()
	defer r.protoMu.Unlock()
	r.pstate = map[string]*protoState{}
	for _, s := range []string{"main", "sub"} {
		ps := &protoState{list: r.cam.protos(s), pending: -1}
		if remembered := r.ev.settings.GetKV("proto:" + r.cam.ID + ":" + s); remembered != "" {
			for i, p := range ps.list {
				if p == remembered {
					ps.idx = i
				}
			}
		}
		r.pstate[s] = ps
	}
}

// proto: the method the stream uses right now.
func (r *CamRunner) proto(stream string) string {
	r.protoMu.Lock()
	defer r.protoMu.Unlock()
	ps := r.pstate[stream]
	return ps.list[ps.idx]
}

func (r *CamRunner) setVia(stream string) {
	p := r.proto(stream)
	r.set(func(s *CamState) {
		if stream == "main" {
			s.MainVia = p
		} else {
			s.SubVia = p
		}
	})
}

// switchProto goes to the next method in the list (or to a given index) and logs it.
func (r *CamRunner) switchProto(stream string, to int, reason string) {
	r.protoMu.Lock()
	ps := r.pstate[stream]
	from := ps.list[ps.idx]
	if to < 0 {
		to = (ps.idx + 1) % len(ps.list)
	}
	ps.idx = to
	next := ps.list[to]
	r.protoMu.Unlock()
	if from != next {
		log.Printf("%s/%s: switching from %s to %s (%s)", r.cam.ID, stream, from, next, reason)
	}
}

// markWorking: the stream has run stably – remember the method for the next start.
func (r *CamRunner) markWorking(stream string) {
	p := r.proto(stream)
	k := "proto:" + r.cam.ID + ":" + stream
	if r.ev.settings.GetKV(k) != p {
		r.ev.settings.SetKV(k, p)
	}
}

func (r *CamRunner) retryPrefer() time.Duration {
	if r.cfg.PreferRetryMin > 0 {
		return time.Duration(r.cfg.PreferRetryMin) * time.Minute
	}
	return 30 * time.Minute
}

// probe: quick test connection with a method (used to see whether the preferred one works again).
func (r *CamRunner) probe(ctx context.Context, stream, proto string) error {
	r.gate(ctx)
	if proto == "bc" {
		st := "mainStream"
		if stream == "sub" {
			st = "subStream"
		}
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		got := false
		err := bc.Stream(cctx, fmt.Sprintf("%s:%d", r.cam.Host, r.cam.bcPort()), r.cam.user, r.cam.pass, st, func(f bc.Frame) {
			if f.Key {
				got = true
				cancel()
			}
		}, nil)
		if got {
			return nil
		}
		if err == nil {
			err = fmt.Errorf("no image")
		}
		return err
	}
	_, _, err := probeSize(ctx, r.cam.urlFor(stream, proto))
	return err
}

// preferLoop: when the stream runs on a fallback method, the preferred one is tried every 30 min.
func (r *CamRunner) preferLoop(ctx context.Context, stream string) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(r.retryPrefer()):
		}
		r.protoMu.Lock()
		ps := r.pstate[stream]
		onPreferred := ps.idx == 0
		best := ps.list[0]
		r.protoMu.Unlock()
		if onPreferred {
			continue
		}
		if err := r.probe(ctx, stream, best); err != nil {
			continue
		}
		log.Printf("%s/%s: %s works again – switching back", r.cam.ID, stream, best)
		r.protoMu.Lock()
		ps.pending = 0
		r.protoMu.Unlock()
		r.procMu.Lock()
		if c := r.procs[stream]; c != nil && c.Process != nil {
			c.Process.Kill()
		}
		r.procMu.Unlock()
	}
}

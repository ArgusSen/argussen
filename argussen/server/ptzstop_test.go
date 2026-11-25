package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// If the app stops sending "move" and the camera does not get the first stop, ArgusSen tries again.
func TestPTZWatchdogRetriesStop(t *testing.T) {
	var mu sync.Mutex
	moves, stops := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(string(b), "ContinuousMove"):
			moves++
		case strings.Contains(string(b), "<tptz:Stop>"):
			stops++
			if stops == 1 { // the first stop is lost
				w.WriteHeader(500)
				return
			}
		}
		w.Write([]byte(`<?xml version="1.0"?><s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body/></s:Envelope>`))
	}))
	defer srv.Close()
	p := &ptzCam{o: newOnvif("127.0.0.1", 1, "", ""), addr: srv.URL, profile: "p", ok: true}
	p.mu.Lock()
	if err := p.move(context.Background(), 1, 0, 0); err != nil {
		t.Fatal(err)
	}
	p.mu.Unlock()
	time.Sleep(3500 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if moves != 1 || stops != 2 {
		t.Fatalf("moves %d stops %d – want 1 and 2 (the lost stop sent again)", moves, stops)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.moving {
		t.Fatal("still marked as moving")
	}
}

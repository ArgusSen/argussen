package main

import (
	"sync"
	"testing"
	"time"
)

func testFrame(at time.Time) *Frame {
	f := &Frame{W: 64, H: 48, YUV: make([]byte, 64*48*3/2), At: at}
	for i := range f.YUV {
		f.YUV[i] = byte(i)
	}
	return f
}

// The still image is shared within a second and made again from a newer frame; no image without a recent frame.
func TestSnapshotCache(t *testing.T) {
	r := &CamRunner{}
	if jpg, err := r.Snapshot(); jpg != nil || err != nil {
		t.Fatalf("no frame: got %d bytes, %v", len(jpg), err)
	}
	now := time.Now()
	r.last = testFrame(now.Add(-time.Minute))
	if jpg, _ := r.Snapshot(); jpg != nil {
		t.Fatal("a frame older than 30 s must give no image")
	}
	r.last = testFrame(now)
	a, err := r.Snapshot()
	if err != nil || len(a) == 0 {
		t.Fatal(err)
	}
	r.last = testFrame(now.Add(500 * time.Millisecond))
	if b, _ := r.Snapshot(); &b[0] != &a[0] {
		t.Fatal("a frame 0.5 s newer must use the cached image")
	}
	r.last = testFrame(now.Add(1500 * time.Millisecond))
	if c, _ := r.Snapshot(); &c[0] == &a[0] {
		t.Fatal("a frame 1.5 s newer must give a new image")
	}
	// many at once (go test -race)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r.mu.Lock()
			r.last = testFrame(time.Now())
			r.mu.Unlock()
			if jpg, err := r.Snapshot(); err != nil || len(jpg) == 0 {
				t.Error("snapshot failed", err)
			}
		}(i)
	}
	wg.Wait()
}

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The fast live view: ffmpeg's fragmented MP4 on fd 3 is split into an init segment and one fragment per frame,
// with the key frames found (a test picture with a key frame every 10th frame).
func TestMSEFragments(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	for _, tc := range []struct{ enc, want string }{{"libx264", "avc1."}, {"libx265", "hev1."}} {
		t.Run(tc.enc, func(t *testing.T) {
			enc := "-c:v " + tc.enc + " -g 10"
			if tc.enc == "libx265" {
				enc += " -x265-params log-level=error"
			}
			// a camera: a test picture in real time as MPEG-TS into ffmpeg with the fast live view's output on fd 3
			mux := exec.Command("bash", "-c", "ffmpeg -nostdin -loglevel error -re -f lavfi -i testsrc2=size=320x240:rate=10 -t 3 "+enc+
				" -f mpegts - | ffmpeg -nostdin -loglevel error -f mpegts -i pipe:0 "+strings.Join(mseArgs(), " "))
			pr, pw, _ := os.Pipe()
			mux.ExtraFiles = []*os.File{pw}
			if err := mux.Start(); err != nil {
				t.Fatal(err)
			}
			pw.Close()
			h := &mseHub{viewers: map[*mseViewer]struct{}{}}
			var frags, keys int
			done := make(chan struct{})
			// a viewer that counts what it gets (joined as soon as the hub is ready)
			go func() {
				defer close(done)
				feedMSE(pr, h)
			}()
			var v *mseViewer
			var gop []mseFrag
			for i := 0; i < 250 && v == nil; i++ {
				v, _, _, gop, _ = h.join()
				time.Sleep(20 * time.Millisecond)
			}
			for _, f := range gop {
				frags++
				if f.key {
					keys++
				}
			}
			if v == nil {
				t.Fatal("the hub never became ready")
			}
			go mux.Wait()
			timeout := time.After(15 * time.Second)
		loop:
			for {
				select {
				case f, ok := <-v.ch:
					if !ok {
						break loop
					}
					frags++
					if f.key {
						keys++
					}
				case <-done:
					// drain what is left
					for {
						select {
						case f := <-v.ch:
							frags++
							if f.key {
								keys++
							}
						default:
							break loop
						}
					}
				case <-timeout:
					t.Fatal("timeout")
				}
			}
			if !strings.HasPrefix(h.codec, tc.want) {
				t.Errorf("codec %q, want %s…", h.codec, tc.want)
			}
			if h.frames < 28 || h.keys < 3 || h.keys > 4 {
				t.Errorf("hub: %d fragments, %d key frames (3 s at 10 fps, a key frame every 10th)", h.frames, h.keys)
			}
			if frags < 1 || keys != 1 {
				t.Errorf("viewer: %d fragments, %d key frames (it starts at a key frame)", frags, keys)
			}
			t.Logf("%s: codec %s, hub %d fragments/%d key frames, viewer %d fragments, init %d bytes", tc.enc, h.codec, h.frames, h.keys, frags, len(h.init))
		})
	}
}

func TestMoofKeyAndCodec(t *testing.T) {
	// an avcC box: version 1, profile 0x64 (High), compatibility 0, level 0x28 (4.0)
	if c := mp4Codec([]byte("....avcC\x01\x64\x00\x28")); c != "avc1.640028" {
		t.Errorf("avc: %q", c)
	}
	// an hvcC box: Main profile, compatibility flag 1 and 2 set, constraint 0x90, level 93 (3.1)
	hv := []byte("....hvcC\x01\x01\x60\x00\x00\x00\x90\x00\x00\x00\x00\x00\x5d")
	if c := mp4Codec(hv); c != "hev1.1.6.L93.90" {
		t.Errorf("hevc: %q", c)
	}
}

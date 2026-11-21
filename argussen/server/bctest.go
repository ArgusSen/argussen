package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"argus/bc"
)

// bcTest fetches a stream via Reolink's own protocol for a number of seconds and saves the raw video (debugging).
func bcTest(cfg *Config, args []string) int {
	if len(args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: argus bctest <camera> <mainStream|subStream|externStream> <seconds> <file.h264>")
		return 2
	}
	var cam *Camera
	for _, c := range cams.All() {
		if c.ID == args[0] {
			cam = c
		}
	}
	if cam == nil {
		fmt.Fprintln(os.Stderr, "unknown camera")
		return 2
	}
	seconds, _ := strconv.Atoi(args[2])
	f, err := os.Create(args[3])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
	defer cancel()
	var st bc.Stats
	start := time.Now()
	var first time.Duration = -1
	var mux *bc.TSMuxer
	if strings.HasSuffix(args[3], ".ts") {
		mux = bc.NewTSMuxer(f)
	}
	err = bc.Stream(ctx, cam.Host+":9000", cam.user, cam.pass, args[1], func(fr bc.Frame) {
		if first < 0 {
			first = time.Since(start)
		}
		if mux != nil {
			mux.WriteFrame(fr)
		} else {
			f.Write(fr.Data)
		}
		if os.Getenv("BCDEBUG") != "" && st.Frames < 25 {
			nal := []int{}
			d := fr.Data
			for i := 0; i+4 < len(d) && len(nal) < 6; i++ {
				if d[i] == 0 && d[i+1] == 0 && (d[i+2] == 1 || (d[i+2] == 0 && d[i+3] == 1)) {
					j := i + 3
					if d[i+2] == 0 {
						j = i + 4
					}
					nal = append(nal, int(d[j]&0x1f))
					i = j
				}
			}
			fmt.Printf("frame key=%v %s %d bytes, NAL types %v\n", fr.Key, fr.Codec, len(fr.Data), nal)
		}
	}, &st)
	fmt.Printf("frames %d (keyframes %d), %d kB, skipped %d, info %dx%d, first frame after %v, error: %v\n",
		st.Frames, st.KeyFrames, st.Bytes/1024, st.Skipped, st.Width, st.Height, first.Round(time.Millisecond), err)
	if err != nil {
		return 1
	}
	return 0
}

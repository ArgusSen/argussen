package main

import (
	"strings"
	"testing"
)

func TestURLFor(t *testing.T) {
	c := &Camera{Host: "192.168.1.20", Port: 554, user: "admin", pass: "p@ss word"}
	normalizeCam(c)
	if got := c.urlFor("main", "rtsp"); got != "rtsp://admin:p%40ss%20word@192.168.1.20:554/h264Preview_01_main" {
		t.Errorf("reolink: %s", got)
	}
	d := &Camera{Kind: "rtsp", Host: "192.168.1.21", Port: 554, Main: "/cam/realmonitor?channel=1&subtype=0", Sub: "cam/realmonitor?channel=1&subtype=1", user: "u", pass: "x"}
	if got := d.urlFor("main", "rtsp"); got != "rtsp://u:x@192.168.1.21:554/cam/realmonitor?channel=1&subtype=0" {
		t.Errorf("query: %s", got)
	}
	if got := d.urlFor("sub", "rtsp"); got != "rtsp://u:x@192.168.1.21:554/cam/realmonitor?channel=1&subtype=1" {
		t.Errorf("no slash: %s", got)
	}
	e := &Camera{Kind: "onvif", Host: "10.0.0.5", Port: 554, Main: "rtsps://192.168.1.1:7441/abc?enableSrtp", Sub: "rtsp://192.168.9.9/sub"}
	if got := e.urlFor("main", "rtsp"); got != "rtsps://10.0.0.5:7441/abc?enableSrtp" {
		t.Errorf("full url: %s", got)
	}
	if got := e.urlFor("sub", "rtsp"); got != "rtsp://10.0.0.5/sub" {
		t.Errorf("full url without port: %s", got)
	}
	if got := hidePass("rtsp://u:p%40ss%20word@h/x failed", "p@ss word"); got != "rtsp://u:***@h/x failed" {
		t.Errorf("hidePass: %s", got)
	}
	if got := hidePass("abc", ""); got != "abc" {
		t.Errorf("empty pass: %s", got)
	}
	if p := e.protos("main"); len(p) != 1 || p[0] != "rtsp" {
		t.Errorf("protos: %v", p)
	}
}

func TestValidation(t *testing.T) {
	for _, h := range []string{"192.168.1.20", "cam.local", "fe80::1", "garage-cam"} {
		if !validHost(h) {
			t.Errorf("host %q should be valid", h)
		}
	}
	for _, h := range []string{"http://x", "x:554", "a b", "-i", "a/b", "x@y", ""} {
		if validHost(h) {
			t.Errorf("host %q should be refused", h)
		}
	}
	for _, p := range []string{"/stream1", "stream1", "/cam/realmonitor?channel=1&subtype=0", "rtsp://h:554/x", "rtsps://h:7441/abc?enableSrtp", "/media.amp?resolution=640x480"} {
		if !validStreamPath(p) {
			t.Errorf("path %q should be valid", p)
		}
	}
	for _, p := range []string{"file:///data/argus.db", "http://h/x", "concat:a|b", "rtsp://u:p@h/x", "/a b", "subfile,,start,0,end,0,,:x"} {
		if validStreamPath(p) {
			t.Errorf("path %q should be refused", p)
		}
	}
	e := &Camera{Kind: "rtsp", Host: "h", Port: 554, Main: "file:///etc/passwd"}
	if got := e.urlFor("main", "rtsp"); got != "rtsp://h:554/file:/etc/passwd" && !strings.HasPrefix(got, "rtsp://") {
		t.Errorf("urlFor must stay rtsp: %s", got)
	}
}

func TestBrandFromModel(t *testing.T) {
	for model, want := range map[string]string{"DCS-2132LB": "D-Link", "DS-2CD2143G2-I": "Hikvision", "IPC-HDW2431T": "Dahua", "RLC-520A": "", "": ""} {
		if got := brandFromModel(model); got != want {
			t.Errorf("%q: got %q, want %q", model, got, want)
		}
	}
}

func TestSubTooBig(t *testing.T) {
	for _, c := range []struct {
		w, h int
		big  bool
	}{{640, 480, false}, {1280, 720, false}, {1280, 960, false}, {1536, 432, false}, {896, 672, false},
		{2560, 1920, true}, {1920, 1080, true}, {7680, 2160, true}, {3840, 2160, true}} {
		if got := subTooBig(c.w, c.h); got != c.big {
			t.Errorf("%dx%d: got %v", c.w, c.h, got)
		}
	}
}

func TestIntegrationShowsRepair(t *testing.T) {
	for v, want := range map[string]bool{"": false, "1.0.0": false, "0.10.1": false, "1.1.0-beta.5": false, "1.1.0-beta.6": true,
		"1.1.0-beta.12": true, "1.1.0": true, "1.1.1": true, "1.2.0-beta.1": true, "2.0.0": true, "dev": false} {
		if got := integrationShowsRepair(v); got != want {
			t.Errorf("%q: got %v", v, got)
		}
	}
}

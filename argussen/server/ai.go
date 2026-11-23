package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"time"
)

type Box struct {
	Label string   `json:"label"` // shown name
	Conf  float64  `json:"conf"`
	X1    int      `json:"x1"`
	Y1    int      `json:"y1"`
	X2    int      `json:"x2"`
	Y2    int      `json:"y2"`
	Zones []string `json:"zones,omitempty"` // the zones its foot point is in (zones.go)
}

var aiClient = &http.Client{Timeout: 10 * time.Second}

// aiSlots limits how many images the AI server gets at once.
var aiSlots = make(chan struct{}, 2)

func frameJPEG(f *Frame, q int) ([]byte, error) {
	img := &image.YCbCr{
		Y: f.YUV[:f.W*f.H], Cb: f.YUV[f.W*f.H : f.W*f.H*5/4], Cr: f.YUV[f.W*f.H*5/4:],
		YStride: f.W, CStride: f.W / 2, SubsampleRatio: image.YCbCrSubsampleRatio420,
		Rect: image.Rect(0, 0, f.W, f.H),
	}
	var b bytes.Buffer
	err := jpeg.Encode(&b, img, &jpeg.Options{Quality: q})
	return b.Bytes(), err
}

func detect(base string, jpg []byte, labels map[string]string, minConf float64) ([]Box, error) {
	aiSlots <- struct{}{}
	defer func() { <-aiSlots }()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("image", "frame.jpg")
	fw.Write(jpg)
	mw.Close()
	resp, err := aiClient.Post(base+"/v1/vision/detection", mw.FormDataContentType(), &body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var r struct {
		Success     bool   `json:"success"`
		Error       string `json:"error"`
		Predictions []struct {
			Label      string  `json:"label"`
			Confidence float64 `json:"confidence"`
			XMin       int     `json:"x_min"`
			YMin       int     `json:"y_min"`
			XMax       int     `json:"x_max"`
			YMax       int     `json:"y_max"`
		} `json:"predictions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	if !r.Success {
		return nil, fmt.Errorf("Blue Onyx: %s", r.Error)
	}
	var out []Box
	for _, p := range r.Predictions {
		name, ok := labels[p.Label]
		if !ok || p.Confidence < minConf {
			continue
		}
		out = append(out, Box{Label: name, Conf: p.Confidence, X1: p.XMin, Y1: p.YMin, X2: p.XMax, Y2: p.YMax})
	}
	return out, nil
}

func iou(a, b Box) float64 {
	x1, y1 := max(a.X1, b.X1), max(a.Y1, b.Y1)
	x2, y2 := min(a.X2, b.X2), min(a.Y2, b.Y2)
	if x2 <= x1 || y2 <= y1 {
		return 0
	}
	inter := float64((x2 - x1) * (y2 - y1))
	ua := float64((a.X2-a.X1)*(a.Y2-a.Y1)+(b.X2-b.X1)*(b.Y2-b.Y1)) - inter
	return inter / ua
}

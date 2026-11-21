package main

import "time"

// MotionDetector compares small blocks (8×8 pixels) with a slowly updated background.
// Blocks that change almost all the time (a timestamp in the image, leaves in the wind) are masked automatically.
type MotionDetector struct {
	cfg      MotionConfig
	w, h     int
	bw, bh   int
	bg       []float32
	freq     []float32                           // how often the block has changed (moving average)
	params   func() (threshold, minArea float64) // read for every frame so settings take effect immediately
	inited   int
	hits     int
	lastMove time.Time
}

const blk = 8

func newMotionDetector(w, h int, cfg MotionConfig, params func() (float64, float64)) *MotionDetector {
	bw, bh := w/blk, h/blk
	return &MotionDetector{cfg: cfg, params: params, w: w, h: h, bw: bw, bh: bh,
		bg: make([]float32, bw*bh), freq: make([]float32, bw*bh)}
}

// Feed takes the Y plane and answers whether there is motion (with a hold time of HoldSec).
func (m *MotionDetector) Feed(y []byte) bool {
	cur := make([]float32, m.bw*m.bh)
	for by := 0; by < m.bh; by++ {
		for bx := 0; bx < m.bw; bx++ {
			sum := 0
			for yy := 0; yy < blk; yy++ {
				row := (by*blk+yy)*m.w + bx*blk
				for xx := 0; xx < blk; xx++ {
					sum += int(y[row+xx])
				}
			}
			cur[by*m.bw+bx] = float32(sum) / (blk * blk)
		}
	}
	now := time.Now()
	thr, minArea := m.cfg.Threshold, m.cfg.MinArea
	if m.params != nil {
		thr, minArea = m.params()
	}
	if m.inited < 3 {
		copy(m.bg, cur)
		m.inited++
		return false
	}
	changed, masked := 0, 0
	for i, v := range cur {
		d := v - m.bg[i]
		if d < 0 {
			d = -d
		}
		c := d > float32(thr)
		if c {
			m.freq[i] = m.freq[i]*0.98 + 0.02
		} else {
			m.freq[i] *= 0.98
		}
		if m.freq[i] > 0.4 {
			masked++
		} else if c {
			changed++
		}
		// the background follows slowly (faster where there is no change)
		a := float32(0.05)
		if c {
			a = 0.01
		}
		m.bg[i] += (v - m.bg[i]) * a
	}
	total := len(cur) - masked
	if total <= 0 {
		total = 1
	}
	frac := float64(changed) / float64(total)
	if frac >= minArea && frac <= m.cfg.MaxArea {
		m.hits++
	} else {
		m.hits = 0
	}
	if frac > m.cfg.MaxArea { // light change (IR on/off, sun behind a cloud): reset the background
		copy(m.bg, cur)
	}
	if m.hits >= 2 {
		m.lastMove = now
	}
	return !m.lastMove.IsZero() && now.Sub(m.lastMove) < time.Duration(m.cfg.HoldSec)*time.Second
}

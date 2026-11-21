package bc

import (
	"io"
)

// TSMuxer packs video frames in MPEG-TS (the format ffmpeg in ArgusSen already reads and stores),
// with timestamps from the camera's own microsecond counter. Video only, one program.
type TSMuxer struct {
	w        io.Writer
	cc       map[uint16]byte
	pts      int64 // 90 kHz
	last     uint32
	have     bool
	lastStep int64
	hevc     bool
	buf      []byte
}

const (
	pidPMT   = 0x1000
	pidVideo = 0x100
)

func NewTSMuxer(w io.Writer) *TSMuxer {
	return &TSMuxer{w: w, cc: map[uint16]byte{}, pts: 90000, lastStep: 6000}
}

// WriteFrame writes one frame. Jumps in the camera's clock (>5 s, or backwards) are smoothed to a normal
// frame interval so the recording does not get gaps or go backwards.
func (m *TSMuxer) WriteFrame(f Frame) error {
	if m.have {
		d := int64(f.Micros - m.last) // uint32 subtraction handles overflow (every ~71 minutes)
		step := d * 9 / 100           // µs → 90 kHz
		if d <= 0 || d > 5_000_000 {
			step = m.lastStep
		} else {
			m.lastStep = step
		}
		m.pts += step
	}
	m.last, m.have = f.Micros, true
	m.hevc = f.Codec == "H265"
	m.buf = m.buf[:0]
	if f.Key {
		m.psi()
	}
	m.pes(f)
	_, err := m.w.Write(m.buf)
	return err
}

func (m *TSMuxer) packet(pid uint16, pusi bool, af []byte, payload []byte) int {
	space := 184
	if af != nil {
		space -= 1 + len(af)
	}
	n := len(payload)
	if n > space {
		n = space
	}
	stuff := space - n
	if stuff > 0 { // padding via the adaptation field
		if af == nil {
			if stuff == 1 {
				af = []byte{}
			} else {
				af = make([]byte, stuff-1)
				af[0] = 0x00
				for i := 1; i < len(af); i++ {
					af[i] = 0xff
				}
			}
		} else {
			for i := 0; i < stuff; i++ {
				af = append(af, 0xff)
			}
		}
	}
	h := byte(0x47)
	b1 := byte(pid>>8) & 0x1f
	if pusi {
		b1 |= 0x40
	}
	ctrl := byte(0x10)
	if af != nil {
		ctrl = 0x30
	}
	cc := m.cc[pid]
	m.cc[pid] = (cc + 1) & 0x0f
	m.buf = append(m.buf, h, b1, byte(pid), ctrl|cc)
	if af != nil {
		m.buf = append(m.buf, byte(len(af)))
		m.buf = append(m.buf, af...)
	}
	m.buf = append(m.buf, payload[:n]...)
	return n
}

func (m *TSMuxer) section(pid uint16, sec []byte) {
	crc := crc32mpeg(sec)
	sec = append(sec, byte(crc>>24), byte(crc>>16), byte(crc>>8), byte(crc))
	p := append([]byte{0x00}, sec...) // pointer field
	for len(p) < 184 {
		p = append(p, 0xff)
	}
	m.packet(pid, true, nil, p)
}

func (m *TSMuxer) psi() {
	// PAT: program 1 → PMT
	pat := []byte{0x00, 0xb0, 0x0d, 0x00, 0x01, 0xc1, 0x00, 0x00, 0x00, 0x01, 0xe0 | byte(pidPMT>>8), byte(pidPMT & 0xff)}
	m.section(0, pat)
	st := byte(0x1b) // H.264
	if m.hevc {
		st = 0x24
	}
	pmt := []byte{0x02, 0xb0, 0x12, 0x00, 0x01, 0xc1, 0x00, 0x00,
		0xe0 | byte(pidVideo>>8), byte(pidVideo & 0xff), 0xf0, 0x00,
		st, 0xe0 | byte(pidVideo>>8), byte(pidVideo & 0xff), 0xf0, 0x00}
	m.section(pidPMT, pmt)
}

func (m *TSMuxer) pes(f Frame) {
	p := m.pts & 0x1ffffffff
	hdr := []byte{0x00, 0x00, 0x01, 0xe0, 0x00, 0x00, 0x80, 0x80, 0x05,
		byte(0x21 | (p>>29)&0x0e), byte(p >> 22), byte(0x01 | (p>>14)&0xfe), byte(p >> 7), byte(0x01 | (p<<1)&0xfe)}
	data := append(hdr, f.Data...)
	// first packet: PCR (slightly before PTS) and "random access" at keyframes
	pcr := (m.pts - 4500) & 0x1ffffffff
	flags := byte(0x10)
	if f.Key {
		flags |= 0x40
	}
	af := []byte{flags, byte(pcr >> 25), byte(pcr >> 17), byte(pcr >> 9), byte(pcr >> 1), byte(pcr<<7) | 0x7e, 0x00}
	n := m.packet(pidVideo, true, af, data)
	data = data[n:]
	for len(data) > 0 {
		n = m.packet(pidVideo, false, nil, data)
		data = data[n:]
	}
}

var crcTab = func() (t [256]uint32) {
	for i := range t {
		c := uint32(i) << 24
		for j := 0; j < 8; j++ {
			if c&0x80000000 != 0 {
				c = c<<1 ^ 0x04c11db7
			} else {
				c <<= 1
			}
		}
		t[i] = c
	}
	return
}()

func crc32mpeg(b []byte) uint32 {
	c := uint32(0xffffffff)
	for _, x := range b {
		c = c<<8 ^ crcTab[byte(c>>24)^x]
	}
	return c
}

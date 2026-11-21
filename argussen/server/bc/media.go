package bc

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// The media packets' "magic" (the first 4 bytes, read as text).
var (
	magicIFrame = []byte("00dc")
	magicPFrame = []byte("01dc")
	magicInfo1  = []byte("1001")
	magicInfo2  = []byte("1002")
	magicAAC    = []byte("05wb")
	magicADPCM  = []byte("02wb")
)

// Frame is one video frame as H.264/H.265 Annex-B (with start codes), ready for ffmpeg.
type Frame struct {
	Key    bool
	Codec  string // "H264" or "H265"
	Micros uint32 // the camera's own time counter (microseconds)
	Data   []byte
}

// Stats are counted continuously so they can be shown/logged.
type Stats struct {
	Frames, KeyFrames, Bytes, Skipped int64
	Width, Height                     int
	OnInfo                            func(w, h int) // called when the camera reports the frame size
}

var streamIDs = map[string]byte{"mainStream": 0, "subStream": 1, "externStream": 4}

// Stream logs in, asks for a stream ("mainStream", "subStream" or "externStream") and calls
// onFrame for every video frame until ctx is cancelled or the connection dies. At the end stop is sent
// (otherwise the camera refuses new connections for a few minutes).
func Stream(ctx context.Context, addr, user, pass, streamType string, onFrame func(Frame), st *Stats) error {
	sid, ok := streamIDs[streamType]
	if !ok {
		return fmt.Errorf("unknown stream %q", streamType)
	}
	c, err := Dial(addr, user, pass, 15*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	c.counter++
	enc := uint32(0) | uint32(sid)<<8 | uint32(c.counter)<<16 // [channel, stream, counter, handle]
	handle := 0
	if sid != 0 {
		handle = 256 * int(sid)
	}
	preview := fmt.Sprintf("<?xml version=\"1.0\" encoding=\"UTF-8\" ?>\n<body>\n<Preview version=\"1.1\">\n<channelId>0</channelId>\n<handle>%d</handle>\n<streamType>%s</streamType>\n</Preview>\n</body>\n", handle, streamType)
	if err := c.send(msgPreview, enc, nil, []byte(preview)); err != nil {
		return err
	}
	defer func() {
		c.send(msgStop, enc, nil, []byte(preview))
		c.send(msgLogout, 0, nil, nil)
		time.Sleep(200 * time.Millisecond)
	}()

	// ping every 10 seconds so the camera does not close the session
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				c.nc.Close()
				return
			case <-t.C:
				c.send(msgPing, 0, nil, nil)
			}
		}
	}()

	var buf []byte // the media packet being assembled
	inPacket := false
	for {
		c.nc.SetReadDeadline(time.Now().Add(20 * time.Second))
		m, err := c.readMsg()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if m.MsgID != msgPreview {
			continue // ping answers etc.
		}
		if m.Status != 200 && m.Status != 0 {
			return fmt.Errorf("the camera refused the stream (status %d)", m.Status)
		}
		pay := m.rawPay
		if len(m.Ext) > 0 {
			if bytes.Contains(m.Ext, []byte("<binaryData>1</binaryData>")) {
				// a new media packet starts here; leftovers from the previous one are padding
				buf = buf[:0]
				inPacket = true
			}
			if n, ok := tagInt(m.Ext, "encryptLen"); ok && n > 0 {
				if n > len(pay) {
					n = len(pay)
				}
				dec := c.aesCrypt(pay[:n], false)
				pay = append(dec, pay[n:]...)
			}
		}
		if !inPacket {
			continue
		}
		if debug && len(m.Ext) > 0 {
			fmt.Printf("message: ext=%q pay=%d byte, start=% x\n", m.Ext, len(pay), pay[:min(16, len(pay))])
		}
		buf = append(buf, pay...)
		// take out all complete packets in the buffer
		for {
			n, f, done, perr := parsePacket(buf, st)
			if perr != nil {
				if st != nil {
					st.Skipped++
				}
				buf = buf[:0]
				inPacket = false
				break
			}
			if !done {
				break
			}
			if f != nil {
				if st != nil {
					st.Frames++
					st.Bytes += int64(len(f.Data))
					if f.Key {
						st.KeyFrames++
					}
				}
				onFrame(*f)
			}
			buf = buf[n:]
			if len(buf) < 4 {
				break
			}
		}
	}
}

var debug = os.Getenv("BCDEBUG") != ""

func pad8(n int) int { return (n + 7) &^ 7 }

// parsePacket reads one media packet from the start of b. done=false: data is missing. n = bytes used (incl. padding to 8).
func parsePacket(b []byte, st *Stats) (n int, f *Frame, done bool, err error) {
	if len(b) < 8 {
		return 0, nil, false, nil
	}
	mg := b[:4]
	le := binary.LittleEndian
	switch {
	case bytes.Equal(mg, magicIFrame) || bytes.Equal(mg, magicPFrame):
		key := bytes.Equal(mg, magicIFrame)
		if len(b) < 24 {
			return 0, nil, false, nil
		}
		codec := string(b[4:8])
		size := int(le.Uint32(b[8:]))
		// byte 12: the length of the extra header field after the fixed 24 bytes (I-frames: 8 = the camera's clock,
		// some models: 80–352 bytes of metadata). The frame starts after it and is size bytes long.
		extra := int(le.Uint32(b[12:]))
		micros := le.Uint32(b[16:])
		if size <= 0 || size > 8<<20 || extra < 0 || extra > 64<<10 {
			return 0, nil, false, errors.New("invalid frame size")
		}
		hl := 24 + extra
		total := hl + size
		if len(b) < total {
			return 0, nil, false, nil
		}
		data := b[hl:total]
		// safety: the frame must start with a start code; otherwise find it in the prefix
		if !bytes.HasPrefix(data, []byte{0, 0, 0, 1}) && !bytes.HasPrefix(data, []byte{0, 0, 1}) {
			i := bytes.Index(b[hl:total], []byte{0, 0, 0, 1})
			if i < 0 {
				return 0, nil, false, errors.New("no start code in the frame")
			}
			data = b[hl+i : total]
		}
		out := make([]byte, len(data))
		copy(out, data)
		return pad8(total), &Frame{Key: key, Codec: codec, Micros: micros, Data: out}, true, nil
	case bytes.Equal(mg, magicInfo1) || bytes.Equal(mg, magicInfo2):
		hl := int(le.Uint32(b[4:]))
		if hl < 8 || hl > 4096 {
			hl = 32
		}
		if len(b) < hl {
			return 0, nil, false, nil
		}
		if st != nil && hl >= 16 {
			st.Width, st.Height = int(le.Uint32(b[8:])), int(le.Uint32(b[12:]))
			if st.OnInfo != nil {
				st.OnInfo(st.Width, st.Height)
			}
		}
		return pad8(hl), nil, true, nil
	case bytes.Equal(mg, magicAAC) || bytes.Equal(mg, magicADPCM):
		size := int(le.Uint16(b[4:]))
		total := 8 + size
		if len(b) < total {
			return 0, nil, false, nil
		}
		return pad8(total), nil, true, nil // audio is not used
	}
	return 0, nil, false, fmt.Errorf("unknown packet %q", mg)
}

// WriteAnnexB writes the frames to w (e.g. ffmpeg's stdin). Stops at the first write error.
func WriteAnnexB(w io.Writer) func(Frame) {
	return func(f Frame) { w.Write(f.Data) }
}

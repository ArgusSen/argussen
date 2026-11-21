// Package bc fetches video from Reolink cameras over their own protocol ("Baichuan", TCP port 9000),
// the same one Reolink's own recorders use. Written for ArgusSen from scratch from protocol descriptions
// (no code copied). Only what ArgusSen needs: login, live stream (main/sub/extern), ping and stop.
package bc

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const magic = 0x0abcdef0

// Message types (message id) and classes.
const (
	msgLogin   = 1
	msgLogout  = 2
	msgPreview = 3
	msgStop    = 4
	msgPing    = 93

	classLegacy = 0x6514 // first message: negotiation of encryption (20 byte header)
	classReply  = 0x6614 // the camera's answer to the negotiation (20 byte header)
	classModern = 0x6414 // ordinary request (24 byte header with payload offset)
	classResp   = 0x0000 // ordinary answer (24 byte header)
)

func hasPayloadOffset(class uint16) bool { return class != classLegacy && class != classReply }

type header struct {
	MsgID     uint32
	BodyLen   uint32
	EncOffset uint32
	Status    uint16 // response code (200 = ok) – in the first request the two bytes are used for the negotiation
	Class     uint16
	PayOffset uint32
}

type message struct {
	header
	Ext     []byte // extension XML (decrypted)
	Payload []byte // the content itself (XML decrypted; media as it is – see media.go)
	rawPay  []byte
}

// ---- encryption ----

var bcKey = []byte{0x1f, 0x2d, 0x3c, 0x4b, 0x5a, 0x69, 0x78, 0xff}

// bcXor is the simple "BC" encryption, which is only used during login. It is its own inverse.
func bcXor(data []byte, offset uint32) []byte {
	out := make([]byte, len(data))
	for i, b := range data {
		out[i] = b ^ bcKey[(int(offset)+i)%8] ^ byte(offset)
	}
	return out
}

var aesIV = []byte("0123456789abcdef")

func (c *Conn) aesCrypt(data []byte, encrypt bool) []byte {
	if c.aesKey == nil || len(data) == 0 {
		return data
	}
	block, _ := aes.NewCipher(c.aesKey)
	out := make([]byte, len(data))
	if encrypt {
		cipher.NewCFBEncrypter(block, aesIV).XORKeyStream(out, data)
	} else {
		cipher.NewCFBDecrypter(block, aesIV).XORKeyStream(out, data)
	}
	return out
}

func md5Upper(s string) string {
	h := md5.Sum([]byte(s))
	return strings.ToUpper(hex.EncodeToString(h[:]))
}

// ---- connection ----

// Conn is one connection to one camera (one stream per connection, which the cameras prefer).
type Conn struct {
	nc      net.Conn
	r       *bufio.Reader
	wmu     sync.Mutex
	aesKey  []byte
	counter byte
	Info    string // device information from the login (XML)
}

// Dial logs in to the camera.
func Dial(addr, user, pass string, timeout time.Duration) (*Conn, error) {
	if !strings.Contains(addr, ":") {
		addr += ":9000"
	}
	nc, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	if tc, ok := nc.(*net.TCPConn); ok {
		tc.SetKeepAlive(true)
		tc.SetReadBuffer(4 << 20) // large keyframes – better in our buffer than lost at the camera
	}
	c := &Conn{nc: nc, r: bufio.NewReaderSize(nc, 1<<20)}
	nc.SetDeadline(time.Now().Add(timeout))
	if err := c.login(user, pass); err != nil {
		nc.Close()
		return nil, err
	}
	nc.SetDeadline(time.Time{})
	return c, nil
}

func (c *Conn) Close() error { return c.nc.Close() }

var reNonce = regexp.MustCompile(`<nonce>([^<]+)</nonce>`)

func (c *Conn) login(user, pass string) error {
	// 1: empty request that asks for AES (bytes 16/17 = 0x12, 0xdc)
	h := header{MsgID: msgLogin, Class: classLegacy, Status: 0xdc12}
	if err := c.writeRaw(h, nil); err != nil {
		return err
	}
	m, err := c.readMsg()
	if err != nil {
		return fmt.Errorf("no answer to login: %w", err)
	}
	body := bcXor(m.rawPay, m.EncOffset)
	if len(m.Ext) > 0 || !bytes.Contains(body, []byte("nonce")) {
		body = bcXor(append(append([]byte{}, m.Ext...), m.rawPay...), m.EncOffset)
	}
	nm := reNonce.FindSubmatch(body)
	if nm == nil {
		return fmt.Errorf("the camera sent no nonce (status %d)", m.Status)
	}
	nonce := string(nm[1])
	trunc := func(s string) string { return s[:31] }
	x := "<?xml version=\"1.0\" encoding=\"UTF-8\" ?>\n<body>\n<LoginUser version=\"1.1\">\n" +
		"<userName>" + trunc(md5Upper(user+nonce)) + "</userName>\n" +
		"<password>" + trunc(md5Upper(pass+nonce)) + "</password>\n" +
		"<userVer>1</userVer>\n</LoginUser>\n<LoginNet version=\"1.1\">\n<type>LAN</type>\n<udpPort>0</udpPort>\n</LoginNet>\n</body>\n"
	h = header{MsgID: msgLogin, Class: classModern}
	if err := c.writeRaw(h, bcXor([]byte(x), 0)); err != nil {
		return err
	}
	m, err = c.readMsg()
	if err != nil {
		return fmt.Errorf("no answer to username/password: %w", err)
	}
	if m.Status != 200 {
		return fmt.Errorf("login refused (status %d) – wrong username or password?", m.Status)
	}
	c.Info = string(bcXor(m.rawPay, m.EncOffset))
	// from here on AES with a key from the nonce and the password
	c.aesKey = []byte(md5Upper(nonce + "-" + pass)[:16])
	return nil
}

// ---- reading and writing messages ----

func (c *Conn) writeRaw(h header, body []byte) error {
	n := 20
	if hasPayloadOffset(h.Class) {
		n = 24
	}
	buf := make([]byte, n, n+len(body))
	binary.LittleEndian.PutUint32(buf[0:], magic)
	binary.LittleEndian.PutUint32(buf[4:], h.MsgID)
	binary.LittleEndian.PutUint32(buf[8:], uint32(len(body)))
	binary.LittleEndian.PutUint32(buf[12:], h.EncOffset)
	binary.LittleEndian.PutUint16(buf[16:], h.Status)
	binary.LittleEndian.PutUint16(buf[18:], h.Class)
	if n == 24 {
		binary.LittleEndian.PutUint32(buf[20:], h.PayOffset)
	}
	buf = append(buf, body...)
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.nc.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := c.nc.Write(buf)
	return err
}

// send an AES-encrypted request (extension and content are encrypted separately).
func (c *Conn) send(msgID uint32, encOffset uint32, ext, payload []byte) error {
	e := c.aesCrypt(ext, true)
	p := c.aesCrypt(payload, true)
	h := header{MsgID: msgID, Class: classModern, EncOffset: encOffset, PayOffset: uint32(len(e))}
	return c.writeRaw(h, append(e, p...))
}

func (c *Conn) readMsg() (*message, error) {
	var hb [24]byte
	if _, err := io.ReadFull(c.r, hb[:20]); err != nil {
		return nil, err
	}
	if binary.LittleEndian.Uint32(hb[0:]) != magic {
		return nil, errors.New("out of sync (wrong magic)")
	}
	m := &message{}
	m.MsgID = binary.LittleEndian.Uint32(hb[4:])
	m.BodyLen = binary.LittleEndian.Uint32(hb[8:])
	m.EncOffset = binary.LittleEndian.Uint32(hb[12:])
	m.Status = binary.LittleEndian.Uint16(hb[16:])
	m.Class = binary.LittleEndian.Uint16(hb[18:])
	if hasPayloadOffset(m.Class) {
		if _, err := io.ReadFull(c.r, hb[20:24]); err != nil {
			return nil, err
		}
		m.PayOffset = binary.LittleEndian.Uint32(hb[20:])
	}
	if m.BodyLen > 16<<20 {
		return nil, fmt.Errorf("unreasonable message length %d", m.BodyLen)
	}
	body := make([]byte, m.BodyLen)
	if _, err := io.ReadFull(c.r, body); err != nil {
		return nil, err
	}
	po := int(m.PayOffset)
	if po > len(body) {
		po = len(body)
	}
	m.Ext, m.rawPay = body[:po], body[po:]
	if c.aesKey != nil && len(m.Ext) > 0 {
		m.Ext = c.aesCrypt(m.Ext, false)
	}
	return m, nil
}

func tagInt(xml []byte, tag string) (int, bool) {
	i := bytes.Index(xml, []byte("<"+tag+">"))
	if i < 0 {
		return 0, false
	}
	rest := xml[i+len(tag)+2:]
	j := bytes.IndexByte(rest, '<')
	if j < 0 {
		return 0, false
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(rest[:j])))
	return v, err == nil
}

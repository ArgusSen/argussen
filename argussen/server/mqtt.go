package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

// Small MQTT 3.1.1 client (QoS 0 only) – enough for Home Assistant: publish, subscribe, last will and ping.
// Keeps ArgusSen free of external dependencies besides SQLite.

type MQTTConfig struct {
	Host     string `json:"host"` // e.g. 192.168.1.5:1883
	User     string `json:"user"`
	Password string `json:"password"`
	Prefix   string `json:"discovery_prefix"` // standard "homeassistant"
}

type MQTT struct {
	cfg       MQTTConfig
	clientID  string
	willTopic string
	willMsg   string
	subs      []string
	onConnect func()
	onMsg     func(topic string, payload []byte)

	mu   sync.Mutex
	conn net.Conn
}

func mqttString(b []byte, s string) []byte {
	b = binary.BigEndian.AppendUint16(b, uint16(len(s)))
	return append(b, s...)
}

func mqttPacket(typ byte, body []byte) []byte {
	out := []byte{typ}
	n := len(body)
	for {
		d := byte(n % 128)
		n /= 128
		if n > 0 {
			d |= 0x80
		}
		out = append(out, d)
		if n == 0 {
			break
		}
	}
	return append(out, body...)
}

func readPacket(r *bufio.Reader) (byte, []byte, error) {
	typ, err := r.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	n, mul := 0, 1
	for i := 0; ; i++ {
		d, err := r.ReadByte()
		if err != nil {
			return 0, nil, err
		}
		n += int(d&0x7f) * mul
		mul *= 128
		if d&0x80 == 0 {
			break
		}
		if i == 3 {
			return 0, nil, errors.New("invalid packet length")
		}
	}
	if n > 4<<20 {
		return 0, nil, fmt.Errorf("packet too large (%d)", n)
	}
	body := make([]byte, n)
	_, err = io.ReadFull(r, body)
	return typ, body, err
}

func (m *MQTT) write(p []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.conn == nil {
		return errors.New("not connected")
	}
	m.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	_, err := m.conn.Write(p)
	if err != nil {
		m.conn.Close()
	}
	return err
}

// Publish sends with QoS 0. Errors are ignored by the caller: Run restores the connection.
func (m *MQTT) Publish(topic string, payload []byte, retain bool) error {
	b := mqttString(nil, topic)
	b = append(b, payload...)
	typ := byte(0x30)
	if retain {
		typ |= 1
	}
	return m.write(mqttPacket(typ, b))
}

func (m *MQTT) Connected() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.conn != nil
}

func (m *MQTT) Run(ctx context.Context) {
	wait := 5 * time.Second
	lastErr := ""
	for ctx.Err() == nil {
		start := time.Now()
		err := m.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > time.Minute {
			wait = 5 * time.Second
		}
		if e := err.Error(); e != lastErr {
			log.Printf("MQTT: connection lost: %v (retrying)", err)
			lastErr = e
		}
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
		wait = min(wait*2, 2*time.Minute)
	}
}

func (m *MQTT) session(ctx context.Context) error {
	c, err := net.DialTimeout("tcp", m.cfg.Host, 10*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	// CONNECT: clean session, last will (retain), username and password, keepalive 30 s
	flags := byte(0x02 | 0x04 | 0x20)
	if m.cfg.User != "" {
		flags |= 0x80
	}
	if m.cfg.Password != "" {
		flags |= 0x40
	}
	b := mqttString(nil, "MQTT")
	b = append(b, 4, flags, 0, 30)
	b = mqttString(b, m.clientID)
	b = mqttString(b, m.willTopic)
	b = mqttString(b, m.willMsg)
	if m.cfg.User != "" {
		b = mqttString(b, m.cfg.User)
	}
	if m.cfg.Password != "" {
		b = mqttString(b, m.cfg.Password)
	}
	c.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.Write(mqttPacket(0x10, b)); err != nil {
		return err
	}
	r := bufio.NewReader(c)
	typ, body, err := readPacket(r)
	if err != nil {
		return err
	}
	if typ != 0x20 || len(body) < 2 {
		return errors.New("unexpected answer to CONNECT")
	}
	if body[1] != 0 {
		return fmt.Errorf("refused by the broker (code %d – wrong login?)", body[1])
	}
	c.SetDeadline(time.Time{})
	m.mu.Lock()
	m.conn = c
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.conn = nil
		m.mu.Unlock()
	}()
	log.Printf("MQTT: connected to %s", m.cfg.Host)

	if len(m.subs) > 0 {
		s := binary.BigEndian.AppendUint16(nil, 1)
		for _, t := range m.subs {
			s = mqttString(s, t)
			s = append(s, 0)
		}
		if err := m.write(mqttPacket(0x82, s)); err != nil {
			return err
		}
	}
	if m.onConnect != nil {
		go m.onConnect()
	}

	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				c.Close()
				return
			case <-t.C:
				m.write([]byte{0xC0, 0})
			}
		}
	}()
	for {
		c.SetReadDeadline(time.Now().Add(75 * time.Second)) // ping every 20 s → an answer comes
		typ, body, err := readPacket(r)
		if err != nil {
			return err
		}
		if typ&0xF0 == 0x30 && len(body) >= 2 {
			n := int(binary.BigEndian.Uint16(body))
			if 2+n > len(body) {
				continue
			}
			topic := string(body[2 : 2+n])
			payload := body[2+n:]
			if (typ>>1)&3 > 0 { // QoS>0 should not come (we subscribe with 0), but skip the packet id
				if len(payload) < 2 {
					continue
				}
				payload = payload[2:]
			}
			if m.onMsg != nil {
				m.onMsg(topic, payload)
			}
		}
	}
}

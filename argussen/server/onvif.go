package main

// A small ONVIF client – only what is needed to add a camera: find its RTSP addresses (main stream = the profile with
// the highest resolution, sub stream = the lowest) and its model. ONVIF is only used when a camera is added or tested;
// while recording, the camera is an ordinary RTSP camera.
// Login: WS-Security UsernameToken (password digest), with the camera's own clock (many cameras have the wrong time),
// and HTTP digest as a fallback for cameras that ask for it.

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type onvifClient struct {
	xaddr      string // device service, e.g. http://192.168.1.20/onvif/device_service
	user, pass string
	offset     time.Duration // camera clock − our clock
	http       *http.Client
}

func newOnvif(host string, port int, user, pass string) *onvifClient {
	if port == 0 {
		port = 80
	}
	return &onvifClient{xaddr: "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/onvif/device_service",
		user: user, pass: pass, http: &http.Client{Timeout: 8 * time.Second}}
}

func (o *onvifClient) security() string {
	if o.user == "" {
		return ""
	}
	nonce := make([]byte, 16)
	rand.Read(nonce)
	created := time.Now().Add(o.offset).UTC().Format("2006-01-02T15:04:05.000Z")
	h := sha1.New()
	h.Write(nonce)
	h.Write([]byte(created))
	h.Write([]byte(o.pass))
	var b strings.Builder
	b.WriteString(`<s:Header><Security s:mustUnderstand="1" xmlns="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd"><UsernameToken><Username>`)
	xml.EscapeText(&b, []byte(o.user))
	b.WriteString(`</Username><Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest">`)
	b.WriteString(base64.StdEncoding.EncodeToString(h.Sum(nil)))
	b.WriteString(`</Password><Nonce EncodingType="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-soap-message-security-1.0#Base64Binary">`)
	b.WriteString(base64.StdEncoding.EncodeToString(nonce))
	b.WriteString(`</Nonce><Created xmlns="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd">`)
	b.WriteString(created)
	b.WriteString(`</Created></UsernameToken></Security></s:Header>`)
	return b.String()
}

// call sends one SOAP request and decodes the body into out.
func (o *onvifClient) call(ctx context.Context, addr, body string, auth bool, out any) error {
	env := `<?xml version="1.0" encoding="UTF-8"?><s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" ` +
		`xmlns:tds="http://www.onvif.org/ver10/device/wsdl" xmlns:trt="http://www.onvif.org/ver10/media/wsdl" ` +
		`xmlns:tt="http://www.onvif.org/ver10/schema">`
	if auth {
		env += o.security()
	}
	env += `<s:Body>` + body + `</s:Body></s:Envelope>`
	do := func(authz string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, "POST", addr, strings.NewReader(env))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/soap+xml; charset=utf-8")
		if authz != "" {
			req.Header.Set("Authorization", authz)
		}
		return o.http.Do(req)
	}
	resp, err := do("")
	if err != nil {
		return errors.New("the camera is not responding")
	}
	if resp.StatusCode == 401 && auth && o.user != "" {
		if d := digestAuth(resp.Header.Get("WWW-Authenticate"), "POST", addr, o.user, o.pass); d != "" {
			resp.Body.Close()
			if resp, err = do(d); err != nil {
				return errors.New("the camera is not responding")
			}
		}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode == 401 || bytes.Contains(data, []byte("NotAuthorized")) {
		return errors.New("wrong username or password")
	}
	if resp.StatusCode != 200 {
		var f struct {
			Reason string `xml:"Body>Fault>Reason>Text"`
		}
		xml.Unmarshal(data, &f)
		if f.Reason != "" {
			return fmt.Errorf("ONVIF: %s", f.Reason)
		}
		return fmt.Errorf("ONVIF: HTTP %d", resp.StatusCode)
	}
	return xml.Unmarshal(data, out)
}

// digestAuth builds an HTTP digest answer (MD5, qop=auth) to a WWW-Authenticate challenge ("" if it is not digest).
func digestAuth(challenge, method, uri, user, pass string) string {
	if !strings.HasPrefix(strings.ToLower(challenge), "digest ") {
		return ""
	}
	p := map[string]string{}
	for _, part := range strings.Split(challenge[7:], ",") {
		if k, v, ok := strings.Cut(strings.TrimSpace(part), "="); ok {
			p[strings.ToLower(k)] = strings.Trim(v, `"`)
		}
	}
	if u, err := url.Parse(uri); err == nil {
		uri = u.RequestURI()
	}
	md := func(s string) string { h := md5.Sum([]byte(s)); return hex.EncodeToString(h[:]) }
	ha1, ha2 := md(user+":"+p["realm"]+":"+pass), md(method+":"+uri)
	cb := make([]byte, 8)
	rand.Read(cb)
	cnonce := hex.EncodeToString(cb)
	resp := md(ha1 + ":" + p["nonce"] + ":" + ha2)
	qop := ""
	if strings.Contains(p["qop"], "auth") {
		qop = "auth"
		resp = md(ha1 + ":" + p["nonce"] + ":00000001:" + cnonce + ":auth:" + ha2)
	}
	s := fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s"`, user, p["realm"], p["nonce"], uri, resp)
	if qop != "" {
		s += `, qop=auth, nc=00000001, cnonce="` + cnonce + `"`
	}
	if p["opaque"] != "" {
		s += `, opaque="` + p["opaque"] + `"`
	}
	return s
}

// syncClock reads the camera's clock (no login needed), so the login is accepted even if the camera's time is wrong.
func (o *onvifClient) syncClock(ctx context.Context) error {
	var r struct {
		T struct {
			Y  int `xml:"Date>Year"`
			Mo int `xml:"Date>Month"`
			D  int `xml:"Date>Day"`
			H  int `xml:"Time>Hour"`
			Mi int `xml:"Time>Minute"`
			S  int `xml:"Time>Second"`
		} `xml:"Body>GetSystemDateAndTimeResponse>SystemDateAndTime>UTCDateTime"`
	}
	if err := o.call(ctx, o.xaddr, `<tds:GetSystemDateAndTime/>`, false, &r); err != nil {
		return err
	}
	if r.T.Y > 2000 {
		cam := time.Date(r.T.Y, time.Month(r.T.Mo), r.T.D, r.T.H, r.T.Mi, r.T.S, 0, time.UTC)
		o.offset = cam.Sub(time.Now())
	}
	return nil
}

type onvifInfo struct {
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
	Main         string `json:"main"` // RTSP address without login
	Sub          string `json:"sub"`
	MainSize     string `json:"main_size"`
	SubSize      string `json:"sub_size"`
	MainCodec    string `json:"main_codec"`
	SubCodec     string `json:"sub_codec"`
}

// onvifStreams logs in to the camera and finds its main and sub stream.
func onvifStreams(ctx context.Context, host string, port int, user, pass string) (*onvifInfo, error) {
	o := newOnvif(host, port, user, pass)
	if err := o.syncClock(ctx); err != nil {
		return nil, fmt.Errorf("ONVIF on port %d: %v", max(port, 80), err)
	}
	info := &onvifInfo{}
	var dev struct {
		Manufacturer string `xml:"Body>GetDeviceInformationResponse>Manufacturer"`
		Model        string `xml:"Body>GetDeviceInformationResponse>Model"`
	}
	if err := o.call(ctx, o.xaddr, `<tds:GetDeviceInformation/>`, true, &dev); err != nil {
		return nil, err
	}
	info.Manufacturer, info.Model = strings.TrimSpace(dev.Manufacturer), strings.TrimSpace(dev.Model)
	if strings.EqualFold(info.Manufacturer, "Manufacturer") { // some cameras (e.g. Reolink) answer with a placeholder
		info.Manufacturer = ""
	}
	var caps struct {
		Media string `xml:"Body>GetCapabilitiesResponse>Capabilities>Media>XAddr"`
	}
	if err := o.call(ctx, o.xaddr, `<tds:GetCapabilities><tds:Category>Media</tds:Category></tds:GetCapabilities>`, true, &caps); err != nil {
		return nil, err
	}
	media := fixHost(strings.TrimSpace(caps.Media), host)
	if media == "" {
		return nil, errors.New("ONVIF: the camera has no media service")
	}
	var prof struct {
		Profiles []struct {
			Token string `xml:"token,attr"`
			Enc   string `xml:"VideoEncoderConfiguration>Encoding"`
			W     int    `xml:"VideoEncoderConfiguration>Resolution>Width"`
			H     int    `xml:"VideoEncoderConfiguration>Resolution>Height"`
		} `xml:"Body>GetProfilesResponse>Profiles"`
	}
	if err := o.call(ctx, media, `<trt:GetProfiles/>`, true, &prof); err != nil {
		return nil, err
	}
	ps := prof.Profiles[:0]
	for _, p := range prof.Profiles {
		if p.W > 0 {
			ps = append(ps, p)
		}
	}
	if len(ps) == 0 {
		return nil, errors.New("ONVIF: the camera has no video profiles")
	}
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].W*ps[i].H > ps[j].W*ps[j].H })
	main, sub := ps[0], ps[len(ps)-1]
	uri := func(token string) (string, error) {
		var r struct {
			URI string `xml:"Body>GetStreamUriResponse>MediaUri>Uri"`
		}
		body := `<trt:GetStreamUri><trt:StreamSetup><tt:Stream>RTP-Unicast</tt:Stream><tt:Transport><tt:Protocol>RTSP</tt:Protocol></tt:Transport></trt:StreamSetup><trt:ProfileToken>` +
			xmlEscape(token) + `</trt:ProfileToken></trt:GetStreamUri>`
		if err := o.call(ctx, media, body, true, &r); err != nil {
			return "", err
		}
		return fixHost(strings.TrimSpace(r.URI), host), nil
	}
	var err error
	if info.Main, err = uri(main.Token); err != nil {
		return nil, err
	}
	info.MainSize, info.MainCodec = fmt.Sprintf("%dx%d", main.W, main.H), main.Enc
	if len(ps) > 1 {
		if info.Sub, err = uri(sub.Token); err != nil {
			return nil, err
		}
		info.SubSize, info.SubCodec = fmt.Sprintf("%dx%d", sub.W, sub.H), sub.Enc
	}
	return info, nil
}

func xmlEscape(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

// fixHost puts the address the camera was reached on into an address it reports (cameras behind NAT, or reporting
// another of their addresses, would otherwise give an address that cannot be reached). Login data is removed.
func fixHost(addr, host string) string {
	u, err := url.Parse(addr)
	if err != nil || u.Host == "" {
		return addr
	}
	if p := u.Port(); p != "" {
		u.Host = net.JoinHostPort(host, p)
	} else {
		u.Host = host
	}
	u.User = nil
	return u.String()
}

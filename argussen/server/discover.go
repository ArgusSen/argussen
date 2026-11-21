package main

// Searching the local network for cameras (Settings → Cameras → New camera → Search the network).
// 1. ONVIF WS-Discovery: a probe is sent to every address in the network (unicast – it also works from inside a
//    container, where multicast does not reach the network) and to the multicast group. Cameras answer with their
//    ONVIF address, model and name.
// 2. A quick port check (RTSP 554, Reolink 9000) finds cameras that do not answer ONVIF.
// Only the network ArgusSen's machine is on is searched (a /24 around its own address), unless another range is given.

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type foundCam struct {
	Host      string `json:"host"`
	Brand     string `json:"brand"`      // e.g. "Reolink" (a guess, "" if unknown)
	Model     string `json:"model"`      // from ONVIF
	Name      string `json:"name"`       // from ONVIF
	Kind      string `json:"kind"`       // suggested type: reolink, onvif or rtsp
	OnvifPort int    `json:"onvif_port"` // 0 = no ONVIF answer
	RTSP      bool   `json:"rtsp"`       // port 554 open
	Added     string `json:"added"`      // the name of the camera, if it is already in ArgusSen
}

// localNetworks: the IPv4 networks of the machine (as Home Assistant app: the host's, asked via the Supervisor).
func localNetworks(ingress bool) []netip.Prefix {
	var out []netip.Prefix
	add := func(s string) {
		if p, err := netip.ParsePrefix(s); err == nil && p.Addr().Is4() && p.Addr().IsPrivate() {
			out = append(out, p)
		}
	}
	if ingress {
		if nets, err := supervisorNetworks(); err == nil {
			for _, n := range nets {
				add(n)
			}
			return out
		}
	}
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 || strings.HasPrefix(i.Name, "docker") || strings.HasPrefix(i.Name, "hassio") || strings.HasPrefix(i.Name, "veth") {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			add(a.String())
		}
	}
	return out
}

func supervisorNetworks() ([]string, error) {
	tok := os.Getenv("SUPERVISOR_TOKEN")
	if tok == "" {
		return nil, errors.New("no Supervisor")
	}
	req, _ := http.NewRequest("GET", "http://supervisor/network/info", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var r struct {
		Data struct {
			Interfaces []struct {
				Enabled, Connected bool
				IPv4               struct {
					Address []string `json:"address"`
				} `json:"ipv4"`
			} `json:"interfaces"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	var out []string
	for _, i := range r.Data.Interfaces {
		if i.Enabled && i.Connected {
			out = append(out, i.IPv4.Address...)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no network")
	}
	return out, nil
}

// searchRange: the addresses to search – at most a /22 (1024 addresses). A bigger network is narrowed to the /24
// around the machine's own address.
func searchRange(p netip.Prefix) []netip.Addr {
	if p.Bits() < 24 {
		p, _ = p.Addr().Prefix(24)
	}
	if p.Bits() < 22 {
		return nil
	}
	p = p.Masked()
	var out []netip.Addr
	for a := p.Addr().Next(); p.Contains(a); a = a.Next() {
		if !p.Contains(a.Next()) { // the broadcast address
			break
		}
		out = append(out, a)
	}
	return out
}

const wsdProbe = `<?xml version="1.0" encoding="UTF-8"?><e:Envelope xmlns:e="http://www.w3.org/2003/05/soap-envelope" ` +
	`xmlns:w="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery" ` +
	`xmlns:dn="http://www.onvif.org/ver10/network/wsdl"><e:Header><w:MessageID>uuid:%s</w:MessageID>` +
	`<w:To e:mustUnderstand="true">urn:schemas-xmlsoap-org:ws:2005:04:discovery</w:To>` +
	`<w:Action e:mustUnderstand="true">http://schemas.xmlsoap.org/ws/2005/04/discovery/Probe</w:Action></e:Header>` +
	`<e:Body><d:Probe><d:Types>dn:NetworkVideoTransmitter</d:Types></d:Probe></e:Body></e:Envelope>`

var (
	xaddrRe = regexp.MustCompile(`XAddrs>([^<]+)<`)
	scopeRe = regexp.MustCompile(`onvif://www\.onvif\.org/(name|hardware|mfr|manufacturer)/([^\s<]+)`)
)

type wsdAnswer struct {
	port             int
	model, name, mfr string
}

// wsDiscover sends ONVIF probes and collects the answers for wait.
func wsDiscover(ctx context.Context, addrs []netip.Addr, wait time.Duration) map[string]wsdAnswer {
	out := map[string]wsdAnswer{}
	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return out
	}
	defer conn.Close()
	id := make([]byte, 16)
	rand.Read(id)
	msg := []byte(fmt.Sprintf(wsdProbe, fmt.Sprintf("%x-%x-%x-%x-%x", id[0:4], id[4:6], id[6:8], id[8:10], id[10:])))
	conn.WriteToUDP(msg, &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 3702})
	go func() {
		// twice: UDP answers get lost (especially over wifi)
		for round := 0; round < 2; round++ {
			if round > 0 {
				time.Sleep(1500 * time.Millisecond)
				conn.WriteToUDP(msg, &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 3702})
			}
			for _, a := range addrs {
				if ctx.Err() != nil {
					return
				}
				conn.WriteToUDP(msg, &net.UDPAddr{IP: a.AsSlice(), Port: 3702})
				time.Sleep(2 * time.Millisecond)
			}
		}
	}()
	deadline := time.Now().Add(wait)
	buf := make([]byte, 65535)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		body := string(buf[:n])
		a := wsdAnswer{}
		if m := xaddrRe.FindStringSubmatch(body); m != nil {
			for _, x := range strings.Fields(m[1]) {
				if u, err := url.Parse(x); err == nil && u.Hostname() == from.IP.String() {
					a.port, _ = strconv.Atoi(u.Port())
					if a.port == 0 {
						a.port = 80
					}
				}
			}
			if a.port == 0 {
				if u, err := url.Parse(strings.Fields(m[1])[0]); err == nil {
					if a.port, _ = strconv.Atoi(u.Port()); a.port == 0 {
						a.port = 80
					}
				}
			}
		}
		for _, s := range scopeRe.FindAllStringSubmatch(body, -1) {
			v, _ := url.PathUnescape(s[2])
			switch s[1] {
			case "hardware":
				a.model = v
			case "name":
				if a.name == "" || len(v) > len(a.name) {
					a.name = v
				}
			default:
				a.mfr = v
			}
		}
		out[from.IP.String()] = a
	}
	return out
}

// openPorts checks which of the ports answer on each address.
func openPorts(ctx context.Context, addrs []netip.Addr, ports []int) map[string]map[int]bool {
	out := map[string]map[int]bool{}
	var mu sync.Mutex
	sem := make(chan struct{}, 128)
	var wg sync.WaitGroup
	d := net.Dialer{Timeout: 700 * time.Millisecond}
	for _, a := range addrs {
		for _, p := range ports {
			wg.Add(1)
			sem <- struct{}{}
			go func(a netip.Addr, p int) {
				defer func() { <-sem; wg.Done() }()
				c, err := d.DialContext(ctx, "tcp", netip.AddrPortFrom(a, uint16(p)).String())
				if err != nil {
					return
				}
				c.Close()
				mu.Lock()
				if out[a.String()] == nil {
					out[a.String()] = map[int]bool{}
				}
				out[a.String()][p] = true
				mu.Unlock()
			}(a, p)
		}
	}
	wg.Wait()
	return out
}

// reolinkModel: Reolink's model names (RLC-, E1, D340 …) – Reolink cameras also have port 9000 open.
var reolinkModel = regexp.MustCompile(`(?i)^(RLC|RLN|E1|E3|D3\d\d|CX|Duo|TrackMix|Argus|Go|Lumus|B\d{3}|W\d{3}|IPC)`)

// probeOnvif: the port where the host answers ONVIF without a login (GetSystemDateAndTime), 0 if none.
func probeOnvif(ctx context.Context, host string) int {
	for _, port := range []int{80, 8000, 8080, 2020, 8899} {
		c, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := newOnvif(host, port, "", "").syncClock(c)
		cancel()
		if err == nil {
			return port
		}
	}
	return 0
}

// brandFromModel guesses the brand from well-known model names ("" if unknown).
func brandFromModel(model string) string {
	m := strings.ToUpper(model)
	for _, b := range []struct{ prefix, brand string }{
		{"DCS-", "D-Link"}, {"DS-2", "Hikvision"}, {"IPC-HDW", "Dahua"}, {"IPC-HFW", "Dahua"}, {"IPC-HDBW", "Dahua"},
		{"AXIS", "Axis"}, {"TAPO", "TP-Link"}, {"VIGI", "TP-Link"}, {"FI9", "Foscam"}, {"IPC32", "Uniview"},
	} {
		if strings.HasPrefix(m, b.prefix) {
			return b.brand
		}
	}
	return ""
}

func discoverCameras(ctx context.Context, nets []netip.Prefix) ([]foundCam, error) {
	var addrs []netip.Addr
	seen := map[netip.Addr]bool{}
	for _, n := range nets {
		for _, a := range searchRange(n) {
			if !seen[a] {
				seen[a] = true
				addrs = append(addrs, a)
			}
		}
	}
	if len(addrs) == 0 {
		return nil, errors.New("no local network found")
	}
	var wsd map[string]wsdAnswer
	var ports map[string]map[int]bool
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); wsd = wsDiscover(ctx, addrs, 4*time.Second) }()
	go func() { defer wg.Done(); ports = openPorts(ctx, addrs, []int{554, 9000}) }()
	wg.Wait()
	byHost := map[string]*foundCam{}
	for ip, a := range wsd {
		byHost[ip] = &foundCam{Host: ip, Model: a.model, Name: a.name, Brand: a.mfr, OnvifPort: a.port, Kind: "onvif"}
	}
	for ip, p := range ports {
		// port 9000 alone is not enough (e.g. Google Home/Chromecast uses it too) – a camera answers ONVIF or RTSP
		if !p[554] && byHost[ip] == nil {
			continue
		}
		f := byHost[ip]
		if f == nil {
			f = &foundCam{Host: ip, Kind: "rtsp"}
			byHost[ip] = f
		}
		f.RTSP = p[554]
		if p[9000] && (f.Model == "" || reolinkModel.MatchString(f.Model)) {
			f.Brand, f.Kind = "Reolink", "reolink"
		}
	}
	// RTSP but no discovery answer: ask the usual ONVIF addresses directly (a lost answer, or discovery switched off)
	var pwg sync.WaitGroup
	var pmu sync.Mutex
	for _, f := range byHost {
		if f.Kind != "rtsp" {
			continue
		}
		pwg.Add(1)
		go func(f *foundCam) {
			defer pwg.Done()
			if port := probeOnvif(ctx, f.Host); port != 0 {
				pmu.Lock()
				f.Kind, f.OnvifPort = "onvif", port
				pmu.Unlock()
			}
		}(f)
	}
	pwg.Wait()
	out := []foundCam{}
	for _, f := range byHost {
		if f.Brand == "" && f.Name != "" && !strings.EqualFold(f.Name, f.Model) && !strings.HasPrefix(strings.ToUpper(f.Name), "IPC") {
			f.Brand = f.Name
		}
		// "D-Link DCS-4602EV" → "D-Link"
		if f.Model != "" && len(f.Brand) > len(f.Model) && strings.HasSuffix(strings.ToUpper(f.Brand), " "+strings.ToUpper(f.Model)) {
			f.Brand = strings.TrimSpace(f.Brand[:len(f.Brand)-len(f.Model)])
		}
		if f.Brand == "" {
			f.Brand = brandFromModel(f.Model)
		}
		for _, c := range cams.All() {
			if c.Host == f.Host {
				f.Added = c.Name
			}
		}
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := netip.ParseAddr(out[i].Host)
		b, _ := netip.ParseAddr(out[j].Host)
		return a.Less(b)
	})
	return out, nil
}

// GET /api/admin/cameras/discover[?range=192.168.2.0/24]
func (s *Server) discoverCams(w http.ResponseWriter, r *http.Request) {
	var nets []netip.Prefix
	if v := strings.TrimSpace(r.URL.Query().Get("range")); v != "" {
		p, err := netip.ParsePrefix(v)
		if err != nil || !p.Addr().Is4() || p.Bits() < 22 {
			jsonErr(w, 400, "give a network such as 192.168.1.0/24 (at most /22)")
			return
		}
		nets = []netip.Prefix{p}
	} else {
		nets = localNetworks(s.cfg.Ingress)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	found, err := discoverCameras(ctx, nets)
	if err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	var searched []string
	for _, n := range nets {
		if n.Bits() < 24 {
			n, _ = n.Addr().Prefix(24)
		}
		searched = append(searched, n.Masked().String())
	}
	writeJSON(w, map[string]any{"cameras": found, "searched": searched})
}

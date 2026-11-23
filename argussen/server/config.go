package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type Camera struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Main    string `json:"main_path"`
	Sub     string `json:"sub_path"`
	Enabled bool   `json:"enabled"`
	// MainProto: the method(s) for the main stream, in order of preference: "bc" (Reolink's own protocol, port 9000),
	// "flv" (Reolink's HTTPS-FLV, port 443) or "rtsp". Some cameras lose RTSP packets ("RTP: bad cseq"); bc and FLV do not.
	MainProto string `json:"main_proto"` // "rtsp", "flv" or "bc" – or a list such as "bc,flv,rtsp" (see failover.go)
	BCPort    int    `json:"bc_port"`    // port for "bc" (default 9000)
	SubProto  string `json:"sub_proto"`  // like MainProto, for the sub stream
	Rotate    int    `json:"rotate"`
	HomeOff   bool   `json:"home_off"`
	HAName    string `json:"ha_name"`  // name in Home Assistant ("Camera <ha_name>")
	HAArea    string `json:"ha_area"`  // suggested area in Home Assistant
	Optional  bool   `json:"optional"` // switched on/off as needed: off is not an error
	// Kind: "reolink" (default – bc/flv/rtsp with Reolink's paths), "onvif" (the RTSP addresses were found via ONVIF)
	// or "rtsp" (addresses given by hand, e.g. from a brand preset). onvif and rtsp always use RTSP.
	Kind      string `json:"kind"`
	OnvifPort int    `json:"onvif_port"` // ONVIF port (kind onvif; default 80)

	user, pass string
}

func (c *Camera) reolink() bool { return c.Kind == "" || c.Kind == "reolink" }

var hostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)

// validHost: a camera address must be a plain host name or IP address (it is put into URLs and ffmpeg arguments).
func validHost(h string) bool {
	if _, err := netip.ParseAddr(h); err == nil {
		return true
	}
	return len(h) <= 253 && hostRe.MatchString(h)
}

// validStreamPath: a stream is a path (with an optional query) or an rtsp:// or rtsps:// address – never another
// scheme (ffmpeg would otherwise read e.g. local files).
func validStreamPath(p string) bool {
	if p == "" {
		return true
	}
	if strings.ContainsAny(p, " \t\r\n@") {
		return false
	}
	if !strings.Contains(p, "://") {
		colon, sep := strings.Index(p, ":"), strings.IndexAny(p, "/?")
		return colon < 0 || (sep >= 0 && sep < colon) // "x:…" before any / would look like a scheme
	}
	u, err := url.Parse(p)
	return err == nil && (u.Scheme == "rtsp" || u.Scheme == "rtsps") && u.User == nil
}

// urlFor: the address of a stream with a given method ("flv" or "rtsp"; "bc" does not use a URL).
// main_path/sub_path is a path (with an optional ?query) on the camera's RTSP port, or a whole rtsp:// or rtsps://
// address (its host is replaced by the camera's address, so changing the address is enough).
func (c *Camera) urlFor(stream, proto string) string {
	if proto == "flv" {
		q := url.Values{"port": {"1935"}, "app": {"bcs"}, "stream": {"channel0_" + stream + ".bcs"}, "user": {c.user}, "password": {c.pass}}
		return "https://" + c.Host + "/flv?" + q.Encode()
	}
	p := c.Sub
	if stream == "main" {
		p = c.Main
	}
	var u *url.URL
	if strings.Contains(p, "://") {
		if pu, err := url.Parse(p); err == nil && (pu.Scheme == "rtsp" || pu.Scheme == "rtsps") {
			u = pu
			if port := u.Port(); port != "" {
				u.Host = net.JoinHostPort(c.Host, port)
			} else {
				u.Host = c.Host
			}
		}
	}
	if u == nil {
		path, q, _ := strings.Cut(p, "?")
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		u = &url.URL{Scheme: "rtsp", Host: net.JoinHostPort(c.Host, strconv.Itoa(c.Port)), Path: path, RawQuery: q}
	}
	u.User = nil
	if c.user != "" {
		u.User = url.UserPassword(c.user, c.pass)
	}
	return u.String()
}

type Config struct {
	Storage           string            `json:"storage"`    // recording folder (e.g. on a NAS)
	Data              string            `json:"data"`       // local folder (database)
	Listen            string            `json:"listen"`     // e.g. ":8080"
	BlueOnyx          string            `json:"blueonyx"`   // AI server (CodeProject.AI API, e.g. Blue Onyx): http://192.168.1.10:32168 – empty = no AI
	SubDays           int               `json:"sub_days"`   // 24/7 sub stream
	MainHours         int               `json:"main_hours"` // full resolution without an event
	EventDays         int               `json:"event_days"` // events + full resolution around them
	Labels            map[string]string `json:"labels"`     // AI server label -> shown name
	MinConf           float64           `json:"min_confidence"`
	Motion            MotionConfig      `json:"motion"`
	PreSec            int               `json:"pre_seconds"`  // keep full resolution this long before an event
	PostSec           int               `json:"post_seconds"` // … and after
	Cameras           []*Camera         `json:"cameras"`
	PreferRetryMin    int               `json:"prefer_retry_min"`     // on a fallback method: try the preferred one every N minutes (default 30)
	WebDir            string            `json:"web_dir"`              // the web version of the app – shown ONLY on the local network
	MQTT              *MQTTConfig       `json:"mqtt"`                 // Home Assistant via MQTT discovery (left out = off)
	StorageMount      string            `json:"storage_mount"`        // network drive mount point: if it is missing, recording falls back/pauses (storagechoice.go, store.go)
	FallbackStorage   string            `json:"fallback_storage"`     // local folder recorded to while the NAS is gone (store.go)
	FallbackMinFreeGB float64           `json:"fallback_min_free_gb"` // keep this much free locally (default 3 GB)
	MinFreeGB         float64           `json:"min_free_gb"`          // keep this much free on the main storage (default: diskguard.go)
	PreviousStorage   []string          `json:"-"`                    // earlier storage places (storagechoice.go)
	PublicHosts       []string          `json:"public_hosts"`         // host names (or parts of them) that mean "from outside" (e.g. a tunnel's domain) – the web version is never shown there
	Language          string            `json:"language"`             // en, da, de, fr, sv, nb, nl: what "Automatic" means outside Home Assistant (language.go) – default en
	Timezone          string            `json:"timezone"`             // e.g. "Europe/Copenhagen"; empty = the machine's own (TZ)
	WebEverywhere     bool              `json:"web_everywhere"`       // standalone server: show the web version also through a reverse proxy/tunnel (login is always needed)
	TrustedProxies    []string          `json:"trusted_proxies"`      // reverse proxies (IPs or networks) whose X-Forwarded-For gives the client's address
	Standalone        bool              `json:"standalone"`           // the standalone server (Docker): retention and the storage screen are set in ArgusSen like in Home Assistant
	Ingress           bool              `json:"ingress"`              // runs as a Home Assistant app: login through HA's ingress headers (see ingress.go)
}

type MotionConfig struct {
	FPS        int     `json:"fps"`
	Threshold  float64 `json:"threshold"` // brightness difference per block (0-255)
	MinArea    float64 `json:"min_area"`  // share of blocks that must change
	MaxArea    float64 `json:"max_area"`  // above this = lighting change, ignored
	HoldSec    int     `json:"hold_seconds"`
	AIInterval float64 `json:"ai_interval_seconds"`
}

func loadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := &Config{
		Storage: "/mnt/nas", Data: "/var/lib/argus", Listen: ":8080",
		BlueOnyx: "",
		WebDir:   "/opt/argus/web", SubDays: 14, MainHours: 24, EventDays: 30, MinConf: 0.5, PreSec: 10, PostSec: 20,
		Labels: map[string]string{"person": "person", "car": "car", "truck": "van", "bus": "van", "bicycle": "bicycle"},
		Motion: MotionConfig{FPS: 5, Threshold: 18, MinArea: 0.003, MaxArea: 0.5, HoldSec: 8, AIInterval: 1},
	}
	if err := json.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for _, cam := range c.Cameras {
		if cam.Port == 0 {
			cam.Port = 554
		}
		normalizeCam(cam)
	}
	return c, nil
}

func (c *Camera) bcPort() int {
	if c.BCPort > 0 {
		return c.BCPort
	}
	return 9000
}

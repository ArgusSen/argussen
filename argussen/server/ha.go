package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// Home Assistant via MQTT discovery. Per camera one device with: camera (still image every 10 s), motion,
// person/car/van/bicycle (on while an event with the object is ongoing), connection, latest event
// (time + objects) and the image of the latest event. Plus the device "ArgusSen" with the switch Home mode.
// Topics: argussen/status (online/offline), argussen/<camera>/…, argussen/home(/set).

const snapshotEvery = 10 * time.Second

type haBridge struct {
	s      *Server
	mq     *MQTT
	prefix string
	events chan Event
	access chan struct{} // new access (login/opened/refused) → send the access sensors again

	mu      sync.Mutex
	motion  map[string]string // last sent state per topic (sent only on change)
	thumbOf map[string]string
}

func startHA(ctx context.Context, s *Server) {
	c := s.cfg.MQTT
	if c == nil || c.Host == "" {
		return
	}
	if c.Prefix == "" {
		c.Prefix = "homeassistant"
	}
	h := &haBridge{s: s, prefix: c.Prefix, events: make(chan Event, 256), access: make(chan struct{}, 1), motion: map[string]string{}, thumbOf: map[string]string{}}
	h.mq = &MQTT{cfg: *c, clientID: "argussen", willTopic: mqttBase + "/status", willMsg: "offline",
		subs: []string{mqttBase + "/home/set", c.Prefix + "/status"}, onConnect: h.connected, onMsg: h.message}
	// OnEvent is called with the EventManager's lock held – must not block
	s.ev.OnEvent = func(e Event) {
		select {
		case h.events <- e:
		default:
		}
	}
	s.auth.OnAccess = func(Access) {
		select {
		case h.access <- struct{}{}:
		default:
		}
	}
	s.ha = h
	go h.mq.Run(ctx)
	go h.loop(ctx)
}

// images: whether camera images (still images and event thumbnails) are sent – Settings → General.
// They are not retained: the broker does not keep them, so a client that connects later cannot read old images.
func (h *haBridge) images() bool { return h.s.ev.settings.GetKV("ha_images") != "off" }

func (h *haBridge) pub(topic string, v any) {
	var b []byte
	switch x := v.(type) {
	case []byte:
		b = x
	case string:
		b = []byte(x)
	default:
		b, _ = json.Marshal(x)
	}
	h.mq.Publish(topic, b, true)
}

// pubState sends only if the value has changed (or force).
func (h *haBridge) pubState(topic, v string, force bool) {
	h.mu.Lock()
	same := h.motion[topic] == v
	h.motion[topic] = v
	h.mu.Unlock()
	if !same || force {
		h.pub(topic, v)
	}
}

func onOff(b bool) string {
	if b {
		return "ON"
	}
	return "OFF"
}

var haObjects = []struct{ label, name, icon string }{
	{"person", "Person", "mdi:walk"},
	{"car", "Car", "mdi:car"},
	{"van", "Van", "mdi:truck"},
	{"bicycle", "Bicycle", "mdi:bicycle"},
}

// mqttBase is the start of all ArgusSen MQTT topics (state, commands, images).
const mqttBase = "argussen"

func (h *haBridge) discovery() {
	h.cleanupLegacy()
	h.pub(h.prefix+"/device/argussen/config", map[string]any{
		"device":             map[string]any{"identifiers": []string{"argussen"}, "name": "ArgusSen", "manufacturer": "ArgusSen", "model": "NVR", "sw_version": version},
		"origin":             map[string]any{"name": "ArgusSen", "sw_version": version},
		"availability_topic": mqttBase + "/status",
		"components": withPrivateHAComponents(map[string]any{
			"home_mode": map[string]any{"platform": "switch", "unique_id": "argussen_home_mode", "name": hn("Home mode"),
				"icon": "mdi:home-account", "state_topic": mqttBase + "/home", "command_topic": mqttBase + "/home/set",
				"default_entity_id": "switch.argussen_home_mode"},
			"storage_low": map[string]any{"platform": "binary_sensor", "unique_id": "argussen_storage_low", "name": hn("Storage almost full"),
				"device_class": "problem", "state_topic": mqttBase + "/storage_low",
				"default_entity_id": "binary_sensor.argussen_storage_low"},
		}),
	})
	for _, cam := range cams.All() {
		if !cam.Enabled {
			continue
		}
		id := cam.ID
		name := cam.HAName
		if name == "" {
			name = cam.Name
		}
		t := mqttBase + "/" + id + "/"
		u := "argussen_" + id + "_"
		cmps := map[string]any{
			"camera": map[string]any{"platform": "camera", "unique_id": u + "camera", "name": nil, "topic": t + "snapshot",
				"default_entity_id": "camera.argussen_" + id},
			"motion": map[string]any{"platform": "binary_sensor", "unique_id": u + "motion", "name": hn("Motion"),
				"device_class": "motion", "state_topic": t + "motion", "default_entity_id": "binary_sensor.argussen_" + id + "_motion"},
			"connection": map[string]any{"platform": "binary_sensor", "unique_id": u + "connection", "name": hn("Connection"),
				"device_class": "connectivity", "entity_category": "diagnostic", "state_topic": t + "online",
				"default_entity_id": "binary_sensor.argussen_" + id + "_connection"},
			"latest_event": map[string]any{"platform": "sensor", "unique_id": u + "latest_event", "name": hn("Latest event"),
				"device_class": "timestamp", "icon": "mdi:cctv", "state_topic": t + "event", "value_template": "{{ value_json.start }}",
				"json_attributes_topic": t + "event", "default_entity_id": "sensor.argussen_" + id + "_latest_event"},
			"latest_event_image": map[string]any{"platform": "image", "unique_id": u + "latest_event_image", "name": hn("Latest event"),
				"image_topic": t + "event/thumb", "content_type": "image/jpeg", "default_entity_id": "image.argussen_" + id + "_latest_event"},
		}
		if !h.images() { // a component with only its platform is removed by Home Assistant
			cmps["camera"] = map[string]any{"platform": "camera"}
			cmps["latest_event_image"] = map[string]any{"platform": "image"}
		}
		for _, o := range haObjects {
			cmps[o.label] = map[string]any{"platform": "binary_sensor", "unique_id": u + o.label, "name": hn(o.name),
				"device_class": "motion", "icon": o.icon, "state_topic": t + o.label,
				"default_entity_id": "binary_sensor.argussen_" + id + "_" + o.label}
		}
		dev := map[string]any{"identifiers": []string{"argussen_" + id}, "name": hn("Camera") + " " + name, "manufacturer": "ArgusSen",
			"model": hn("Reolink camera"), "sw_version": version, "via_device": "argussen"}
		if cam.HAArea != "" {
			dev["suggested_area"] = cam.HAArea
		}
		h.pub(h.prefix+"/device/argussen_"+id+"/config", map[string]any{
			"device": dev, "origin": map[string]any{"name": "ArgusSen", "sw_version": version},
			"availability_topic": mqttBase + "/status", "components": cmps,
		})
	}
}

// cleanupLegacy removes (once) the devices from before 2026-11-20, which had Danish IDs under "argus"
// (homeassistant/device/argus[_<camera>]) and the retained images under argus/<camera>/.
func (h *haBridge) cleanupLegacy() {
	if h.s.ev.settings.GetKV("ha_legacy_removed") == "1" {
		return
	}
	h.mq.Publish(h.prefix+"/device/argus/config", nil, true)
	for _, c := range cams.All() {
		h.mq.Publish(h.prefix+"/device/argus_"+c.ID+"/config", nil, true)
		// the old topics (before 0.7.0) used Danish label names – they are emptied here
		for _, t := range []string{"snapshot", "event", "event/thumb", "motion", "online", "person", "bil", "varebil", "cykel"} {
			h.mq.Publish("argus/"+c.ID+"/"+t, nil, true)
		}
	}
	for _, t := range []string{"argus/status", "argus/home", "argus/adgang"} {
		h.mq.Publish(t, nil, true)
	}
	h.s.ev.settings.SetKV("ha_legacy_removed", "1")
	log.Printf("MQTT: old Home Assistant devices (argus) removed")
}

// connected: (re)send discovery and all states.
func (h *haBridge) connected() {
	// before 0.9.0 the images were retained on the broker: remove them once
	if h.s.ev.settings.GetKV("ha_images_unretained") != "1" {
		for _, id := range h.s.camIDs() {
			h.mq.Publish(mqttBase+"/"+id+"/snapshot", nil, true)
			h.mq.Publish(mqttBase+"/"+id+"/event/thumb", nil, true)
		}
		h.s.ev.settings.SetKV("ha_images_unretained", "1")
	}
	h.discovery()
	h.pub(mqttBase+"/status", "online")
	h.states(true)
	h.sendAccess()
	// latest event per camera (survives a restart of ArgusSen). The objects start as "off" – an event in the
	// database without an end may come from a restart in the middle of the event; ongoing ones update on the next AI hit.
	for _, id := range h.s.camIDs() {
		for _, o := range haObjects {
			h.pubState(mqttBase+"/"+id+"/"+o.label, "OFF", true)
		}
		if l, err := h.s.ev.ListEvents([]string{id}, time.Now().Add(time.Minute), 1); err == nil && len(l) > 0 {
			h.sendEvent(l[0], true, true)
		}
	}
}

func (h *haBridge) states(force bool) {
	h.pubState(mqttBase+"/home", onOff(h.s.ev.settings.Home()), force)
	h.pubState(mqttBase+"/storage_low", onOff(h.s.storageLow.Load()), force)
	for _, id := range h.s.camIDs() {
		rn := h.s.runner(id)
		if rn == nil {
			continue
		}
		st := rn.State()
		h.pubState(mqttBase+"/"+id+"/motion", onOff(st.Motion), force)
		h.pubState(mqttBase+"/"+id+"/online", onOff(st.SubOK), force)
	}
}

func (h *haBridge) message(topic string, p []byte) {
	v := strings.TrimSpace(string(p))
	switch {
	case topic == mqttBase+"/home/set":
		if v == "ON" || v == "OFF" {
			h.s.setMode(v == "ON", "Home Assistant")
			h.pubState(mqttBase+"/home", v, true)
		}
	case topic == h.prefix+"/status" && v == "online":
		log.Printf("MQTT: Home Assistant started – sending discovery again")
		go func() { time.Sleep(5 * time.Second); h.connected() }()
	}
}

func (h *haBridge) sendEvent(e Event, force, fromDB bool) {
	t := mqttBase + "/" + e.Cam + "/"
	open := e.End.IsZero()
	if fromDB && open && time.Since(e.Start) > 10*time.Minute {
		open = false // never closed (ArgusSen restarted in the middle of the event)
	}
	if !fromDB {
		for _, o := range haObjects {
			h.pubState(t+o.label, onOff(open && contains(e.Labels, o.label)), force)
		}
	}
	var end any
	if !open {
		end = e.End.Format(time.RFC3339)
	}
	labels := e.Labels
	if labels == nil {
		labels = []string{}
	}
	h.pub(t+"event", map[string]any{"id": e.ID, "start": e.Start.Format(time.RFC3339), "end": end, "ongoing": open,
		"objects": strings.Join(labels, ", "), "labels": labels, "score": int(e.Score*100 + 0.5), "protected": e.Keep})
	h.mu.Lock()
	changed := e.thumb != "" && h.thumbOf[e.Cam] != e.thumb
	if changed {
		h.thumbOf[e.Cam] = e.thumb
	}
	h.mu.Unlock()
	if (changed || (force && e.thumb != "")) && h.images() {
		if b, err := os.ReadFile(store.Find(e.thumb)); err == nil {
			h.mq.Publish(t+"event/thumb", b, false)
		}
	}
}

func (h *haBridge) loop(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	lastSnap := time.Time{}
	lastAccess := time.Now()
	for {
		select {
		case <-ctx.Done():
			if h.mq.Connected() {
				h.mq.Publish(mqttBase+"/status", []byte("offline"), true)
			}
			return
		case <-h.access:
			if h.mq.Connected() {
				h.sendAccess()
				lastAccess = time.Now()
			}
		case e := <-h.events:
			if h.mq.Connected() {
				h.sendEvent(e, false, false)
			}
		case <-tick.C:
			if !h.mq.Connected() {
				continue
			}
			h.states(false)
			if time.Since(lastAccess) > 5*time.Minute { // the 24-hour counters drop by themselves
				h.sendAccess()
				lastAccess = time.Now()
			}
			if time.Since(lastSnap) < snapshotEvery || !h.images() {
				continue
			}
			lastSnap = time.Now()
			for _, id := range h.s.camIDs() {
				rn := h.s.runner(id)
				if rn == nil {
					continue
				}
				f := rn.LastFrame()
				if f == nil || time.Since(f.At) > 30*time.Second {
					continue
				}
				if jpg, err := frameJPEG(f, 75); err == nil {
					h.mq.Publish(mqttBase+"/"+id+"/snapshot", jpg, false)
				}
			}
		}
	}
}

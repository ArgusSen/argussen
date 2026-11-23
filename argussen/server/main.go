package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

var version = "dev"

func main() {
	cfgPath := flag.String("config", "/etc/argus/argus.json", "configuration file")
	flag.Usage = func() { fmt.Fprintf(os.Stderr, "argus [-config file]          run the server\n%s\n", userHelp) }
	flag.Parse()
	log.SetFlags(0) // journald adds the timestamp itself

	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		log.Fatalf("configuration: %v", err)
	}
	loadPrivate(*cfgPath)
	if cfg.Timezone != "" {
		if l, err := time.LoadLocation(cfg.Timezone); err == nil {
			localTZ = l
		}
	}
	if cfg.Ingress { // Home Assistant app: the storage is chosen in ArgusSen (storagechoice.go)
		applyStorageChoice(cfg)
		if cfg.StorageMount == "" {
			os.MkdirAll(cfg.Storage, 0o755)
		}
	}
	if cfg.StorageMount != "" {
		// network drive: if it is missing, recording waits (watchStorage) instead of stopping
	} else if fi, err := os.Stat(cfg.Storage); err != nil || !fi.IsDir() {
		log.Fatalf("storage %s does not exist (is the NAS mounted?)", cfg.Storage)
	}
	os.MkdirAll(cfg.Data, 0o750)
	db, err := openDB(filepath.Join(cfg.Data, "argus.db"))
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	if flag.Arg(0) != "user" {
		if err := logDB.open(db); err != nil {
			log.Fatalf("database: %v", err)
		}
	}
	if err := initAuthDB(db); err != nil {
		log.Fatalf("database: %v", err)
	}
	if cams, err = openCamStore(db, cfg); err != nil {
		log.Fatalf("cameras: %v", err)
	}
	if flag.Arg(0) == "bctest" { // argus bctest <camera> <mainStream|subStream|externStream> <sec> <file.h264>
		os.Exit(bcTest(cfg, flag.Args()[1:]))
	}
	if flag.Arg(0) == "user" {
		if err := userCmd(db, cfg, flag.Args()[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	migrateToEnglish(db) // once: Danish names in an older database → English
	settings, err := newSettings(db, cfg)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	settings.adoptRetention() // before the first cleanup
	ev := newEventManager(cfg, db, settings)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv := &Server{cfg: cfg, ev: ev, auth: newAuth(db), ctx: ctx, cams: map[string]*CamRunner{},
		cancels: map[string]context.CancelFunc{}, dones: map[string]chan struct{}{}}
	initStore(cfg)
	publicHosts = cfg.PublicHosts
	trustedProxies = parseNets(cfg.TrustedProxies)
	srv.startLanguage()
	if cfg.StorageMount != "" && !srv.mainOK() {
		if store.local != "" {
			store.useLocal.Store(true)
			log.Printf("ERROR: the NAS (%s) is not responding – recording locally in %s until it is back", cfg.Storage, store.local)
		} else {
			srv.storagePaused.Store(true)
			log.Printf("ERROR: storage %s is not available (network drive not mounted) – waiting to record", cfg.Storage)
		}
	}
	for _, c := range cams.All() {
		srv.startCam(c)
	}
	go srv.watchStorage()
	go runRetention(ctx, cfg, ev)
	go srv.measureUsage(ctx)
	go srv.guardDisks(ctx)
	go srv.ensureUpdateEntity(ctx)
	startHA(ctx, srv)
	// events also go to /api/stream (the Home Assistant integration) – after MQTT has set its own hook
	prevOnEvent := ev.OnEvent
	ev.OnEvent = func(e Event) {
		if prevOnEvent != nil {
			prevOnEvent(e)
		}
		hub.publish("event", e.Cam, streamEvent{Event: e, Ongoing: e.End.IsZero()})
	}
	go srv.watchForStream()
	go srv.announceIntegration()

	hs := &http.Server{Addr: cfg.Listen, Handler: srv.routes(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := hs.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()
	startProfiling() // only in the private build (private.go)
	go func() {      // forget old failed logins and expired sessions
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Hour):
				srv.auth.cleanup()
			}
		}
	}()
	log.Printf("ArgusSen %s started: %d cameras, storage %s, web %s", version, len(srv.ids), cfg.Storage, cfg.Listen)
	<-ctx.Done()
	log.Printf("stopping …")
	sh, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hs.Shutdown(sh)
	time.Sleep(2 * time.Second) // let ffmpeg finish its segments
	db.Close()
}

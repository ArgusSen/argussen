package main

// Home Assistant shows "Update" for an app through an update entity made by its Supervisor integration. The
// integration only notices newly installed apps when it refreshes its app list – and that refresh is paused while
// there are no app entities at all (a fresh Home Assistant whose first app is ArgusSen). Then updates for ArgusSen
// cannot be installed until Home Assistant restarts. (Found in a real install test 2026-11-20; Home Assistant's
// hassio/coordinator.py, "Scheduled polling is paused in that case as well".)
//
// So, as a Home Assistant app, ArgusSen checks a few minutes after start whether its update entity exists. If not,
// it asks Home Assistant to reload the Supervisor integration – the same as Home Assistant does itself when it
// notices a new app. If that does not help, a notification asks the user to restart Home Assistant once.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

func (s *Server) ensureUpdateEntity(ctx context.Context) {
	if !s.cfg.Ingress || os.Getenv("SUPERVISOR_TOKEN") == "" {
		return
	}
	wait := func(d time.Duration) bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(d):
			return true
		}
	}
	if !wait(3 * time.Minute) { // Home Assistant may still be starting
		return
	}
	slug, err := supervisorSelfSlug()
	if err != nil {
		return
	}
	exists := func() (bool, error) {
		var entries []struct {
			Platform string `json:"platform"`
			UniqueID string `json:"unique_id"`
		}
		if err := haWS("config/entity_registry/list", &entries); err != nil {
			return false, err
		}
		for _, e := range entries {
			if e.Platform == "hassio" && e.UniqueID == slug+"_version_latest" {
				return true, nil
			}
		}
		return false, nil
	}
	for attempt := 0; attempt < 5; attempt++ { // Home Assistant may not answer yet: try for up to ~10 minutes
		ok, err := exists()
		if err == nil {
			if ok {
				return
			}
			break
		}
		if !wait(2 * time.Minute) {
			return
		}
	}
	var hassioEntries []struct {
		EntryID string `json:"entry_id"`
	}
	if err := haWSCmd(map[string]any{"type": "config_entries/get", "domain": "hassio"}, &hassioEntries); err != nil || len(hassioEntries) == 0 {
		return
	}
	if err := haCoreRequest("POST", "/api/config/config_entries/entry/"+hassioEntries[0].EntryID+"/reload"); err != nil {
		log.Printf("Home Assistant: could not reload the Supervisor integration: %v", err)
		return
	}
	log.Printf("Home Assistant: the update entity for ArgusSen was missing – the Supervisor integration was reloaded")
	if !wait(time.Minute) {
		return
	}
	if ok, err := exists(); err == nil && !ok {
		haNotify(hn("ArgusSen: restart Home Assistant once"),
			hn("Updates for ArgusSen can only be installed after Home Assistant has been restarted once (Settings → ⋮ → Restart Home Assistant)."))
	}
}

// supervisorSelfSlug: the app's own slug (e.g. "1a2b3c4d_argussen").
func supervisorSelfSlug() (string, error) {
	req, _ := http.NewRequest("GET", "http://supervisor/addons/self/info", nil)
	req.Header.Set("Authorization", "Bearer "+os.Getenv("SUPERVISOR_TOKEN"))
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var r struct {
		Data struct {
			Slug string `json:"slug"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil || r.Data.Slug == "" {
		return "", fmt.Errorf("no slug (HTTP %d)", resp.StatusCode)
	}
	return r.Data.Slug, nil
}

// haCoreRequest calls Home Assistant's REST API through the Supervisor.
func haCoreRequest(method, path string) error {
	req, _ := http.NewRequest(method, "http://supervisor/core"+path, nil)
	req.Header.Set("Authorization", "Bearer "+os.Getenv("SUPERVISOR_TOKEN"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// haNotify shows a notification in Home Assistant (the bell in the sidebar).
func haNotify(title, message string) {
	b, _ := json.Marshal(map[string]string{"title": title, "message": message, "notification_id": "argussen_restart"})
	req, _ := http.NewRequest("POST", "http://supervisor/core/api/services/persistent_notification/create", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+os.Getenv("SUPERVISOR_TOKEN"))
	req.Header.Set("Content-Type", "application/json")
	if resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req); err == nil {
		resp.Body.Close()
	}
}

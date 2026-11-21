package main

// One-time migration of an existing database to the English names (2026-11-20). Before that, the AI objects,
// the access log and the log kinds were stored with Danish names. Runs at start-up and does nothing afterwards.

import (
	"database/sql"
	"log"
	"time"
)

func migrateToEnglish(db *sql.DB) {
	// the tables are created here as well, so the migration can run before the settings are loaded
	db.Exec(`CREATE TABLE IF NOT EXISTS kv(k TEXT PRIMARY KEY, v TEXT)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS cam_settings(cam TEXT PRIMARY KEY, json TEXT NOT NULL)`)
	var done string
	db.QueryRow(`SELECT v FROM kv WHERE k='migrated_en'`).Scan(&done)
	if done == "1" {
		return
	}
	// a new database (no log lines from before this start): nothing to migrate, and no old Home Assistant devices
	// to remove (ha.go)
	var lines int
	if db.QueryRow(`SELECT COUNT(*) FROM log WHERE t < ?`, time.Now().Add(-time.Minute).UnixMilli()).Scan(&lines) != nil || lines == 0 {
		db.Exec(`INSERT INTO kv(k,v) VALUES('migrated_en','1'),('ha_legacy_removed','1') ON CONFLICT(k) DO UPDATE SET v='1'`)
		return
	}
	stmts := []string{
		// AI objects in events (comma separated) and boxes (JSON)
		`UPDATE events SET labels = TRIM(REPLACE(REPLACE(REPLACE(','||labels||',', ',bil,', ',car,'), ',varebil,', ',van,'), ',cykel,', ',bicycle,'), ',')`,
		`UPDATE events SET boxes = REPLACE(REPLACE(REPLACE(boxes, '"label":"bil"', '"label":"car"'), '"label":"varebil"', '"label":"van"'), '"label":"cykel"', '"label":"bicycle"') WHERE boxes IS NOT NULL`,
		// chosen objects per camera
		`UPDATE cam_settings SET json = REPLACE(REPLACE(REPLACE(json, '"bil"', '"car"'), '"varebil"', '"van"'), '"cykel"', '"bicycle"')`,
		// log kinds
		`UPDATE log SET kind = 'error' WHERE kind = 'fejl'`,
		`UPDATE log SET kind = 'user' WHERE kind = 'bruger'`,
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			log.Printf("migration: %v", err)
		}
	}
	// the access log: table "adgang" → "access"
	var n int
	if db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='adgang'`).Scan(&n); n == 1 {
		_, err := db.Exec(`INSERT INTO access(at,type,username,ip,origin,device)
			SELECT tid,
				CASE type WHEN 'aabnet' THEN 'opened' WHEN 'afvist' THEN 'refused' ELSE type END,
				bruger, ip,
				CASE fra WHEN 'lokalt' THEN 'local' WHEN 'hjemme' THEN 'home' WHEN 'udefra' THEN 'remote' ELSE fra END,
				enhed FROM adgang ORDER BY id`)
		if err == nil {
			db.Exec(`DROP TABLE adgang`)
		} else {
			log.Printf("migration of the access log: %v", err)
		}
	}
	db.Exec(`INSERT INTO kv(k,v) VALUES('migrated_en','1') ON CONFLICT(k) DO UPDATE SET v='1'`)
	log.Printf("database migrated to English names")
}

package main

import (
	"database/sql"
	"log"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// The log: all log lines are also stored in the database, so the app can show them.
// A line "garage/main: text" gets the object "garage". The kind is guessed from the text.

type LogLine struct {
	ID   int64     `json:"id"`
	Time time.Time `json:"time"`
	Kind string    `json:"kind"` // info, error, ok, alarm, user
	Obj  string    `json:"obj"`
	Msg  string    `json:"msg"`
	line string    // the whole English line ("" in rows from before 0.7.0)
}

type logStore struct {
	mu  sync.Mutex
	db  *sql.DB
	buf []LogLine // lines from before the database is open
}

var logDB = &logStore{}

var objRe = regexp.MustCompile(`^([\p{L}0-9_]+)(/(main|sub))? ?: (.*)$`)

// splitLine fills in the object and text of a log line (shown = the line in the language lang).
func splitLine(ll *LogLine, shown, lang string) {
	if ll.Kind == "user" {
		ll.Obj, ll.Msg = translateLog("User", lang), shown
		return
	}
	ll.Obj, ll.Msg = "ArgusSen", shown
	if m := objRe.FindStringSubmatch(shown); m != nil {
		ll.Obj, ll.Msg = m[1], m[4]
		if m[3] != "" {
			ll.Msg = strings.ToUpper(m[3][:1]) + m[3][1:] + ": " + ll.Msg
		}
	}
}

// logKind guesses the kind from the English line.
func logKind(line string) string {
	lm := strings.ToLower(line)
	switch {
	case strings.Contains(lm, "confirmed by ai"):
		return "alarm"
	case strings.HasPrefix(lm, "login") || strings.Contains(lm, "password") || strings.Contains(lm, "settings for"):
		return "user"
	case strings.Contains(lm, ": running"):
		return "ok"
	case strings.Contains(lm, "error") || strings.Contains(lm, "retrying") || strings.Contains(lm, "restarting") ||
		strings.Contains(lm, "reconnecting") || strings.Contains(lm, "not responding"):
		return "error"
	}
	return "info"
}

// Write: the code logs in English. The line is stored in English (column line) and translated when shown.
func (l *logStore) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if line == "" {
			continue
		}
		shown := translateLog(line, lang())
		os.Stderr.Write([]byte(shown + "\n"))
		ll := LogLine{Time: time.Now(), Kind: logKind(line), line: line}
		splitLine(&ll, line, "en")
		l.add(ll)
	}
	return len(p), nil
}

func (l *logStore) add(ll LogLine) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.db == nil {
		if len(l.buf) < 1000 {
			l.buf = append(l.buf, ll)
		}
		return
	}
	l.db.Exec(`INSERT INTO log(t,kind,obj,msg,line) VALUES(?,?,?,?,?)`, ll.Time.UnixMilli(), ll.Kind, ll.Obj, ll.Msg, ll.line)
}

func (l *logStore) open(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS log(id INTEGER PRIMARY KEY, t INTEGER NOT NULL, kind TEXT, obj TEXT, msg TEXT);
		CREATE INDEX IF NOT EXISTS log_t ON log(t);`); err != nil {
		return err
	}
	db.Exec(`ALTER TABLE log ADD COLUMN line TEXT NOT NULL DEFAULT ''`) // fails harmlessly if it exists
	l.mu.Lock()
	l.db = db
	buf := l.buf
	l.buf = nil
	l.mu.Unlock()
	for _, ll := range buf {
		l.add(ll)
	}
	return nil
}

// cleanLog: the log is kept for 90 days.
func (l *logStore) clean() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.db != nil {
		l.db.Exec(`DELETE FROM log WHERE t < ?`, time.Now().Add(-90*24*time.Hour).UnixMilli())
	}
}

func (l *logStore) list(before int64, q string, limit int) ([]LogLine, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	sqlq := `SELECT id,t,kind,obj,msg,line FROM log WHERE id < ?`
	args := []any{before}
	if q != "" {
		sqlq += ` AND (obj LIKE ? OR msg LIKE ? OR kind = ?)`
		args = append(args, "%"+q+"%", "%"+q+"%", q)
	}
	sqlq += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := l.db.Query(sqlq, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogLine{}
	for rows.Next() {
		var ll LogLine
		var t int64
		rows.Scan(&ll.ID, &t, &ll.Kind, &ll.Obj, &ll.Msg, &ll.line)
		ll.Time = time.UnixMilli(t)
		if ll.line != "" && lang() != "en" {
			splitLine(&ll, translateLog(ll.line, lang()), lang())
		}
		out = append(out, ll)
	}
	return out, rows.Err()
}

func init() { log.SetOutput(logDB) }

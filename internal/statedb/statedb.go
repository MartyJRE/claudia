// Package statedb opens state.db, the SQLite file the TUI and the watcher
// share: the run log, the cost ledger and the autopilot's own state.
package statedb

import (
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "modernc.org/sqlite"

	"github.com/MartyJRE/claudia/internal/state"
)

const File = "state.db"

// Migrations[i] takes the schema from user_version i to i+1.
var Migrations = []string{
	`CREATE TABLE IF NOT EXISTS runs (
		id       TEXT PRIMARY KEY,
		kind     TEXT NOT NULL,
		subject  TEXT NOT NULL,
		step     TEXT NOT NULL,
		session  TEXT NOT NULL DEFAULT '',
		sha      TEXT NOT NULL DEFAULT '',
		started  INTEGER NOT NULL,
		finished INTEGER NOT NULL DEFAULT 0,
		outcome  TEXT NOT NULL,
		reason   TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX IF NOT EXISTS runs_subject ON runs (subject, started);
	CREATE INDEX IF NOT EXISTS runs_session ON runs (session);`,

	`ALTER TABLE runs ADD COLUMN started_by TEXT NOT NULL DEFAULT '';
	CREATE TABLE kv (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);
	CREATE TABLE overrides (
		subject TEXT NOT NULL,
		step    TEXT NOT NULL,
		mode    TEXT NOT NULL,
		at      INTEGER NOT NULL,
		PRIMARY KEY (subject, step)
	);
	CREATE TABLE costs (
		session TEXT PRIMARY KEY,
		ticket  TEXT NOT NULL DEFAULT '',
		linked  INTEGER NOT NULL DEFAULT 0,
		profile TEXT NOT NULL DEFAULT '',
		usd     REAL NOT NULL DEFAULT 0,
		tokens  INTEGER NOT NULL DEFAULT 0,
		models  TEXT NOT NULL DEFAULT '{}',
		first   INTEGER NOT NULL DEFAULT 0,
		last    INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX costs_ticket ON costs (ticket);`,

	`CREATE TABLE agreed (
		subject TEXT PRIMARY KEY,
		pos     INTEGER NOT NULL,
		note    TEXT NOT NULL DEFAULT '',
		at      INTEGER NOT NULL,
		sprint  INTEGER NOT NULL DEFAULT 0
	);`,

	`CREATE TABLE todos (
		id      INTEGER PRIMARY KEY AUTOINCREMENT,
		title   TEXT NOT NULL,
		note    TEXT NOT NULL DEFAULT '',
		asked   TEXT NOT NULL DEFAULT '',
		project TEXT NOT NULL DEFAULT '',
		ticket  TEXT NOT NULL DEFAULT '',
		created INTEGER NOT NULL,
		done    INTEGER NOT NULL DEFAULT 0
	);`,
}

// Open opens state.db in the state dir, migrating it as needed. The TUI and
// the watcher both write, hence WAL and a busy timeout.
func Open() (*sql.DB, error) {
	if err := os.MkdirAll(state.Dir(), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+state.Path(File)+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func migrate(db *sql.DB) error {
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	for ; v < len(Migrations); v++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(Migrations[v] + fmt.Sprintf(`; PRAGMA user_version = %d`, v+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("state.db migration %d: %w", v+1, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// MS stores a time as Unix milliseconds, the zero time as 0.
func MS(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func FromMS(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v)
}

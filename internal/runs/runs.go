// Package runs keeps the run log: every Board step claudia started, the
// session it runs in and how it went. It's the only thing that knows the log
// lives in SQLite.
package runs

import (
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "modernc.org/sqlite"

	"github.com/MartyJRE/claudia/internal/state"
)

const (
	Running  = "running"
	OK       = "ok"
	NeedsYou = "needs-you"
	Failed   = "failed"
)

// Run is one step started on a ticket (Kind "ticket", Subject its key) or a
// review (Kind "review", Subject the MR's URL).
type Run struct {
	ID       string
	Kind     string
	Subject  string
	Step     string
	Session  string
	SHA      string // the MR's head when a review or feedback run started
	Started  time.Time
	Finished time.Time
	Outcome  string
	Reason   string
}

// Settled: the run ended one way or another.
func (r Run) Settled() bool { return r.Outcome != Running && r.Outcome != NeedsYou }

type Store struct{ db *sql.DB }

const file = "state.db"

// migrations[i] takes the schema from user_version i to i+1.
var migrations = []string{
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
}

// Open opens state.db in the state dir, migrating it as needed. The TUI and
// the watcher both write, hence WAL and a busy timeout.
func Open() (*Store, error) {
	if err := os.MkdirAll(state.Dir(), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+state.Path(file)+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	s := &Store{db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	for ; v < len(migrations); v++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[v] + fmt.Sprintf(`; PRAGMA user_version = %d`, v+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("state.db migration %d: %w", v+1, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

// Start records a run; one without an outcome is running.
func (s *Store) Start(r Run) error {
	if r.Outcome == "" {
		r.Outcome = Running
	}
	_, err := s.db.Exec(`INSERT INTO runs (id, kind, subject, step, session, sha, started, finished, outcome, reason)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Kind, r.Subject, r.Step, r.Session, r.SHA, ms(r.Started), ms(r.Finished), r.Outcome, r.Reason)
	return err
}

// Settle sets the outcome of the session's run while it can still change.
func (s *Store) Settle(session, outcome, reason string, at time.Time) error {
	_, err := s.db.Exec(`UPDATE runs SET outcome = ?, reason = ?, finished = ? WHERE session = ? AND outcome IN (?, ?)`,
		outcome, reason, ms(at), session, Running, NeedsYou)
	return err
}

// For is the subject's runs, newest first.
func (s *Store) For(subject string) []Run {
	return s.query(`WHERE subject = ? ORDER BY started DESC, id`, subject)
}

// Running is the runs still under way, newest first.
func (s *Store) Running() []Run {
	return s.query(`WHERE outcome = ? ORDER BY started DESC, id`, Running)
}

// open is the runs a session can still change: running ones, and needs-you
// ones whose session may carry on once answered.
func (s *Store) open() []Run {
	return s.query(`WHERE outcome IN (?, ?) ORDER BY started DESC, id`, Running, NeedsYou)
}

func (s *Store) query(where string, args ...any) []Run {
	rows, err := s.db.Query(`SELECT id, kind, subject, step, session, sha, started, finished, outcome, reason FROM runs `+where, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var r Run
		var started, finished int64
		if rows.Scan(&r.ID, &r.Kind, &r.Subject, &r.Step, &r.Session, &r.SHA, &started, &finished, &r.Outcome, &r.Reason) != nil {
			continue
		}
		r.Started, r.Finished = fromMS(started), fromMS(finished)
		out = append(out, r)
	}
	return out
}

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMS(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v)
}

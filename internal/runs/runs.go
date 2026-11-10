// Package runs keeps the run log: every Board step claudia started, the
// session it runs in and how it went. It's the only thing that knows the log
// lives in SQLite.
package runs

import (
	"database/sql"
	"slices"
	"time"

	"github.com/MartyJRE/claudia/internal/statedb"
)

const (
	Running  = "running"
	OK       = "ok"
	NeedsYou = "needs-you"
	// Waiting: the run asked someone else and can't go on until they answer.
	Waiting = "waiting"
	Failed  = "failed"
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
	// StartedBy is ByAutopilot for runs the autopilot started, else empty.
	StartedBy string
}

const ByAutopilot = "autopilot"

// Settled: the run ended one way or another.
func (r Run) Settled() bool { return !slices.Contains(openOutcomes, r.Outcome) }

// openOutcomes are those a run's session can still change.
var openOutcomes = []string{Running, NeedsYou, Waiting}

type Store struct{ db *sql.DB }

// Open opens state.db, migrating it as needed.
func Open() (*Store, error) {
	db, err := statedb.Open()
	if err != nil {
		return nil, err
	}
	return &Store{db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Start records a run; one without an outcome is running.
func (s *Store) Start(r Run) error {
	if r.Outcome == "" {
		r.Outcome = Running
	}
	_, err := s.db.Exec(`INSERT INTO runs (id, kind, subject, step, session, sha, started, finished, outcome, reason, started_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Kind, r.Subject, r.Step, r.Session, r.SHA, ms(r.Started), ms(r.Finished), r.Outcome, r.Reason, r.StartedBy)
	return err
}

// Settle sets the outcome of the session's newest run while it can still
// change; a resumed session's older runs keep theirs.
func (s *Store) Settle(session, outcome, reason string, at time.Time) error {
	_, err := s.db.Exec(`UPDATE runs SET outcome = ?, reason = ?, finished = ? WHERE id = (SELECT id FROM runs
		WHERE session = ? AND outcome IN (?, ?, ?) ORDER BY started DESC, id LIMIT 1)`,
		outcome, reason, ms(at), session, Running, NeedsYou, Waiting)
	return err
}

// Note sets the reason on a run still under way, leaving it running.
func (s *Store) Note(session, reason string) error {
	_, err := s.db.Exec(`UPDATE runs SET reason = ? WHERE session = ? AND outcome = ?`, reason, session, Running)
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

// Since is every run started at or after t, newest first.
func (s *Store) Since(t time.Time) []Run {
	return s.query(`WHERE started >= ? ORDER BY started DESC, id`, ms(t))
}

// AutopilotUSD is what the sessions of runs the autopilot started since t
// cost, as far as the cost ledger knows.
func (s *Store) AutopilotUSD(since time.Time) float64 {
	var usd float64
	_ = s.db.QueryRow(`SELECT COALESCE(SUM(c.usd), 0) FROM costs c WHERE c.session IN
		(SELECT session FROM runs WHERE started_by = ? AND started >= ? AND session != '')`, ByAutopilot, ms(since)).Scan(&usd)
	return usd
}

// open is the runs a session can still change: running ones, and needs-you
// or waiting ones whose session may carry on once answered.
func (s *Store) open() []Run {
	return s.query(`WHERE outcome IN (?, ?, ?) ORDER BY started DESC, id`, Running, NeedsYou, Waiting)
}

// SessionsStartedBy is the sessions of the runs started by by.
func (s *Store) SessionsStartedBy(by string) map[string]bool {
	out := map[string]bool{}
	for _, r := range s.query(`WHERE started_by = ? AND session != ''`, by) {
		out[r.Session] = true
	}
	return out
}

// Latest is each session's newest run.
func (s *Store) Latest() map[string]Run {
	out := map[string]Run{}
	for _, r := range s.query(`WHERE session != '' ORDER BY started DESC, id`) {
		if _, ok := out[r.Session]; !ok {
			out[r.Session] = r
		}
	}
	return out
}

func (s *Store) query(where string, args ...any) []Run {
	rows, err := s.db.Query(`SELECT id, kind, subject, step, session, sha, started, finished, outcome, reason, started_by FROM runs `+where, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var r Run
		var started, finished int64
		if rows.Scan(&r.ID, &r.Kind, &r.Subject, &r.Step, &r.Session, &r.SHA, &started, &finished, &r.Outcome, &r.Reason, &r.StartedBy) != nil {
			continue
		}
		r.Started, r.Finished = fromMS(started), fromMS(finished)
		out = append(out, r)
	}
	return out
}

func ms(t time.Time) int64 { return statedb.MS(t) }

func fromMS(v int64) time.Time { return statedb.FromMS(v) }

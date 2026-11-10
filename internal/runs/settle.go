package runs

import (
	"regexp"
	"strings"
	"time"

	"github.com/MartyJRE/claudia/internal/claude"
)

// Marker is the line a run ends its last message with.
const Marker = "CLAUDIA_RESULT:"

var resultLine = regexp.MustCompile(`(?m)` + Marker + `[ \t]*(ok|needs-you|failed)\b[ \t—–:-]*(.*)$`)

// Result reads the last result line of a reply.
func Result(text string) (outcome, reason string, ok bool) {
	all := resultLine.FindAllStringSubmatch(text, -1)
	if len(all) == 0 {
		return "", "", false
	}
	m := all[len(all)-1]
	return m[1], strings.Trim(strings.TrimSpace(m[2]), "`*"), true
}

// Seen is what the caller knows of a run's session.
type Seen struct {
	Session string
	Live    bool   // its process still runs
	Working bool   // and is busy
	Reply   string // the last turn, when it's Claude's reply
	Summary string // the watcher's summary of that very turn, if written
	// ContextFull: the context is all but used up, or Claude Code said so;
	// without autocompact the session can't go on until /compact.
	ContextFull bool
	// Starting: its claude process runs but hasn't registered, e.g. held by a
	// startup dialog nobody answered.
	Starting bool
}

// WithStarting marks the sessions whose claude process runs without having
// registered, from the ids of running processes; one with a transcript from
// before counts too.
func WithStarting(seen []Seen, procs map[string]claude.Proc) []Seen {
	known := map[string]bool{}
	for i, sn := range seen {
		known[sn.Session] = true
		if _, ok := procs[sn.Session]; ok && !sn.Live {
			seen[i].Starting = true
		}
	}
	for id := range procs {
		if !known[id] {
			seen = append(seen, Seen{Session: id, Starting: true})
		}
	}
	return seen
}

// StuckAtStartup is the reason on a run whose claude runs but never got going.
const StuckAtStartup = "stuck at startup"

// ContextFull is the reason a run whose context filled up waits on you.
const ContextFull = "context full"

// FullAt is the context fill a stopped run counts as full from.
const FullAt = 0.95

// SeenOf reads a session; summary counts only when written for its latest turn.
func SeenOf(s claude.Session, summary string, summaryFor time.Time) Seen {
	starting := s.Live != nil && s.Live.Starting
	sn := Seen{Session: s.ID, Live: s.Live != nil && !starting, Starting: starting}
	sn.Working = sn.Live && s.Live.Status.Working()
	if s.Transcript == nil {
		return sn
	}
	sn.ContextFull = s.ContextFull || s.ContextFill() >= FullAt
	if n := len(s.Tail); n > 0 && s.Tail[n-1].Role == "assistant" {
		sn.Reply = s.Tail[n-1].Text
	}
	if summaryFor.Equal(s.Updated) {
		sn.Summary = summary
	}
	return sn
}

// grace is how long a fresh run's session may stay unseen: claude takes a
// moment to register, and a /color run exits before the session proper starts.
const grace = Grace

// Grace is how long a fresh session may run without registering before it
// counts as stuck at startup.
const Grace = 2 * time.Minute

// Reconcile settles the open runs whose sessions stopped, and says whether any
// changed. A session idle without a result line keeps its run running: Claude
// may be waiting on the user.
func (s *Store) Reconcile(seen []Seen, now time.Time) (bool, error) {
	by := map[string]Seen{}
	for _, sn := range seen {
		by[sn.Session] = sn
	}
	changed := false
	for _, r := range s.open() {
		sn := by[r.Session]
		if r.Outcome == Running && r.Reason == StuckAtStartup && sn.Live {
			if err := s.Note(r.Session, ""); err != nil {
				return changed, err
			}
			changed = true
		}
		if sn.Working {
			continue
		}
		outcome, reason, ok := Result(sn.Reply)
		if !ok && sn.ContextFull {
			outcome, reason, ok = NeedsYou, ContextFull, true
		}
		if !sn.Live {
			// Still running as far as the slot count goes, so nothing piles up behind it.
			if sn.Starting {
				if r.Outcome == Running && r.Reason != StuckAtStartup && now.Sub(r.Started) >= grace {
					if err := s.Note(r.Session, StuckAtStartup); err != nil {
						return changed, err
					}
					changed = true
				}
				continue
			}
			if r.Outcome == NeedsYou || now.Sub(r.Started) < grace {
				continue
			}
			if !ok {
				outcome, reason = fallback(sn.Summary)
			}
			ok = true
		}
		if !ok || (outcome == r.Outcome && reason == r.Reason) {
			continue
		}
		if err := s.Settle(r.Session, outcome, reason, now); err != nil {
			return changed, err
		}
		changed = true
	}
	return changed, nil
}

// fallback reads the outcome off the summary of a session that ended without
// a result line.
func fallback(summary string) (string, string) {
	for prefix, outcome := range map[string]string{"Done:": OK, "Needs you:": NeedsYou} {
		if rest, ok := strings.CutPrefix(summary, prefix); ok {
			return outcome, strings.TrimSpace(rest)
		}
	}
	return Failed, "ended without a result"
}

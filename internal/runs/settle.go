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
}

// SeenOf reads a session; summary counts only when written for its latest turn.
func SeenOf(s claude.Session, summary string, summaryFor time.Time) Seen {
	sn := Seen{Session: s.ID, Live: s.Live != nil}
	sn.Working = sn.Live && s.Live.Status.Working()
	if s.Transcript == nil {
		return sn
	}
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
const grace = 2 * time.Minute

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
		if sn.Working {
			continue
		}
		outcome, reason, ok := Result(sn.Reply)
		if !sn.Live {
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

// Package summary writes one line per Claude session saying where it stands,
// so the sessions view shows which ones to go back to. A summary is a separate
// headless run over the transcript's tail; the session itself is never touched.
package summary

import (
	"strings"
	"sync"
	"time"

	"github.com/MartyJRE/claudia/internal/brief"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/state"
)

const file = "summaries.json"

type Summary struct {
	Text string `json:"text"`
	// For is the transcript's last update the summary was written from.
	For time.Time `json:"for"`
	Err string    `json:"error,omitempty"`
}

var mu sync.Mutex

// Load returns the summaries by session id.
func Load() map[string]Summary {
	mu.Lock()
	defer mu.Unlock()
	out := map[string]Summary{}
	state.Load(file, &out)
	return out
}

// Put stores one session's summary, keeping the others.
func Put(id string, s Summary) error {
	mu.Lock()
	defer mu.Unlock()
	all := map[string]Summary{}
	state.Load(file, &all)
	all[id] = s
	return state.Save(file, all)
}

// Stale says whether the session moved on since its summary was written.
func Stale(s claude.Session, have map[string]Summary) bool {
	if s.Transcript == nil || len(s.Tail) == 0 {
		return false
	}
	return !have[s.ID].For.Equal(s.Updated)
}

const instructions = `You label a developer's Claude Code session for their dashboard, so they can tell at a glance whether to go back to it.
You get the session's title and the last few turns of the conversation. Reply with one line, at most 14 words, no quotes, no trailing period:
- If Claude ended by asking the user something, or anything waits on the user (the developer reading this, whatever name they go by), start with "Needs you:" and say what (e.g. "Needs you: OK to send the test message?").
- If it's blocked on someone other than the user or on something external, start with "Waiting:" (e.g. "Waiting: Michal to approve the Graph permissions").
- If the work is finished with nothing pending, start with "Done:" and say the outcome (e.g. "Done: PR #5 open with mail and inline images").
- Otherwise say what was last being done.
Use names, ticket keys and PR numbers as they appear. Don't invent anything.`

var headless = brief.Headless

// Generate writes the summary for a session, as its own profile.
func Generate(s claude.Session) Summary {
	var b strings.Builder
	b.WriteString("Title: " + s.Name() + "\n\n")
	for _, t := range s.Tail {
		b.WriteString(t.Role + ":\n" + t.Text + "\n\n")
	}
	text, err := headless("summary", instructions, b.String(), s.Profile.Dir, "--model", "haiku")
	out := Summary{Text: oneLine(text), For: s.Updated}
	if err != nil {
		out.Err = err.Error()
	}
	return out
}

func oneLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return strings.Trim(strings.TrimSpace(s), `"`)
}

package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/runs"
	"github.com/MartyJRE/claudia/internal/tmux"
)

func TestStuckAtStartupFull(t *testing.T) {
	m := newModelShell(t)
	pane := tmux.Pane{ID: "%9", Session: "feeds", Window: "0", Index: "0"}
	fake(t, &locatePane, func(_ *tmux.Locator, pid int) (tmux.Pane, bool) { return pane, pid == 7 })
	rows := []row{{Session: claude.Session{ID: "known", Live: &claude.Live{}}}}
	procs := map[string]claude.Proc{
		"stuck": {PID: 7, Name: "review: fix it", Age: runs.Grace + time.Second},
		"other": {PID: 8, Name: "no pane", Age: time.Hour},
		"young": {PID: 9, Age: time.Second},
		"mine":  {PID: 10, Age: time.Hour},
		"known": {PID: 11, Age: time.Hour},
	}
	kinds := map[string]string{"stuck": "review", "other": "fix", "young": "review", "known": "review"}
	got := stuckRows(rows, procs, kinds, nil)
	if len(got) != 2 || got[0].ID != "other" || got[1].ID != "stuck" {
		t.Fatalf("rows %+v", got)
	}
	r := got[1]
	if !r.Live.Starting || !r.needsYou() || r.pane == nil || r.Name() != "review: fix it" || r.kind != "review" || got[0].pane != nil {
		t.Errorf("stuck row %+v", r)
	}
	if s := plainShell(m.status(r)); s != "◆ stuck at startup" {
		t.Errorf("status %q", s)
	}
	// It lands in the Inbox like any session waiting on you, saying why.
	m.sessions = append(m.sessions, r)
	found := false
	for _, it := range m.inbox() {
		if it.kind == kClaude && it.sess.ID == "stuck" {
			found = strings.Contains(it.text, "stuck at startup")
		}
	}
	if !found {
		t.Error("no inbox item for the stuck session")
	}
}

func TestStuckCardTurn(t *testing.T) {
	stuck := row{Session: claude.Session{ID: "s", Live: &claude.Live{Status: claude.StatusWaiting, Starting: true}}}
	if tn, next, _ := cardTurn(boardCard{sessions: []row{stuck}}, nil); tn != turnNeedsYou || next != "stuck at startup" {
		t.Errorf("turn %d %q", tn, next)
	}
}

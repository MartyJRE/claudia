package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/tmux"
)

func workerShell(id, kind string, st claude.Status) row {
	return row{
		Session: claude.Session{ID: id, Profile: claude.Profile{Name: "work"},
			Live: &claude.Live{SessionID: id, Cwd: "/state/worker/" + kind, Name: "claudia-" + kind, Status: st, Worker: kind, StartedAt: msAgoShell(time.Minute)}},
		pane: &tmux.Pane{ID: "%9", Session: "claudia", Window: "1", Index: "0"},
	}
}

func workersModelShell(t *testing.T, place string) *Model {
	withConfig(t, func(c *config.Config) { c.WorkersRaw = place })
	m := sessionsModelShell(t)
	m.Update(sessionsMsg(append(sessionsShell(), workerShell("wwwwwwww-sum", "summary", claude.StatusBusy), workerShell("vvvvvvvv-brf", "brief", claude.StatusIdle))))
	return m
}

func TestWorkersKeptApartShell(t *testing.T) {
	m := workersModelShell(t, "hidden")
	if len(m.sessions) != 4 || len(m.workers) != 2 {
		t.Fatalf("workers are split off: %d sessions, %d workers", len(m.sessions), len(m.workers))
	}
	if got := sessionIDsShell(m); got != "aaaa bbbb cccc #Recently closed dddd" {
		t.Errorf("hidden lists none: %s", got)
	}
	s := screenShell(m)
	containsAllShell(t, "hidden", s, "1 working", "2 Sessions 3")
	lacksShell(t, "hidden", s, "claudia's workers", "2 working")
}

func TestWorkersTopAndBottomShell(t *testing.T) {
	m := workersModelShell(t, "top")
	if got := sessionIDsShell(m); got != "#claudia's workers+ aaaa bbbb cccc #Recently closed dddd" {
		t.Errorf("top, folded by default: %s", got)
	}
	m.cur[vSessions].pos = 0
	pressShell(m, " ")
	if got := sessionIDsShell(m); got != "#claudia's workers wwww vvvv aaaa bbbb cccc #Recently closed dddd" {
		t.Errorf("space opens them: %s", got)
	}
	if p := loadSessionPrefs(); !slices.Equal(p.Expanded, []string{workersKey}) || len(p.Collapsed) != 0 {
		t.Errorf("opening is saved apart from folding: %+v", p)
	}
	s := screenShell(m)
	containsAllShell(t, "open", s, "claudia's workers  2 running in the background", "✶ thinking       claudia summary", "· idle           claudia brief", "1 working", "2 Sessions 3")

	cursorOnShell(t, m, "wwwwwwww-sum")
	containsAllShell(t, "detail", plainShell(m.detailSession()), "claudia summary", "to write a session's one-line summary", "work  ·  wwwwwwww")
	for _, k := range []string{"enter", "R", "K", "m", "S", "U", "t", "J", "o", "P"} {
		m.flash = ""
		pressShell(m, k)
		if m.chooser != nil || !strings.Contains(m.flash, "claudia's own summary run") {
			t.Errorf("%s on a worker: %q", k, m.flash)
		}
	}
	pressShell(m, " ")
	if got := sessionIDsShell(m); !strings.HasPrefix(got, "#claudia's workers+ aaaa") {
		t.Errorf("space on a worker folds its group: %s", got)
	}

	pressShell(m, "+")
	if got := sessionIDsShell(m); !strings.HasPrefix(got, "#claudia's workers wwww") {
		t.Errorf("+ opens them too: %s", got)
	}
	pressShell(m, "-")
	if p := m.sessPrefs; len(p.Expanded) != 0 || slices.Contains(p.Collapsed, workersKey) {
		t.Errorf("- folds them back to the default: %+v", p)
	}

	pressShell(m, "/")
	typeShell(m, "brief")
	if got := sessionIDsShell(m); got != "#claudia's workers vvvv" {
		t.Errorf("a filter matches the kind and unfolds: %s", got)
	}
	pressShell(m, "esc")

	withConfig(t, func(c *config.Config) { c.WorkersRaw = "bottom" })
	m.refilter()
	if got := sessionIDsShell(m); got != "aaaa bbbb cccc #Recently closed+ #claudia's workers+" {
		t.Errorf("bottom: %s", got)
	}
}

func TestWorkerUnknownKindShell(t *testing.T) {
	m := workersModelShell(t, "top")
	m.workers = []row{workerShell("uuuuuuuu-new", "triage", claude.StatusIdle)}
	m.sessPrefs.Expanded = []string{workersKey}
	m.refilter()
	cursorOnShell(t, m, "uuuuuuuu-new")
	if s := plainShell(m.detailSession()); !strings.Contains(s, "to write triage;") {
		t.Errorf("an unknown kind names itself: %s", s)
	}
}

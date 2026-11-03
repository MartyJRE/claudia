package ui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/tmux"
	"github.com/MartyJRE/claudia/internal/vault"
)

// fakeSessions makes the store report these sessions; pids in panes sit in tmux,
// pids in hosts run in that terminal app.
func fakeSessions(t *testing.T, ss []claude.Session, panes map[int]tmux.Pane, hosts map[int]string) {
	fake(t, &listSessions, func(*claude.Store) []claude.Session { return ss })
	fake(t, &locatePane, func(_ *tmux.Locator, pid int) (tmux.Pane, bool) {
		p, ok := panes[pid]
		return p, ok
	})
	fake(t, &locateHost, func(_ *tmux.Locator, pid int) string { return hosts[pid] })
}

func TestHeadlessFull(t *testing.T) {
	isolateShell(t)
	t.Setenv("HOME", t.TempDir())
	root := vaultViews(t)
	withConfig(t, func(c *config.Config) { c.Vault, c.VaultProjects = root, "01 - Projects" })
	fakeSessions(t, []claude.Session{
		{ID: "in-tmux", Live: &claude.Live{PID: 10, Cwd: "/code/a"}},
		{ID: "in-ghostty", Live: &claude.Live{PID: 20, Cwd: "/code/b"}},
		{ID: "closed", Transcript: &claude.Transcript{Cwd: "/code/c"}},
	}, map[int]tmux.Pane{10: {ID: "%5", Session: "a", Window: "1"}}, map[int]string{20: "Ghostty"})

	m := Headless()
	if len(m.sessions) != 3 {
		t.Fatalf("sessions: %+v", m.sessions)
	}
	if m.sessions[0].pane == nil || m.sessions[0].pane.ID != "%5" || m.sessions[1].pane != nil || m.sessions[1].host != "Ghostty" || m.sessions[2].pane != nil || m.sessions[2].host != "" {
		t.Errorf("live sessions are placed in tmux or their terminal: %+v", m.sessions)
	}
	if m.vault == nil || len(m.vaultNotes) != 4 {
		t.Errorf("the vault is read right away: %d notes", len(m.vaultNotes))
	}
	if len(m.vis.sessions) == 0 || len(m.vis.notes) == 0 {
		t.Errorf("rows are built for ctl: %d sessions, %d notes", len(m.vis.sessions), len(m.vis.notes))
	}
}

func TestNewVaultFull(t *testing.T) {
	file := vaultViews(t) + "/Inbox.md"
	for _, dir := range []string{"", "/does/not/exist", file} {
		withConfig(t, func(c *config.Config) { c.Vault = dir })
		if v := newVault(); v != nil {
			t.Errorf("vault %q: no index", dir)
		}
	}
}

func TestCtlHereNotesFull(t *testing.T) {
	m := newModelShell(t)
	m.vault = vault.New(vaultViews(t), "01 - Projects", claude.TicketRegexp())
	m.vault.Refresh()
	h := m.CtlHere(m.sessions[1])
	if len(h.Notes) != 2 || !strings.HasSuffix(h.Notes[0], ".md") {
		t.Errorf("notes about the session's ticket ABC-2: %v", h.Notes)
	}
}

func TestBriefDataSprintFull(t *testing.T) {
	m := newModelShell(t)
	m.jira.Sprint[0].Sprint = &atlassian.Sprint{Name: "Sprint 9"}
	b, _ := json.Marshal(m.BriefData())
	if !strings.Contains(string(b), `"Key":"ABC-1","Summary":"Payments export","Status":"Dev in progress","Epic":"","Sprint":"Sprint 9"`) {
		t.Errorf("issues carry their sprint: %s", b)
	}
}

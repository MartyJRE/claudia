package ui

import (
	"errors"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/vault"
)

func busyShell(m *Model, a claude.Activity, kind string) row {
	for i := range m.sessions {
		if m.sessions[i].ID == "bbbbbbbb-busy" {
			m.sessions[i].Activity = a
			m.sessions[i].kind = kind
			m.reindex()
			return m.sessions[i]
		}
	}
	panic("no busy session")
}

func TestActivityFramesAreOneCellShell(t *testing.T) {
	for verb, f := range activityFrames {
		for _, g := range f.glyphs {
			if lipgloss.Width(g) != 1 {
				t.Errorf("%s: %q is %d cells", verb, g, lipgloss.Width(g))
			}
		}
		if w := lipgloss.Width(f.glyphs[0] + " " + verb); w > sessionCols[0].w {
			t.Errorf("%s doesn't fit the STATUS column", verb)
		}
	}
}

func TestSessionActivityShell(t *testing.T) {
	m := newModelShell(t)
	m.view = vSessions
	for _, c := range []struct {
		act        claude.Activity
		kind, want string
	}{
		{claude.Activity{Verb: "running", Detail: "go test ./..."}, "", "⣾ running"},
		{claude.Activity{Verb: "editing", Detail: "internal/ui/app.go"}, "", "❙ editing"},
		{claude.Activity{Verb: "reading", Detail: "README.md"}, "", "◜ reading"},
		{claude.Activity{Verb: "delegating", Detail: "to 2 agents"}, "", "⠢ delegating"},
		{claude.Activity{Verb: "working", Detail: "getJiraIssue"}, "", "⠋ working"},
		{claude.Activity{}, "", "✶ thinking"},
		{claude.Activity{Verb: "reading", Detail: "a.go"}, "review", "◜ reviewing"},
		{claude.Activity{}, "review", "◜ reviewing"},
		{claude.Activity{Verb: "editing", Detail: "a.go"}, "review", "❙ editing"},
	} {
		if got := plainShell(m.status(busyShell(m, c.act, c.kind))); got != c.want {
			t.Errorf("%+v %s: %q, want %q", c.act, c.kind, got, c.want)
		}
	}

	r := busyShell(m, claude.Activity{Verb: "editing", Detail: "internal/ui/app.go"}, "")
	containsAllShell(t, "the row", screenShell(m), "❙ editing      invoice pdf ·personal  internal/ui/app.go")
	cursorOnShell(t, m, r.ID)
	containsAllShell(t, "the detail", plainShell(m.detailSession()), "❙ editing  internal/ui/app.go")
	m.frame = 3
	if got := plainShell(m.status(r)); got != "❚ editing" {
		t.Errorf("the caret moves every third frame: %q", got)
	}
	withConfig(t, func(c *config.Config) { off := false; c.AnimationsRaw = &off })
	if got := plainShell(m.status(r)); got != "❙ editing" || m.spin() != "⠋" {
		t.Errorf("still, glyphs keep their first frame: %q %q", got, m.spin())
	}

	r.Transcript = nil
	if got := plainShell(m.status(r)); got != "✶ thinking" {
		t.Errorf("no transcript yet: %q", got)
	}
	r.Live = &claude.Live{Status: claude.StatusIdle}
	if workingOn(r) != "" {
		t.Error("an idle session isn't working on anything")
	}
}

func TestAnimatesShell(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(*Model)
		want  bool
	}{
		{"inbox at rest", func(m *Model) {}, false},
		{"brief being written", func(m *Model) { m.briefPending = time.Now() }, true},
		{"fetching what the view shows", func(m *Model) { m.busy[srcGitlab] = true }, true},
		{"fetching something else", func(m *Model) { m.busy[srcJira] = true }, false},
		{"a working session", func(m *Model) { m.view = vSessions }, true},
		{"sessions at rest", func(m *Model) {
			m.view = vSessions
			m.sessions = m.sessions[:1]
			m.reindex()
		}, false},
		{"sessions loading", func(m *Model) { m.view, m.sessLoaded, m.sessions = vSessions, false, nil; m.reindex() }, true},
		{"MRs never fetched", func(m *Model) { m.view, m.gl.FetchedAt = vReview, time.Time{} }, true},
		{"MRs failed", func(m *Model) { m.view, m.gl.FetchedAt, m.errs[srcGitlab] = vMine, time.Time{}, errors.New("x") }, false},
		{"Jira without Atlassian", func(m *Model) { m.view, m.jira.FetchedAt = vJira, time.Time{} }, false},
		{"Jira loading", func(m *Model) { m.view, m.jira.FetchedAt, m.atlErr = vJira, time.Time{}, nil }, true},
		{"Confluence loading", func(m *Model) { m.view, m.atlErr = vConfluence, nil }, true},
		{"no vault", func(m *Model) { m.view = vNotes }, false},
		{"vault loading", func(m *Model) { m.view, m.vault = vNotes, &vault.Index{} }, true},
		{"usage loading", func(m *Model) { m.view = vUsage }, true},
		{"usage loaded", func(m *Model) { m.view, m.usageLoaded = vUsage, true }, false},
		{"mentions", func(m *Model) { m.view = vMentions }, false},
		{"updating", func(m *Model) { m.upd = &updateView{stage: upBuilding} }, true},
		{"update failed", func(m *Model) { m.upd = &updateView{stage: upBuilding, err: errors.New("x")} }, false},
		{"update done", func(m *Model) { m.upd = &updateView{stage: upDone} }, false},
		{"animations off", func(m *Model) {
			off := false
			withConfig(t, func(c *config.Config) { c.AnimationsRaw = &off })
			m.briefPending = time.Now()
		}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := newModelShell(t)
			c.setup(m)
			if got := m.animates(); got != c.want {
				t.Errorf("animates %v, want %v", got, c.want)
			}
		})
	}
}

func TestFramesRestartShell(t *testing.T) {
	m := newModelShell(t)
	ticks := instantTicks(t)
	m.animating = false
	if _, cmd := m.Update(flashMsg{text: "hi"}); cmd != nil || m.animating {
		t.Error("nothing moves, no frames")
	}
	m.briefPending = time.Now()
	_, cmd := m.Update(flashMsg{text: "hi"})
	if !m.animating || countFull[frameMsg](drainFull(cmd)) != 1 || len(*ticks) != 1 || (*ticks)[0] != 120*time.Millisecond {
		t.Errorf("a spinner on screen starts the frames: %v", *ticks)
	}
	if _, cmd := m.Update(flashMsg{text: "hi"}); cmd != nil {
		t.Error("and doesn't start a second chain")
	}
}

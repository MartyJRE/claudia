package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/MartyJRE/claudia/internal/config"
)

func TestKeyActionsShell(t *testing.T) {
	out := KeyActions()
	for _, a := range actions {
		if !strings.Contains(out, a.id) {
			t.Errorf("%s missing from `claudia keys`", a.id)
		}
	}
	if strings.Contains(out, "problem:") {
		t.Errorf("default keys report problems:\n%s", out)
	}
	if !strings.Contains(out, "space") {
		t.Errorf("the space key is spelled out")
	}
}

func TestActionScopesShell(t *testing.T) {
	km, _ := newKeymap(nil)
	m := &Model{km: km}
	for _, c := range []struct {
		v    view
		key  string
		want string
	}{
		{vInbox, "x", "inbox.snooze"},
		{vSessions, "x", ""},
		{vSessions, "K", "sessions.close"},
		{vReview, "a", "mrs.all"},
		{vMine, "f", "mrs.fix"},
		{vJira, "m", "jira.move"},
		{vMentions, "x", "mentions.dismiss"},
		{vConfluence, "y", "confluence.copy"},
		{vNotes, "e", "notes.edit"},
		{vUsage, "a", ""},
		{vUsage, "q", "global.quit"},
		{vNotes, "enter", "notes.read"},
		{vUsage, "enter", ""},
	} {
		m.view = c.v
		if got := m.action(c.key); got != c.want {
			t.Errorf("%s %q: %q, want %q", viewNames[c.v], c.key, got, c.want)
		}
	}
	if got := m.key("sessions.fold"); got != "space" {
		t.Errorf("key: %q", got)
	}
	if got := m.key("nope.nothing"); got != "—" {
		t.Errorf("unknown action: %q", got)
	}
	if got := m.hints("mrs.open", "sessions.close"); len(got) != 2 || got[0] != [2]string{"o", "open"} || got[1] != [2]string{"K", "kill"} {
		t.Errorf("hints: %v", got)
	}
}

func TestRemappedFooterShell(t *testing.T) {
	m := newModelShell(t)
	m.km, _ = newKeymap(map[string]config.KeyList{"inbox.snooze": {"z"}})
	m.switchView(vInbox)
	s := plainShell(m.View())
	containsAllShell(t, "remapped", s, "z snooze", "z snooze until it changes")
	if strings.Contains(s, "x snooze") {
		t.Errorf("old key still shown")
	}
	n := len(m.vis.inbox)
	pressShell(m, "x")
	if len(m.vis.inbox) != n {
		t.Errorf("old key still snoozes")
	}
	pressShell(m, "z")
	if len(m.vis.inbox) != n-1 {
		t.Errorf("new key snoozes")
	}
}

func TestModelColorShell(t *testing.T) {
	for fam, want := range map[string]lipgloss.Color{"Opus": purple, "Sonnet": blue, "Haiku": cyan, "Fable": red, "Gpt": commentGrey} {
		if got := modelColor(fam); got != want {
			t.Errorf("%s: %v", fam, got)
		}
	}
}

func TestPillsShell(t *testing.T) {
	if got := plainShell(pill(black, yellow, "2 needs you")); got != "2 needs you" {
		t.Errorf("rounded pill: %q", got)
	}
	if got := plainShell(tab("1 Inbox", false)); got != "1 Inbox" {
		t.Errorf("tab: %q", got)
	}
}

func TestHighlightShell(t *testing.T) {
	prev := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	lipgloss.SetColorProfile(3) // termenv.Ascii
	if got := highlight("plain"); got != "plain" {
		t.Errorf("without colour the text is untouched: %q", got)
	}

	lipgloss.SetColorProfile(0) // termenv.TrueColor
	styled := sKey.Render("a") + " " + sDim.Render("b")
	got := highlight(styled)
	if plainShell(got) != "a b" {
		t.Errorf("text changed: %q", plainShell(got))
	}
	bg := strings.TrimSuffix(lipgloss.NewStyle().Background(visualGrey).Render(" "), " \x1b[0m")
	if !strings.HasPrefix(got, bg) || strings.Count(got, bg) < 3 {
		t.Errorf("background must be reopened after every reset: %q", got)
	}
}

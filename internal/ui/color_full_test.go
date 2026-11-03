package ui

import (
	"strings"
	"testing"

	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/launch"
)

func colorRow(id, color string, live bool) row {
	r := row{Session: claude.Session{ID: id, Transcript: &claude.Transcript{Color: color}}}
	if live {
		r.Live = &claude.Live{SessionID: id, Status: "idle"}
	}
	return r
}

func TestColoredPicksLeastUsed(t *testing.T) {
	m := newModelShell(t)
	m.sessions = []row{
		colorRow("live-red", "red", true),
		colorRow("live-blue", "blue", true),
		colorRow("closed-green", "green", false),
		{Session: claude.Session{ID: "no-transcript", Live: &claude.Live{}}},
	}
	if got := m.colored(launch.Spec{ID: "new"}, nil).Color; got != "green" {
		t.Errorf("closed sessions don't count, so green is free: %q", got)
	}
	if got := m.colored(launch.Spec{ID: "new"}, []string{"green", "yellow"}).Color; got != "purple" {
		t.Errorf("colours given in the same batch count too: %q", got)
	}
	if got := m.colored(launch.Spec{ID: "closed-green"}, nil).Color; got != "" {
		t.Errorf("a session that has a colour keeps it, got %q", got)
	}
	m.sessions = nil
	for _, c := range launch.Colors {
		m.sessions = append(m.sessions, colorRow("live-"+c, c, true))
	}
	m.sessions = append(m.sessions, colorRow("live-red-2", "red", true))
	if got := m.colored(launch.Spec{ID: "new"}, nil).Color; got != "blue" {
		t.Errorf("all taken: the first least used, got %q", got)
	}
}

func TestColorDot(t *testing.T) {
	if got := plainShell(colorDot(colorRow("a", "pink", true))); got != "● " {
		t.Errorf("dot %q", got)
	}
	for _, r := range []row{colorRow("a", "", true), colorRow("a", "magenta", true), {Session: claude.Session{ID: "a"}}} {
		if colorDot(r) != "" {
			t.Errorf("no dot for %+v", r.Transcript)
		}
	}
	r := colorRow("abcdefgh-1", "cyan", true)
	if s := plainShell(sessionTitle(r, false)); !strings.HasPrefix(s, "● ") {
		t.Errorf("title %q", s)
	}
}

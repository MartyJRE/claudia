package ui

import (
	"testing"

	"github.com/MartyJRE/claudia/internal/config"
)

func TestKeymap(t *testing.T) {
	if _, problems := newKeymap(nil); len(problems) > 0 {
		t.Fatalf("default keys conflict: %v", problems)
	}

	km, problems := newKeymap(map[string]config.KeyList{"sessions.close": {"ctrl+w"}})
	if len(problems) > 0 {
		t.Fatalf("rebinding: %v", problems)
	}
	m := &Model{km: km, view: vSessions}
	if got := m.action("ctrl+w"); got != "sessions.close" {
		t.Errorf("ctrl+w → %q", got)
	}
	if got := m.action("K"); got != "" {
		t.Errorf("K still bound to %q", got)
	}
	if got := m.action("j"); got != "global.down" {
		t.Errorf("global fallback: j → %q", got)
	}

	_, problems = newKeymap(map[string]config.KeyList{"sessions.close": {"R"}, "sessions.nope": {"z"}, "inbox.snooze": {"q"}})
	if len(problems) != 3 {
		t.Errorf("want a clash in sessions, a clash with global and an unknown action, got %v", problems)
	}
}

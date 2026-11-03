package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/config"
)

func TestKeyActionsProblemsFull(t *testing.T) {
	withConfig(t, func(c *config.Config) { c.Keys = map[string]config.KeyList{"nope.action": {"z"}} })
	if out := KeyActions(); !strings.HasSuffix(out, "problem: unknown key action nope.action\n") {
		t.Errorf("problems are listed last:\n%s", out)
	}
}

func TestSquarePillsFull(t *testing.T) {
	square := false
	withConfig(t, func(c *config.Config) { c.RoundedPillsRaw = &square })
	if got := plainShell(rounded(lipgloss.NewStyle(), black, "Inbox")); got != " Inbox " {
		t.Errorf("square pills are padded text: %q", got)
	}
}

func TestUpdateMsgOtherFull(t *testing.T) {
	m := newModelShell(t)
	m.upd = &updateView{}
	if m.updateMsg(struct{}{}) != nil {
		t.Error("only update messages drive the update screen")
	}
}

func TestHandledMentionsDimFull(t *testing.T) {
	m := newModelShell(t)
	m.switchView(vMentions)
	pressShell(m, "a")
	if s := screenShell(m); !strings.Contains(s, "○") {
		t.Errorf("handled mentions show a hollow dot:\n%s", s)
	}
}

func TestJiraFoldEdgesFull(t *testing.T) {
	m := jiraModelViews(t)
	before := len(m.jiraPrefs.Collapsed)
	m.cur[vJira].pos = 99
	m.toggleFold()
	if len(m.jiraPrefs.Collapsed) != before {
		t.Error("a cursor past the end folds nothing")
	}

	m.unfoldTo("ABC-404")
	if len(m.jiraPrefs.Collapsed) != before {
		t.Error("an unknown issue unfolds nothing")
	}

	m.jira.Sprint = append(m.jira.Sprint, atlassian.Issue{Key: "ABC-6", Parent: "ABC-50"})
	m.setFolded("ABC-50", true)
	m.setFolded("sprint:No sprint", true)
	m.unfoldTo("ABC-6")
	if m.folded("ABC-50") || m.folded("sprint:No sprint") {
		t.Errorf("a parent outside the lists is still unfolded: %v", m.jiraPrefs.Collapsed)
	}
}

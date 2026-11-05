package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

const planMD = `# Add hello.txt

## Context
Add a small file.

## Steps
1. Create hello.txt.
2. Verify with cat.
3. Commit it.
4. Push it.
`

// planModelShell opens the plan review for a session whose plan file holds planMD.
func planModelShell(t *testing.T) *Model {
	t.Helper()
	m := newModelShell(t)
	p := filepath.Join(t.TempDir(), "plan.md")
	os.WriteFile(p, []byte(planMD), 0o600)
	r := m.sessions[0]
	r.PlanPath = p
	m.openPlan(r)
	if m.plan == nil {
		t.Fatalf("no plan: %s", m.flash)
	}
	return m
}

func TestPlanCommentFlowShell(t *testing.T) {
	m := planModelShell(t)
	pressShell(m, "G", "k", "V", "j", "c")
	typeShell(m, "don't commit or push")
	pressShell(m, "enter")
	p := m.plan
	if len(p.comments) != 1 || p.comments[0].From != 8 || p.comments[0].To != 9 || p.comments[0].Text != "don't commit or push" {
		t.Fatalf("comment on a selection: %+v (cur %d anchor %d editing %d)", p.comments, p.cur, p.anchor, p.editing)
	}
	pressShell(m, "k", "k", "c")
	typeShell(m, "skip the cat check")
	pressShell(m, "enter")
	if len(p.comments) != 2 || p.comments[1].From != 7 || p.comments[1].Text != "skip the cat check" {
		t.Fatalf("second comment: %+v", p.comments)
	}
	if s := screenShell(m); !strings.Contains(s, "you  skip the cat check") || !strings.Contains(s, "2 comment(s)") {
		t.Errorf("comments show under their lines:\n%s", s)
	}
}

func TestPlanKeyBurstShell(t *testing.T) {
	m := planModelShell(t)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("GkVjcno push")})
	p := m.plan
	if p.editing != 0 || p.anchor != 8 || p.cur != 9 || p.input.Value() != "no push" {
		t.Fatalf("a burst is taken key by key, the rest typed once writing: cur %d anchor %d editing %d %q", p.cur, p.anchor, p.editing, p.input.Value())
	}
	pressShell(m, "enter")
	if len(p.comments) != 1 || p.comments[0].From != 8 || p.comments[0].Text != "no push" {
		t.Errorf("%+v", p.comments)
	}
}

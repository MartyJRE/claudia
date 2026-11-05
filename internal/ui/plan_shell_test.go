package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/plan"
	"github.com/MartyJRE/claudia/internal/tmux"

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

func TestPlanOpenShell(t *testing.T) {
	m := newModelShell(t)
	r := m.sessions[0]
	r.Transcript = nil
	m.openPlan(r)
	if m.plan != nil || m.flash != "no plan in this session" {
		t.Errorf("no transcript: %q", m.flash)
	}
	r = m.sessions[0]
	r.PlanPath, r.Plan = filepath.Join(t.TempDir(), "gone.md"), ""
	m.openPlan(r)
	if m.plan != nil {
		t.Error("no plan text anywhere")
	}
	r.Plan = "# From the transcript\n1. step"
	m.openPlan(r)
	if m.plan == nil || m.plan.key != r.PlanPath || len(m.plan.lines) != 2 {
		t.Fatalf("falls back to the presented plan, keyed by its file: %+v", m.plan)
	}
	r.PlanPath = ""
	m.openPlan(r)
	if m.plan.key != "session:"+r.ID {
		t.Errorf("without a file, keyed by the session: %q", m.plan.key)
	}
}

func TestPlanMoveSelectEditDeleteShell(t *testing.T) {
	m := planModelShell(t)
	m.width, m.height = 100, 14
	p := m.plan
	last := len(p.lines) - 1
	for _, c := range []struct {
		keys []string
		cur  int
	}{
		{[]string{"j"}, 1}, {[]string{"down"}, 2}, {[]string{"k"}, 1}, {[]string{"up", "up"}, 0},
		{[]string{"G"}, last}, {[]string{"j"}, last}, {[]string{"g"}, 0}, {[]string{"end"}, last}, {[]string{"home"}, 0},
		{[]string{"ctrl+d"}, 5}, {[]string{"pgdown", "pgdown"}, last}, {[]string{"ctrl+u"}, last - 5}, {[]string{"pgup", "pgup"}, 0},
	} {
		pressShell(m, c.keys...)
		if p.cur != c.cur {
			t.Errorf("%v: cursor %d, want %d", c.keys, p.cur, c.cur)
		}
	}

	pressShell(m, "j", "V", "j", "j")
	if from, to := p.selection(); from != 1 || to != 3 {
		t.Errorf("selection %d..%d", from, to)
	}
	pressShell(m, "V")
	if p.anchor != -1 {
		t.Error("V again ends the selection")
	}
	pressShell(m, "V", "esc")
	if p.anchor != -1 || m.plan == nil {
		t.Error("esc first drops the selection, keeping the review open")
	}

	pressShell(m, "e")
	if p.editing != -1 || m.flash != "no comment on this line" {
		t.Errorf("e without a comment: %q", m.flash)
	}
	pressShell(m, "c")
	typeShell(m, "first")
	pressShell(m, "esc")
	if len(p.comments) != 0 || p.editing != -1 {
		t.Error("esc while writing drops it")
	}
	pressShell(m, "enter")
	typeShell(m, "  ")
	pressShell(m, "enter")
	if len(p.comments) != 0 {
		t.Error("an empty comment isn't kept")
	}
	pressShell(m, "c")
	typeShell(m, "line one")
	pressShell(m, "alt+enter")
	typeShell(m, "line two")
	if s := screenShell(m); !strings.Contains(s, "comment") || !strings.Contains(s, "enter save") {
		t.Errorf("the input box shows while writing:\n%s", s)
	}
	pressShell(m, "enter")
	if len(p.comments) != 1 || p.comments[0].Text != "line one\nline two" {
		t.Fatalf("multi-line comment: %+v", p.comments)
	}
	if saved := plan.Load(p.key, p.lines); len(saved) != 1 {
		t.Error("comments are saved as they're written")
	}

	pressShell(m, "e")
	if p.editing != 0 || p.input.Value() != "line one\nline two" {
		t.Fatalf("e edits the comment on the line: %d %q", p.editing, p.input.Value())
	}
	if s := screenShell(m); !strings.Contains(s, "edit comment") {
		t.Errorf("edit box:\n%s", s)
	}
	p.input.SetValue("changed")
	pressShell(m, "enter")
	if p.comments[0].Text != "changed" {
		t.Errorf("edited: %q", p.comments[0].Text)
	}
	pressShell(m, "e")
	p.input.SetValue("")
	pressShell(m, "enter")
	if len(p.comments) != 0 {
		t.Error("emptying a comment deletes it")
	}
	pressShell(m, "c")
	typeShell(m, "again")
	pressShell(m, "enter", "d")
	if len(p.comments) != 0 || plan.Load(p.key, p.lines) != nil {
		t.Error("d deletes it, saved")
	}
	pressShell(m, "d")
}

func TestPlanCloseShell(t *testing.T) {
	m := planModelShell(t)
	pressShell(m, "c")
	typeShell(m, "kept")
	pressShell(m, "enter")
	key, lines := m.plan.key, m.plan.lines
	if cmd := pressShell(m, "esc"); m.plan != nil || isQuitShell(cmd) {
		t.Error("esc closes the review back to claudia")
	}
	if got := plan.Load(key, lines); len(got) != 1 || got[0].Text != "kept" {
		t.Errorf("comments survive closing: %+v", got)
	}
	m.openPlan(m.sessions[0])
	m.plan = nil
	m = planModelShell(t)
	m.opts.PlanOnly = true
	if cmd := pressShell(m, "q"); m.plan != nil || !isQuitShell(cmd) {
		t.Error("in the popup, closing quits")
	}
}

func TestPlanSendShell(t *testing.T) {
	m := planModelShell(t)
	pressShell(m, "s")
	if m.flash != "no comments yet: c to write one" {
		t.Errorf("nothing to send: %q", m.flash)
	}
	pressShell(m, "j", "j", "c")
	typeShell(m, "why?")
	pressShell(m, "enter")
	want := "Comments on the plan:\n\n> ## Context\nwhy?"

	clip := fakeClipboard(t, nil)
	m.plan.sess.pane = nil
	cmd := pressShell(m, "s")
	if cmd == nil || !strings.Contains(m.flash, "isn't in tmux") {
		t.Fatalf("no pane: %q", m.flash)
	}
	cmd()
	if len(*clip) != 1 || !strings.HasSuffix((*clip)[0], " < "+want) {
		t.Errorf("copied instead: %v", *clip)
	}

	var sent []string
	var err error
	fake(t, &sendPlanFeedback, func(pane, text string) error { sent = append(sent, pane+" "+text); return err })
	m.plan.sess.pane = &tmux.Pane{ID: "%7"}
	for _, c := range []struct {
		err  error
		want string
	}{
		{tmux.ErrNoPlanPrompt, "the plan prompt isn't open: feedback copied, paste it into the plan prompt"},
		{errors.New("no server"), "no server: feedback copied, paste it into the plan prompt"},
	} {
		err = c.err
		msg := pressShell(m, "s")()
		_, cmd := m.Update(msg)
		if m.flash != c.want || !m.flashErr || m.plan == nil || cmd == nil {
			t.Errorf("%v: %q", c.err, m.flash)
		}
		cmd()
	}
	if len(sent) != 2 || sent[0] != "%7 "+want || len(*clip) != 3 {
		t.Errorf("sent %q, copied %d", sent, len(*clip))
	}

	err = nil
	key, lines := m.plan.key, m.plan.lines
	m.Update(pressShell(m, "s")())
	if m.plan != nil || m.flash != "feedback sent to "+m.sessions[0].Name() || plan.Load(key, lines) != nil {
		t.Errorf("sent: closed and cleared: %q", m.flash)
	}
	if _, cmd := m.Update(planSentMsg{}); cmd != nil {
		t.Error("a late result after closing does nothing")
	}
}

func TestPlanViewShell(t *testing.T) {
	m := newModelShell(t)
	m.width, m.height = 60, 12
	r := m.sessions[0]
	long := strings.Repeat("word ", 30)
	r.Plan = "# Title\n## Section\nuse `go test` here\n```\nfenced `not code`\n```\n" + long + "\n" + strings.Repeat("x\n", 20)
	r.PlanPath = ""
	m.openPlan(r)
	p := m.plan
	s := screenShell(m)
	for _, want := range []string{"plan · " + r.Name(), "0 comment(s)", "use go test here", "fenced `not code`", "j/k move"} {
		if !strings.Contains(s, want) {
			t.Errorf("%q missing:\n%s", want, s)
		}
	}
	if strings.Contains(s, "`go test`") {
		t.Error("inline code loses its backticks")
	}
	if n := strings.Count(s, "\n") + 1; n != m.height {
		t.Errorf("%d lines on a %d-line screen", n, m.height)
	}
	if !strings.Contains(planLine("# Title", false), "Title") || planLine("x", true) != sDim.Render("x") {
		t.Error("planLine")
	}

	pressShell(m, "j", "j", "j", "j", "j", "j", "c")
	typeShell(m, strings.Repeat("long comment ", 10))
	pressShell(m, "enter")
	s = screenShell(m)
	if !strings.Contains(s, "you  long comment") || strings.Count(s, "┃") < 2 {
		t.Errorf("a long comment wraps under its line:\n%s", s)
	}
	pressShell(m, "G")
	if s := screenShell(m); !strings.Contains(s, "▌ x") {
		t.Errorf("the cursor scrolls into view at the bottom:\n%s", s)
	}
	pressShell(m, "g")
	if s := screenShell(m); !strings.Contains(s, "▌ # Title") {
		t.Errorf("and back at the top:\n%s", s)
	}
	m.setFlash("boom", true)
	if !strings.Contains(screenShell(m), "boom") {
		t.Error("a flash takes the footer")
	}
	m.ix.sessionTicket[r.ID] = "ABC-1"
	if !strings.Contains(screenShell(m), "· ABC-1") {
		t.Error("the ticket shows in the header")
	}
	p.anchor = 0
	pressShell(m, "j", "j", "c")
	if s := screenShell(m); !strings.Contains(s, "comment on 3 lines") {
		t.Errorf("a selection's box says how many lines:\n%s", s)
	}
}

func TestPlanFromSessionsAndInboxShell(t *testing.T) {
	m := sessionsModelShell(t)
	p := filepath.Join(t.TempDir(), "plan.md")
	os.WriteFile(p, []byte(planMD), 0o600)
	for i := range m.sessions {
		if m.sessions[i].Transcript != nil {
			m.sessions[i].PlanPath = p
		}
	}
	m.reindex()
	m.switchView(vSessions)
	for m.vis.sessions[m.cur[vSessions].pos].isSess == false {
		pressShell(m, "down")
	}
	pressShell(m, "P")
	if m.plan == nil {
		t.Fatal("P on a session opens its plan")
	}
	pressShell(m, "esc")

	i := slices.IndexFunc(m.sessions, func(r row) bool { return r.live() && r.Transcript != nil })
	m.sessions[i].Live.Status = claude.StatusWaiting
	m.reindex()
	m.switchView(vInbox)
	m.refilter()
	m.cur[vInbox].pos = slices.IndexFunc(m.vis.inbox, func(it inboxItem) bool { return it.kind == kClaude })
	pressShell(m, "P")
	if m.plan == nil || m.plan.sess.ID != m.sessions[i].ID {
		t.Fatal("P on a waiting session in the inbox opens its plan")
	}
	pressShell(m, "esc")
	m.cur[vInbox].pos = slices.IndexFunc(m.vis.inbox, func(it inboxItem) bool { return it.kind != kClaude })
	pressShell(m, "P")
	if m.plan != nil {
		t.Error("P on anything else does nothing")
	}
}

func TestPlanForShell(t *testing.T) {
	m := newModelShell(t)
	p := filepath.Join(t.TempDir(), "plan.md")
	os.WriteFile(p, []byte(planMD), 0o600)
	rows := m.sessions
	rows[0].PlanPath = p
	fake(t, &listSessions, func(*claude.Store) []claude.Session {
		var out []claude.Session
		for _, r := range rows {
			out = append(out, r.Session)
		}
		return out
	})
	pane := rows[0].pane
	if pane == nil {
		t.Fatal("fixture session has no pane")
	}
	fake(t, &locatePane, func(_ *tmux.Locator, pid int) (tmux.Pane, bool) {
		if rows[0].Live != nil && pid == rows[0].Live.PID {
			return *pane, true
		}
		return tmux.Pane{}, false
	})
	got, err := PlanFor(pane.ID)
	if err != nil || got.plan == nil || !got.opts.PlanOnly || got.plan.sess.ID != rows[0].ID {
		t.Fatalf("plan for the pane's session: %v", err)
	}
	if _, err := PlanFor("%404"); !errors.Is(err, ErrNoSession) {
		t.Errorf("no session in the pane: %v", err)
	}
	rows[0].PlanPath, rows[0].Plan = "", ""
	if _, err := PlanFor(pane.ID); err == nil || err.Error() != "no plan in this session" {
		t.Errorf("no plan: %v", err)
	}
	_ = fmt.Sprint
}

func TestPlanBurstClosesAndUpwardSelectionShell(t *testing.T) {
	m := planModelShell(t)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("jqjj")})
	if m.plan != nil {
		t.Fatal("q in a burst closes the review; the rest is dropped")
	}

	m = planModelShell(t)
	m.width, m.height = 80, 9
	pressShell(m, "G", "V", "k", "k", "k", "c")
	p := m.plan
	if p.anchor != len(p.lines)-1 || p.cur != len(p.lines)-4 {
		t.Fatalf("selected upward: cur %d anchor %d", p.cur, p.anchor)
	}
	if s := screenShell(m); !strings.Contains(s, "comment on 4 lines") || !strings.Contains(s, "enter save") {
		t.Errorf("the box under the selection's last line stays on screen:\n%s", s)
	}
}

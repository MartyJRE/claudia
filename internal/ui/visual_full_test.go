package ui

import (
	"strings"
	"testing"

	"github.com/MartyJRE/claudia/internal/atlassian"
)

func TestVisualSelectAndHandOver(t *testing.T) {
	m := newModelShell(t)
	t.Setenv("TMUX", "/tmp/sock,1,0")
	started := fakeStart(t, "batch:1", nil)
	fakeSwitch(t, nil)
	m.switchView(vMine)

	if m.count() != 2 {
		t.Fatalf("fixture: two rows in My MRs, got %d", m.count())
	}
	pressShell(m, "V", "j", "j")
	lo, hi, ok := m.visualRange()
	if !ok || lo != 0 || hi != 1 {
		t.Fatalf("range %d..%d %v", lo, hi, ok)
	}
	s := plainShell(m.View())
	if strings.Count(s, " ▏ ") != 1 || !strings.Contains(s, "claude on these 2") || !strings.Contains(s, "esc cancel") {
		t.Errorf("selection isn't drawn:\n%s", s)
	}
	pressShell(m, "k")
	if _, hi, _ := m.visualRange(); hi != 0 {
		t.Errorf("k shrinks the range, hi %d", hi)
	}
	pressShell(m, "j", "x")
	if m.visual == nil {
		t.Error("other keys are ignored while selecting")
	}

	pressShell(m, "c")
	if m.visual != nil || m.prompt == nil || m.prompt.title != "what should Claude do with these 2?" {
		t.Fatalf("c asks what to do: %+v", m.prompt)
	}
	typeShell(m, "close all of these, I don't need them anymore at all, really")
	pressShell(m, "enter")
	if len(*started) != 1 {
		t.Fatalf("one session for the batch, got %d", len(*started))
	}
	sp := (*started)[0]
	prompt := sp.Args[len(sp.Args)-1]
	if sp.Kind != "batch" || sp.Session != batchSession || sp.Name != "batch: close all of these, I don't need them a…" {
		t.Errorf("spec %+v", sp)
	}
	if !strings.Contains(prompt, "I picked these 2 items") || !strings.Contains(prompt, m.vis.mrs[0].WebURL) ||
		!strings.Contains(prompt, m.vis.mrs[1].WebURL) || !strings.Contains(prompt, "What I want: close all of these") ||
		!strings.Contains(prompt, "wait for my go-ahead") {
		t.Errorf("prompt:\n%s", prompt)
	}
	if m.background {
		t.Error("c starts in the foreground")
	}
}

func TestVisualBackgroundAndLeaving(t *testing.T) {
	m := newModelShell(t)
	t.Setenv("TMUX", "/tmp/sock,1,0")
	started := fakeStart(t, "batch:1", nil)
	switched := fakeSwitch(t, nil)

	pressShell(m, "V", "C")
	typeShell(m, "snooze")
	pressShell(m, "enter")
	if len(*started) != 1 || len(*switched) != 0 || !strings.Contains(m.flash, "in the background") {
		t.Errorf("C hands over in the background: started %d switched %d flash %q", len(*started), len(*switched), m.flash)
	}

	pressShell(m, "V", "esc")
	if m.visual != nil {
		t.Error("esc leaves the selection")
	}
	pressShell(m, "V", "V")
	if m.visual != nil {
		t.Error("V again leaves it")
	}
	pressShell(m, "V")
	m.switchView(vReview)
	if m.visual != nil {
		t.Error("switching tabs drops the selection")
	}
	m.visual = &visualSel{view: vInbox}
	if _, _, ok := m.visualRange(); ok || m.inVisual(0) {
		t.Error("a selection made on another tab doesn't show here")
	}
	m.visual = nil

	m.switchView(vSessions)
	pressShell(m, "V")
	if m.visual != nil || m.flash != "nothing to select here" {
		t.Errorf("not on Sessions: %q", m.flash)
	}
	m.switchView(vReview)
	m.gl.Review = nil
	m.refilter()
	if pressShell(m, "V"); m.visual != nil {
		t.Error("nothing to select in an empty list")
	}
}

func TestSelectedItemsPerView(t *testing.T) {
	m := newModelShell(t)
	m.conf.Mentions = []atlassian.Page{{Title: "Plan", URL: "https://wiki/plan"}}
	m.conf.Mine = []atlassian.Page{{Title: "Notes", URL: "https://wiki/notes"}}
	m.atl = &atlassian.Client{Base: "https://acme.atlassian.net"}
	m.refilter()
	cases := []struct {
		v    view
		want string
	}{
		{vInbox, "("},
		{vReview, "open (https://"},
		{vJira, "(https://acme.atlassian.net/browse/"},
		{vMentions, "mention on "},
		{vConfluence, "page · "},
	}
	for _, c := range cases {
		m.switchView(c.v)
		n := m.count()
		if n == 0 {
			t.Fatalf("%s: fixture has no rows", viewNames[c.v])
		}
		m.cur[c.v].pos = 0
		m.visual = &visualSel{view: c.v, anchor: n - 1}
		items := m.selectedItems()
		if len(items) == 0 || !strings.Contains(strings.Join(items, "\n"), c.want) {
			t.Errorf("%s: items %q", viewNames[c.v], items)
		}
	}

	m.switchView(vMine)
	for i, r := range m.vis.mrs {
		if r.merged {
			m.cur[vMine].pos, m.visual = i, &visualSel{view: vMine, anchor: i}
			if items := m.selectedItems(); len(items) != 1 || !strings.Contains(items[0], "· merged (") {
				t.Errorf("merged MR item %q", items)
			}
			break
		}
	}

	m.switchView(vJira)
	for i, r := range m.vis.jira {
		if r.header() {
			m.cur[vJira].pos, m.visual = i, &visualSel{view: vJira, anchor: i}
			break
		}
	}
	if items := m.selectedItems(); len(items) != 0 {
		t.Errorf("a header alone is nothing to hand over: %q", items)
	}
	pressShell(m, "c")
	if m.prompt != nil || m.flash != "nothing to hand over in the selection" {
		t.Errorf("empty selection: %q", m.flash)
	}
	m.visual = nil
	if m.selectedItems() != nil {
		t.Error("no selection, no items")
	}

	m.atl = nil
	if m.ticketURL("ABC-1") != "ABC-1" {
		t.Error("without Jira the key itself")
	}
}

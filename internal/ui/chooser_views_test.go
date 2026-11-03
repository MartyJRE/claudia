package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/links"
)

func chooserModelViews(t *testing.T, names ...string) (*Model, *string) {
	m := newModelViews(t)
	var items []choice
	for _, n := range names {
		items = append(items, choice{label: n, hint: "hint " + n, value: n})
	}
	picked := new(string)
	m.chooser = newChooser("pick", items, func(m *Model, ch choice) (tea.Model, tea.Cmd) {
		*picked = ch.value.(string)
		return m, nil
	})
	return m, picked
}

func shownViews(c *chooser) string {
	var out []string
	for _, ch := range c.shown {
		out = append(out, ch.value.(string))
	}
	return strings.Join(out, " ")
}

func TestChooserNavigationAndPick(t *testing.T) {
	m, picked := chooserModelViews(t, "alpha", "beta", "gamma")
	pressViews(m, "up")
	if m.chooser.cur.pos != 0 {
		t.Error("up stops at the top")
	}
	pressViews(m, "down", "down", "down", "down")
	if m.chooser.cur.pos != 2 {
		t.Errorf("down stops at the bottom, pos %d", m.chooser.cur.pos)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	if m.chooser.cur.pos != 1 {
		t.Error("ctrl+p moves up")
	}
	pressViews(m, "enter")
	if m.chooser != nil || *picked != "beta" {
		t.Errorf("enter picks the selection, got %q", *picked)
	}
}

func TestChooserTypingFilters(t *testing.T) {
	m, picked := chooserModelViews(t, "payments", "billing", "pay-gateway", "people")
	pressViews(m, "down", "down")
	pressViews(m, "p", "a", "y")
	if got := shownViews(m.chooser); got != "payments pay-gateway" {
		t.Errorf("typing filters: %s", got)
	}
	if m.chooser.cur.pos != 0 {
		t.Error("typing resets the cursor")
	}
	pressViews(m, "z")
	if got := shownViews(m.chooser); got != "" {
		t.Errorf("no match: %s", got)
	}
	if !strings.Contains(screenViews(m), "pick › payz") {
		t.Error("the query shows")
	}
	pressViews(m, "enter")
	if m.chooser != nil || *picked != "" {
		t.Error("enter with nothing shown closes without picking")
	}
}

func TestChooserFuzzyRank(t *testing.T) {
	m, _ := chooserModelViews(t, "plan/abc", "x/pxlxaxn", "abc/plan", "planning")
	m.chooser.setQuery("plan")
	if got := shownViews(m.chooser); got != "abc/plan planning plan/abc x/pxlxaxn" {
		t.Errorf("exact base, prefix, path, fuzzy: %s", got)
	}
}

func TestChooserTabAndStatus(t *testing.T) {
	m, _ := chooserModelViews(t, "one", "two")
	n := 0
	m.chooser.tab = func(*Model, choice) { n++ }
	m.chooser.status = func(_ *Model, ch choice) string { return "profile: " + ch.value.(string) }
	pressViews(m, "tab", "tab")
	if n != 2 {
		t.Errorf("tab calls the option, %d times", n)
	}
	if s := screenViews(m); !strings.Contains(s, "tab profile: one") || !strings.Contains(s, "enter choose") {
		t.Errorf("footer:\n%s", s)
	}
	pressViews(m, "esc")
	if m.chooser != nil {
		t.Error("esc closes")
	}
}

func TestChooserView(t *testing.T) {
	var names []string
	for _, c := range "abcdefghijklmnopqrstuvwxyz" {
		names = append(names, "item-"+string(c))
	}
	m, _ := chooserModelViews(t, names...)
	s := plainViews(m.viewChooser(10))
	if !strings.Contains(s, "▌ item-a  hint item-a") || strings.Contains(s, "item-i") {
		t.Errorf("first page with the cursor on top:\n%s", s)
	}
	m.chooser.cur.pos = 20
	s = plainViews(m.viewChooser(10))
	if !strings.Contains(s, "▌ item-u") || strings.Contains(s, "item-m") || !strings.Contains(s, "item-n") {
		t.Errorf("scrolls to keep the cursor at the bottom:\n%s", s)
	}
	m.chooser.cur.pos = 2
	s = plainViews(m.viewChooser(10))
	if !strings.Contains(s, "▌ item-c") || !strings.Contains(s, "item-j") {
		t.Errorf("scrolls back up:\n%s", s)
	}

	m, _ = chooserModelViews(t)
	if !strings.Contains(plainViews(m.viewChooser(10)), "nothing to choose from") {
		t.Error("empty chooser")
	}
}

func TestFuzzy(t *testing.T) {
	cases := []struct {
		s, q string
		want bool
	}{
		{"payments", "pmt", true},
		{"payments", "", true},
		{"payments", "tmp", false},
		{"abc", "abcd", false},
	}
	for _, c := range cases {
		if got := fuzzy(c.s, c.q); got != c.want {
			t.Errorf("fuzzy(%q, %q) = %v", c.s, c.q, got)
		}
	}
}

func TestPathChoice(t *testing.T) {
	c := pathChoice("/code/Work/acme/pay", "/code", "main", 7)
	if c.search != "Work/acme/pay" || plainViews(c.label) != "Work/acme/pay" || c.hint != "main" || c.value != 7 {
		t.Errorf("%+v", c)
	}
}

func TestSeenChanges(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	base := gitlab.MR{WebURL: "u", SHA: "a1", UserNotesCount: 2, Pipeline: &gitlab.Pipeline{Status: "running"}, MergeStatus: "mergeable"}
	s, existed := loadSeen()
	if existed || len(s) != 0 {
		t.Fatal("fresh state")
	}
	chips := func(m gitlab.MR) string {
		var out []string
		for _, c := range s.changes(m) {
			out = append(out, c.text+map[int]string{0: "", 1: "!"}[c.kind])
		}
		return strings.Join(out, ",")
	}
	if got := chips(base); got != "new" {
		t.Errorf("unseen: %s", got)
	}
	s.mark(base)
	with := func(f func(*gitlab.MR)) gitlab.MR {
		m := base
		f(&m)
		return m
	}
	cases := []struct {
		name string
		mr   gitlab.MR
		want string
	}{
		{"unchanged", base, ""},
		{"new commits", with(func(m *gitlab.MR) { m.SHA = "b2" }), "new commits"},
		{"comments", with(func(m *gitlab.MR) { m.UserNotesCount = 5 }), "+3 comments"},
		{"fewer comments", with(func(m *gitlab.MR) { m.UserNotesCount = 1 }), ""},
		{"pipeline passed", with(func(m *gitlab.MR) { m.Pipeline = &gitlab.Pipeline{Status: "success"} }), "pipeline success"},
		{"pipeline failed", with(func(m *gitlab.MR) { m.Pipeline = &gitlab.Pipeline{Status: "failed"} }), "pipeline failed!"},
		{"pipeline gone", with(func(m *gitlab.MR) { m.Pipeline = nil }), ""},
		{"needs rebase", with(func(m *gitlab.MR) { m.MergeStatus = "need_rebase" }), "needs rebase!"},
		{"conflicts", with(func(m *gitlab.MR) { m.MergeStatus = "conflict" }), "conflicts!"},
		{"broken", with(func(m *gitlab.MR) { m.MergeStatus = "broken_status" }), "conflicts!"},
		{"other merge status", with(func(m *gitlab.MR) { m.MergeStatus = "checking" }), ""},
		{"several", with(func(m *gitlab.MR) { m.SHA = "c3"; m.UserNotesCount = 3 }), "new commits,+1 comments"},
	}
	for _, c := range cases {
		if got := chips(c.mr); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
	reloaded, existed := loadSeen()
	if !existed || reloaded["u"].SHA != "a1" || reloaded["u"].Pipeline != "running" {
		t.Errorf("mark is saved: %+v", reloaded)
	}
}

func TestIndexLinksTicketsAndMRs(t *testing.T) {
	mr := func(id int, url, branch, title string) gitlab.MR {
		return gitlab.MR{ProjectID: id, WebURL: url, SourceBranch: branch, Title: title}
	}
	a := mr(1, "a", "feat/ABC-1/x", "")
	b := mr(2, "b", "devel-x", "ABC-1: api side")
	c := mr(3, "c", "feat/ABC-1/x", "frontend")
	d := mr(1, "d", "chore/deps", "bump deps")
	gl := gitlab.Snapshot{Review: []gitlab.MR{a, d}, Mine: []gitlab.MR{b, a}, Merged: []gitlab.MR{c}}
	ix := buildIndex(nil, gl, atlassian.JiraSnapshot{Sprint: []atlassian.Issue{{Key: "ABC-1"}}, Team: []atlassian.Issue{{Key: "ABC-2"}}}, &links.Links{})
	var urls []string
	for _, m := range ix.ticketMRs["ABC-1"] {
		urls = append(urls, m.WebURL)
	}
	if strings.Join(urls, ",") != "a,b,c" {
		t.Errorf("MRs by branch or title, once each: %v", urls)
	}
	if len(ix.issues) != 2 || len(ix.mrs) != 4 {
		t.Errorf("issues %d mrs %d", len(ix.issues), len(ix.mrs))
	}
	if mrTicket(d) != "" || mrTicket(b) != "ABC-1" {
		t.Error("mrTicket")
	}

	var rel []string
	for _, m := range related(a, gl) {
		rel = append(rel, m.WebURL)
	}
	if strings.Join(rel, ",") != "a,b" {
		t.Errorf("related open MRs of the same ticket: %v", rel)
	}
	e := mr(4, "e", "chore/deps", "")
	gl.Mine = append(gl.Mine, e, mr(1, "f", "chore/deps", ""))
	rel = rel[:0]
	for _, m := range related(d, gl) {
		rel = append(rel, m.WebURL)
	}
	if strings.Join(rel, ",") != "d,e" {
		t.Errorf("same branch in another project: %v", rel)
	}
}

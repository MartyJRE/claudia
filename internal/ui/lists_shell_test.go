package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/gitlab"
)

// listRowsShell draws a groupable list as text: #header (+ when folded), an
// indented nested item, or a bare lone one.
func listRowsShell(m *Model, v view, name func(i int) string) string {
	var b []string
	for _, r := range m.listRows(v) {
		switch {
		case r.header():
			s := "#" + r.group.label
			if r.collapsed {
				s += "+"
			}
			b = append(b, s)
		case r.nested:
			b = append(b, " "+name(r.item))
		default:
			b = append(b, name(r.item))
		}
	}
	return strings.Join(b, "|")
}

func inboxNameShell(m *Model) func(int) string {
	return func(i int) string { return kindLabel[m.vis.inbox[i].kind] + ":" + m.vis.inbox[i].where }
}

func mrNameShell(m *Model) func(int) string {
	return func(i int) string { return fmt.Sprintf("!%d", m.vis.mrs[i].IID) }
}

func cursorToRowShell(t *testing.T, m *Model, v view, want string, name func(int) string) {
	t.Helper()
	for i, r := range m.listRows(v) {
		if !r.header() && name(r.item) == want || r.header() && "#"+r.group.label == want {
			m.cur[v].pos = i
			return
		}
	}
	t.Fatalf("%s not listed: %s", want, listRowsShell(m, v, name))
}

func TestInboxByKindShell(t *testing.T) {
	m := newModelShell(t)
	m.gl.Review = append(m.gl.Review, mrShell("ledger", 14, "fix: totals", "bob", "fix/totals", 5*time.Hour))
	m.reindex()
	name := inboxNameShell(m)
	flat := listRowsShell(m, vInbox, name)
	if strings.Contains(flat, "#") {
		t.Fatalf("the inbox starts as the plain list: %s", flat)
	}
	containsAllShell(t, "flat footer", screenShell(m), "v layout")
	lacksShell(t, "flat footer", screenShell(m), "space fold")

	cursorToRowShell(t, m, vInbox, "threads:billing !21", name)
	pressShell(m, "v")
	if m.flash != "layout: by kind" || loadListPrefs().Layout["inbox"] != 1 {
		t.Errorf("v switches and saves the layout: %q %+v", m.flash, loadListPrefs())
	}
	if got, want := listRowsShell(m, vInbox, name), "#review| review:ledger !14| review:payments !11|threads:billing !21|mention:ABC-1|claude:payments"; got != want {
		t.Fatalf("by kind:\n got %s\nwant %s", got, want)
	}
	if it, ok := m.currentInbox(); !ok || it.kind != kThreads {
		t.Errorf("the cursor stays on its item: %+v", it)
	}
	m.flash = ""
	s := screenShell(m)
	containsAllShell(t, "by kind", s, "everyone waiting on you · by kind", "▾ review  2 items", "space fold", "v layout")

	cursorToRowShell(t, m, vInbox, "review:payments !11", name)
	pressShell(m, " ")
	if got := listRowsShell(m, vInbox, name); !strings.HasPrefix(got, "#review+|threads") {
		t.Errorf("space on an item folds its group: %s", got)
	}
	if _, ok := m.currentGroup(vInbox); !ok {
		t.Error("the cursor moves to the folded header")
	}
	containsAllShell(t, "folded", screenShell(m), "▸ review  2 items", "2 items · space folds · - folds all · + opens all · v switches layout")
	if cmd := pressShell(m, "o"); cmd != nil {
		t.Error("o on a header opens nothing")
	}
	pressShell(m, " ")
	if got := listRowsShell(m, vInbox, name); strings.Contains(got, "+") {
		t.Errorf("space on the header opens it: %s", got)
	}

	pressShell(m, "-")
	if got := listRowsShell(m, vInbox, name); !strings.HasPrefix(got, "#review+") || len(loadListPrefs().Collapsed) != 1 {
		t.Errorf("- folds all: %s %+v", got, loadListPrefs())
	}
	m.filter.SetValue("ledger")
	m.refilter()
	if got := listRowsShell(m, vInbox, name); got != "review:ledger !14" {
		t.Errorf("a filter opens folds, and a group of one has no header: %s", got)
	}
	m.filter.SetValue("")
	m.refilter()
	pressShell(m, "+")
	if got := listRowsShell(m, vInbox, name); strings.Contains(got, "+") || len(m.listPrefs.Collapsed) != 0 {
		t.Errorf("+ opens all: %s", got)
	}

	m.cur[vInbox].pos = 3
	m.visual = &visualSel{view: vInbox, anchor: 0}
	if items := m.selectedItems(); len(items) != 3 || !strings.HasPrefix(items[2], "threads") {
		t.Errorf("a selection skips headers: %q", items)
	}
	m.visual = nil

	cursorToRowShell(t, m, vInbox, "threads:billing !21", name)
	pressShell(m, " ")
	if strings.Contains(listRowsShell(m, vInbox, name), "+") {
		t.Error("an item alone in its group has nothing to fold")
	}
	pressShell(m, "v")
	if m.flash != "layout: oldest first" || listRowsShell(m, vInbox, name) != flat {
		t.Errorf("v again is back to the plain list: %q", m.flash)
	}
	pressShell(m, " ")
	if len(m.listPrefs.Collapsed) != 0 {
		t.Error("the plain list has nothing to fold")
	}
}

func TestMRLayoutsShell(t *testing.T) {
	m := newModelShell(t)
	m.gl.Review = append(m.gl.Review, mrShell("payments", 15, "feat: more payments", "carol", "feat/more", 3*time.Hour))
	m.switchView(vReview)
	name := mrNameShell(m)
	pressShell(m, "v")
	if got, want := listRowsShell(m, vReview, name), "#g/payments| !11| !15|!12"; got != want {
		t.Errorf("review by repo:\n got %s\nwant %s", got, want)
	}
	containsAllShell(t, "review by repo", screenShell(m), "not yet approved by you · by repo", "▾ g/payments  2 MRs", "   !11 payments")
	cursorToRowShell(t, m, vReview, "#g/payments", name)
	containsAllShell(t, "repo detail", screenShell(m), "2 MRs · space folds")
	if cmd := pressShell(m, "enter"); cmd != nil {
		t.Error("enter on a header does nothing")
	}

	m.switchView(vMine)
	name = mrNameShell(m)
	if got := listRowsShell(m, vMine, name); got != "!21|!22" {
		t.Errorf("each view keeps its own layout: %s", got)
	}
	pressShell(m, "v", "v")
	if m.flash != "layout: by state" {
		t.Errorf("Mine's third layout: %q", m.flash)
	}
	containsAllShell(t, "mine by state", screenShell(m), "· by state", "pipeline failed › feat(ABC-2): invoices", "merged › chore: bump deps")

	// The cursor follows its item into a folded group, landing on the header.
	extra := mrShell("billing", 23, "feat: more invoices", "me", "feat/more", time.Hour)
	extra.Pipeline = &gitlab.Pipeline{Status: "failed"}
	m.gl.Mine = append(m.gl.Mine, extra)
	m.listPrefs.Collapsed = []string{"my-mrs:state:pipeline failed"}
	m.listPrefs.Layout["my-mrs"] = 1
	m.refilter()
	cursorToRowShell(t, m, vMine, "!21", name)
	pressShell(m, "v")
	if g, ok := m.currentGroup(vMine); !ok || g.group.label != "pipeline failed" {
		t.Errorf("cursor on %+v", m.listRows(vMine)[m.cur[vMine].pos])
	}
	pressShell(m, "+")
	if got := listRowsShell(m, vMine, name); got != "#pipeline failed| !21| !23|!22" {
		t.Errorf("+ opens the states: %s", got)
	}
	pressShell(m, "-")
	if got := listRowsShell(m, vMine, name); got != "#pipeline failed+|!22" {
		t.Errorf("- folds the states: %s", got)
	}
}

func TestMRStatesShell(t *testing.T) {
	m := newModelShell(t)
	mr := func(f func(*gitlab.MR)) mrRow {
		r := mrShell("x", 1, "t", "me", "b", 0)
		f(&r)
		return mrRow{MR: r}
	}
	m.vis.mrs = []mrRow{
		{MR: mrShell("x", 1, "t", "me", "b", 0), merged: true},
		mr(func(r *gitlab.MR) { r.Draft = true }),
		mr(func(r *gitlab.MR) { r.Pipeline = &gitlab.Pipeline{Status: "failed"} }),
		mr(func(r *gitlab.MR) { r.Threads = []gitlab.Thread{{}} }),
		mr(func(r *gitlab.MR) { r.MergeStatus = "conflict" }),
		mr(func(r *gitlab.MR) { r.Approved = true }),
		mr(func(r *gitlab.MR) {}),
	}
	var got []string
	for i := range m.vis.mrs {
		got = append(got, mrStateGroup(m, i).label)
	}
	if strings.Join(got, ",") != "merged,draft,pipeline failed,open threads,needs rebase,approved,in review" {
		t.Errorf("states: %v", got)
	}
}

func TestMentionsAndPagesGroupedShell(t *testing.T) {
	m := newModelShell(t)
	m.atlErr = nil
	m.jira.Mentions = append(m.jira.Mentions, atlassian.Mention{Key: "ABC-1", Summary: "Payments export", Author: "Gus", Text: "and this?", CommentID: "c3", Created: time.Now().Add(-2 * time.Hour)})
	m.conf = atlassian.ConfluenceSnapshot{
		Mentions: []atlassian.Page{{ID: "1", Title: "Runbook", Space: "OPS", Modified: time.Now().Add(-time.Hour)}},
		Mine: []atlassian.Page{
			{Title: "Deploys", Space: "OPS", Modified: time.Now().Add(-time.Hour)},
			{Title: "Roadmap", Space: "ENG", Modified: time.Now().Add(-2 * time.Hour)},
			{Title: "Oncall", Space: "OPS", Modified: time.Now().Add(-3 * time.Hour)},
		},
		FetchedAt: time.Now(),
	}
	m.reindex()

	m.switchView(vMentions)
	where := func(i int) string { return m.vis.mentions[i].where + ":" + m.vis.mentions[i].author }
	pressShell(m, "v")
	if got, want := listRowsShell(m, vMentions, where), "OPS:|#Payments export| ABC-1:Gus| ABC-1:Eve Smith"; got != want {
		t.Errorf("mentions by ticket/space:\n got %s\nwant %s", got, want)
	}
	containsAllShell(t, "mentions", screenShell(m), "last 30 days · by ticket/space", "   ▾ ABC-1 Payments export  2 mentions")
	cursorToRowShell(t, m, vMentions, "#Payments export", where)
	containsAllShell(t, "mention group detail", screenShell(m), "ABC-1 Payments export", "2 mentions · space folds")
	pressShell(m, "-")
	if got := listRowsShell(m, vMentions, where); got != "OPS:|#Payments export+" {
		t.Errorf("mentions fold: %s", got)
	}
	if cmd := pressShell(m, "o"); cmd != nil {
		t.Error("o on a header opens nothing")
	}

	m.switchView(vConfluence)
	title := func(i int) string { return m.vis.pages[i].Title }
	pressShell(m, "v")
	if got, want := listRowsShell(m, vConfluence, title), "#OPS| Deploys| Oncall|Roadmap"; got != want {
		t.Errorf("pages by space:\n got %s\nwant %s", got, want)
	}
	containsAllShell(t, "pages", screenShell(m), "pages you edited recently · by space", "▾ OPS  2 pages", "  OPS    Oncall")
	cursorToRowShell(t, m, vConfluence, "#OPS", title)
	containsAllShell(t, "page group detail", screenShell(m), "2 pages · space folds")
	if cmd := pressShell(m, "o"); cmd != nil {
		t.Error("o on a header opens nothing")
	}
	pressShell(m, "-")
	if got := listRowsShell(m, vConfluence, title); got != "#OPS+|Roadmap" {
		t.Errorf("pages fold: %s", got)
	}
	m.cur[vConfluence].pos = 1
	m.visual = &visualSel{view: vConfluence, anchor: 0}
	if items := m.selectedItems(); len(items) != 1 || !strings.Contains(items[0], "Roadmap") {
		t.Errorf("selection: %q", items)
	}
}

func TestListLayoutPrefsShell(t *testing.T) {
	m := newModelShell(t)
	m.listPrefs.Layout = map[string]int{"inbox": 9, "review": -1}
	if m.listLayout(vInbox).name != "oldest first" || m.listLayout(vReview).name != "flat" {
		t.Error("a saved layout out of range falls back to the first")
	}
	if l := m.listLayout(vJira); l.name != "" || l.group != nil {
		t.Error("views without layouts have none")
	}
	if m.listRows(vJira) != nil {
		t.Error("Jira isn't a groupable list")
	}
	m.view = vConfluence
	m.refilter()
	m.toggleListFold(vConfluence)
	if len(m.listPrefs.Collapsed) != 0 {
		t.Error("folding an empty list does nothing")
	}
}

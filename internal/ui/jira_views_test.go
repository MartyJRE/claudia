package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/gitlab"
)

func TestJiraViewRendersSprintsAndStatuses(t *testing.T) {
	m := jiraModelViews(t)
	if got, want := rowKeysViews(m.vis.jira), "#Sprint 2| ABC-1|  ABC-3|  ABC-2|#Sprint 3| ABC-4"; got != want {
		t.Fatalf("rows:\n got %s\nwant %s", got, want)
	}
	s := screenViews(m)
	for _, want := range []string{"Sprint 2 active", "Sprint 3 upcoming", "3 tickets", "Card form", "Refund flow", "Story", "Task", "In Progress", "my sprint + in progress · sprint › epic · by rank"} {
		if !strings.Contains(s, want) {
			t.Errorf("screen lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "Old bug") {
		t.Error("done issues show before d")
	}

	pressViews(m, "d")
	if !strings.Contains(screenViews(m), "Old bug") {
		t.Error("d should show done issues")
	}
	pressViews(m, "d")
	if strings.Contains(screenViews(m), "Old bug") {
		t.Error("second d should hide done issues again")
	}
}

func TestJiraTeamToggle(t *testing.T) {
	m := jiraModelViews(t)
	m.cur[vJira].pos = 3
	pressViews(m, "a")
	if !m.teamJira || m.cur[vJira].pos != 0 {
		t.Fatalf("a should switch to the team and reset the cursor, team=%v pos=%d", m.teamJira, m.cur[vJira].pos)
	}
	if got, want := rowKeysViews(m.vis.jira), "#Sprint 2| ABC-7"; got != want {
		t.Errorf("team rows: got %s want %s", got, want)
	}
	if s := screenViews(m); !strings.Contains(s, "team sprint") || !strings.Contains(s, "Bob") {
		t.Errorf("team screen:\n%s", s)
	}
	pressViews(m, "a")
	if m.teamJira {
		t.Error("a again goes back to mine")
	}
}

func TestJiraSortCycles(t *testing.T) {
	m := jiraModelViews(t)
	m.jiraPrefs.Group = groupFlat
	m.showDone = true
	m.refilter()
	keys := func() string {
		var ks []string
		for _, r := range m.vis.jira {
			ks = append(ks, r.issue.Key)
		}
		return strings.Join(ks, " ")
	}
	steps := []struct {
		name, flash, order string
	}{
		{"status", "sorted by status", "ABC-1 ABC-3 ABC-2 ABC-4 ABC-10"},
		{"recently updated", "sorted by recently updated", "ABC-3 ABC-10 ABC-2 ABC-1 ABC-4"},
		{"newest key", "sorted by newest key", "ABC-10 ABC-4 ABC-3 ABC-2 ABC-1"},
		{"rank", "sorted by rank", "ABC-1 ABC-3 ABC-2 ABC-4 ABC-10"},
	}
	for _, st := range steps {
		pressViews(m, "s")
		if m.flash != st.flash {
			t.Errorf("flash %q, want %q", m.flash, st.flash)
		}
		if got := keys(); got != st.order {
			t.Errorf("%s: got %s want %s", st.name, got, st.order)
		}
		if p := loadJiraPrefs(); sortNames[p.Sort] != st.name {
			t.Errorf("saved sort %d, want %s", p.Sort, st.name)
		}
	}
}

func TestJiraStatusSortWithinCategory(t *testing.T) {
	m := &Model{jiraPrefs: jiraPrefs{Sort: sortStatus, Group: groupFlat}}
	issues := []atlassian.Issue{
		{Key: "ABC-1", Status: "Testing", Category: "indeterminate"},
		{Key: "ABC-2", Status: "Backlog", Category: "new"},
		{Key: "ABC-3", Status: "Code review", Category: "indeterminate"},
	}
	var got []string
	for _, r := range m.buildJira(issues, false) {
		got = append(got, r.issue.Key)
	}
	if strings.Join(got, " ") != "ABC-3 ABC-1 ABC-2" {
		t.Errorf("got %v", got)
	}
}

func TestJiraLayoutCycles(t *testing.T) {
	m := jiraModelViews(t)
	pressViews(m, "v")
	if m.flash != "layout: epic" {
		t.Errorf("flash %q", m.flash)
	}
	if got, want := rowKeysViews(m.vis.jira), "ABC-1| ABC-3| ABC-2|ABC-4"; got != want {
		t.Errorf("epic layout: got %s want %s", got, want)
	}
	pressViews(m, "v")
	if got, want := rowKeysViews(m.vis.jira), "ABC-1|ABC-3|ABC-2|ABC-4"; got != want {
		t.Errorf("flat layout: got %s want %s", got, want)
	}
	if !strings.Contains(screenViews(m), "· flat ·") {
		t.Error("subtitle should name the layout")
	}
	pressViews(m, "v")
	if m.jiraPrefs.Group != groupSprint || loadJiraPrefs().Group != groupSprint {
		t.Error("third v returns to sprints and is saved")
	}
}

func TestJiraFolding(t *testing.T) {
	m := jiraModelViews(t)

	cursorToViews(t, m, "#Sprint 3")
	pressViews(m, " ")
	if got, want := rowKeysViews(m.vis.jira), "#Sprint 2| ABC-1|  ABC-3|  ABC-2|#Sprint 3+"; got != want {
		t.Errorf("fold header: got %s want %s", got, want)
	}
	if !strings.Contains(screenViews(m), "▸") {
		t.Error("folded rows draw ▸")
	}
	pressViews(m, " ")
	if strings.Contains(rowKeysViews(m.vis.jira), "+") {
		t.Errorf("space again unfolds: %s", rowKeysViews(m.vis.jira))
	}

	cursorToViews(t, m, "ABC-2")
	pressViews(m, " ")
	if got, want := rowKeysViews(m.vis.jira), "#Sprint 2| ABC-1+|#Sprint 3| ABC-4"; got != want {
		t.Errorf("space on a leaf folds its parent: got %s want %s", got, want)
	}
	if r := m.vis.jira[m.cur[vJira].pos]; r.issue.Key != "ABC-1" {
		t.Errorf("cursor should move to the folded parent, on %+v", r)
	}
	if s := screenViews(m); !strings.Contains(s, "Payments epic  (2)") {
		t.Errorf("folded parent shows its count:\n%s", s)
	}

	pressViews(m, "-")
	if got, want := rowKeysViews(m.vis.jira), "#Sprint 2+|#Sprint 3+"; got != want {
		t.Errorf("fold all: got %s want %s", got, want)
	}
	if p := loadJiraPrefs(); len(p.Collapsed) != 3 {
		t.Errorf("fold all saves every parent and sprint: %v", p.Collapsed)
	}
	pressViews(m, "+")
	if got, want := rowKeysViews(m.vis.jira), "#Sprint 2| ABC-1|  ABC-3|  ABC-2|#Sprint 3| ABC-4"; got != want {
		t.Errorf("unfold all: got %s want %s", got, want)
	}

	m.cur[vJira].pos = 0
	m.jiraPrefs.Collapsed = []string{"sprint:Sprint 2"}
	m.filter.SetValue("refund")
	m.refilter()
	if got := rowKeysViews(m.vis.jira); got != "#Sprint 2| (ABC-1)|  ABC-3" {
		t.Errorf("a filter unfolds and keeps parents of matches: %s", got)
	}
}

func TestJiraStatusChooser(t *testing.T) {
	m := jiraModelViews(t)
	pressViews(m, "f")
	if m.chooser == nil {
		t.Fatal("f opens the status chooser")
	}
	var labels []string
	for _, c := range m.chooser.shown {
		labels = append(labels, plainViews(c.label))
	}
	if got := strings.Join(labels, ","); got != "✓ In Progress,✓ To Do,✓ Done" {
		t.Errorf("statuses: %s", got)
	}
	pressViews(m, "down", "down", "up", "enter")
	if len(m.jiraPrefs.Hidden) != 1 || m.jiraPrefs.Hidden[0] != "To Do" {
		t.Fatalf("enter hides the status: %v", m.jiraPrefs.Hidden)
	}
	if m.chooser == nil || m.chooser.cur.pos != 1 || plainViews(m.chooser.shown[1].label) != "✗ To Do" {
		t.Fatalf("chooser stays open on the toggled status: %+v", m.chooser)
	}
	pressViews(m, "esc")
	if got := rowKeysViews(m.vis.jira); got != "#Sprint 2| ABC-1|  ABC-3" {
		t.Errorf("hidden status rows: %s", got)
	}
	if !strings.Contains(screenViews(m), "1 statuses hidden") {
		t.Error("subtitle counts hidden statuses")
	}

	// A hidden status stays listed even when no issue has it any more.
	m.jiraPrefs.Hidden = append(m.jiraPrefs.Hidden, "Gone")
	pressViews(m, "f")
	if n := len(m.chooser.all); n != 4 {
		t.Errorf("want 4 statuses incl. the stale hidden one, got %d", n)
	}
	m.chooser.setQuery("to do")
	pressViews(m, "enter")
	if len(m.jiraPrefs.Hidden) != 1 || m.jiraPrefs.Hidden[0] != "Gone" {
		t.Errorf("toggling again shows it: %v", m.jiraPrefs.Hidden)
	}
}

func TestJiraDetail(t *testing.T) {
	m := jiraModelViews(t)
	m.gl = gitlab.Snapshot{Mine: []gitlab.MR{{IID: 12, Title: "ABC-3 refunds", WebURL: "https://gitlab.example.com/acme/pay/-/merge_requests/12", State: "opened"}}}
	m.sessions = []row{
		{Session: claude.Session{ID: "session-one", Transcript: &claude.Transcript{Branch: "feat/ABC-3/refund"}, Live: &claude.Live{Status: claude.StatusWaiting}}},
		{Session: claude.Session{ID: "session-two", Transcript: &claude.Transcript{Branch: "feat/ABC-3/more"}}},
	}
	m.jira.Mentions = []atlassian.Mention{{Key: "ABC-3", CommentID: "1", Author: "Bob", Text: "?"}}
	m.reindex()

	cursorToViews(t, m, "ABC-3")
	s := screenViews(m)
	for _, want := range []string{"ABC-3 Refund flow", "In Progress  ·  Task  ·  Ann  ·  ABC-1 Payments epic", "!12 pay", "sessions:", "1 unanswered mention(s)", "✻ 2"} {
		if !strings.Contains(s, want) {
			t.Errorf("detail lacks %q:\n%s", want, s)
		}
	}

	cursorToViews(t, m, "#Sprint 2")
	s = screenViews(m)
	if !strings.Contains(s, "Sprint 2  ·  active") || !strings.Contains(s, "1 to do  1 in progress  1 done") {
		t.Errorf("sprint summary:\n%s", s)
	}
	m.teamJira = true
	m.refilter()
	if s := plainViews(m.detailSprint(m.vis.jira[0])); !strings.Contains(s, "0 to do  1 in progress  0 done") {
		t.Errorf("team sprint summary:\n%s", s)
	}
}

func TestJiraIssueSessions(t *testing.T) {
	m := newModelViews(t)
	m.ix.ticketSess = map[string][]string{"ABC-1": {"a", "b"}}
	m.sessions = []row{{Session: claude.Session{ID: "a"}}, {Session: claude.Session{ID: "b"}}}
	if got := plainViews(m.issueSessions("ABC-1")); got != "✻ 2" {
		t.Errorf("got %q", got)
	}
	if m.issueSessions("ABC-2") != "" {
		t.Error("no sessions, no robot")
	}
	m.ix.ticketMRs = map[string][]gitlab.MR{"ABC-1": {{IID: 3}, {IID: 4, State: "merged"}}}
	if got := plainViews(m.issueMRs("ABC-1")); got != "!3 !4" {
		t.Errorf("mrs %q", got)
	}
}

func TestJiraEmptyStates(t *testing.T) {
	m := newModelViews(t)
	m.view = vJira
	m.refilter()
	if !strings.Contains(screenViews(m), "asking Jira") {
		t.Error("before the first fetch the list says it's asking")
	}
	m.jira.FetchedAt = time.Now()
	if !strings.Contains(screenViews(m), "nothing in your sprint") {
		t.Error("empty sprint message")
	}
	m.atl, m.atlErr = nil, errors.New("set atlassian_url")
	if !strings.Contains(screenViews(m), "set atlassian_url") {
		t.Error("without a client the error shows")
	}
	if _, cmd := m.keyJira("o"); cmd != nil {
		t.Error("issue actions need a client")
	}
}

func TestGotoTicket(t *testing.T) {
	m := jiraModelViews(t)
	m.view = vMentions
	m.jiraPrefs.Collapsed = []string{"sprint:Sprint 2", "ABC-1"}
	m.filter.SetValue("zzz")
	m.gotoTicket("ABC-3")
	if m.view != vJira || m.teamJira || m.filter.Value() != "" {
		t.Fatalf("view=%d team=%v filter=%q", m.view, m.teamJira, m.filter.Value())
	}
	if is, ok := m.currentIssue(); !ok || is.Key != "ABC-3" {
		t.Errorf("cursor on %+v", is)
	}
	if len(m.jiraPrefs.Collapsed) != 0 {
		t.Errorf("going to a ticket unfolds its sprint and parents: %v", m.jiraPrefs.Collapsed)
	}

	m.gotoTicket("ABC-7")
	if !m.teamJira {
		t.Error("a team-only ticket switches to the team list")
	}
	if is, _ := m.currentIssue(); is.Key != "ABC-7" {
		t.Errorf("cursor on %s", is.Key)
	}

	if _, cmd := m.gotoTicket("ABC-99"); cmd == nil || !strings.Contains(m.flash, "ABC-99 isn't in the sprint lists") {
		t.Errorf("unknown ticket opens in the browser, flash %q", m.flash)
	}
	m.gotoTicket("")
	if m.flash != "no ticket linked" || !m.flashErr {
		t.Errorf("flash %q", m.flash)
	}
	m.atl = nil
	if _, cmd := m.gotoTicket("ABC-99"); cmd != nil {
		t.Error("nothing to open without a client")
	}
}

func TestJiraClaudePicksIntent(t *testing.T) {
	m := jiraModelViews(t)
	cases := []struct{ key, fits string }{
		{"ABC-2", "Analysis"},
		{"ABC-3", "Continue"},
	}
	for _, c := range cases {
		cursorToViews(t, m, c.key)
		pressViews(m, "c")
		if m.chooser == nil || m.chooser.title != "session for "+c.key {
			t.Fatalf("%s: c opens the intent picker", c.key)
		}
		it, _ := m.chooser.selected()
		if it.label != c.fits || it.hint != "fits the ticket's state" {
			t.Errorf("%s: selected %q (%q)", c.key, it.label, it.hint)
		}
		pressViews(m, "esc")
	}
	cursorToViews(t, m, "ABC-2")
	pressViews(m, "enter")
	if m.chooser == nil || m.chooser.title != "session for ABC-2" {
		t.Error("enter without sessions starts claude for the ticket")
	}
}

func TestJiraTransitions(t *testing.T) {
	m := jiraModelViews(t)
	m.Update(transitionsMsg{key: "ABC-2", err: errors.New("403")})
	if m.flash != "403" || m.chooser != nil {
		t.Errorf("an error flashes: %q", m.flash)
	}
	m.Update(transitionsMsg{key: "ABC-2", ts: []atlassian.Transition{{ID: "11", Name: "Start", To: "In Progress"}, {ID: "31", Name: "Done", To: "Done"}}})
	if m.chooser == nil || m.chooser.title != "move ABC-2" {
		t.Fatal("transitions open a chooser")
	}
	var labels []string
	for _, c := range m.chooser.shown {
		labels = append(labels, plainViews(c.label))
	}
	if got := strings.Join(labels, ","); got != "Start → In Progress,Done" {
		t.Errorf("labels %s", got)
	}
}

func TestJiraImagesMsg(t *testing.T) {
	m := jiraModelViews(t)
	if _, cmd := m.Update(imagesMsg{key: "ABC-2"}); cmd != nil || m.flash != "ABC-2 has no image attachments" {
		t.Errorf("flash %q", m.flash)
	}
	if _, cmd := m.Update(imagesMsg{key: "ABC-2", files: []string{"/tmp/a.png"}}); cmd == nil || m.flash != "" {
		t.Errorf("images hand over to the viewer, flash %q", m.flash)
	}
}

func TestJiraHelpers(t *testing.T) {
	for k, want := range map[string]int{"ABC-12": 12, "X-1": 1, "LONG-KEY-7": 7, "ABC-x": 0} {
		if got := keyNum(k); got != want {
			t.Errorf("keyNum(%s) = %d", k, got)
		}
	}
	for in, want := range map[string]string{"Úkol": "Task", "Podúloha": "Subtask", "Chyba": "Bug", "Epos": "Epic", "Příběh": "Story", "Spike": "Spike"} {
		if got := typeName(in); got != want {
			t.Errorf("typeName(%s) = %s", in, got)
		}
	}
	if got := plainViews(statusText(atlassian.Issue{Status: "Done", Category: "done"})); got != "Done" {
		t.Errorf("statusText %q", got)
	}
}

func TestJiraContextParent(t *testing.T) {
	m := &Model{}
	issues := []atlassian.Issue{
		{Key: "ABC-5", Summary: "child", Parent: "ABC-9", ParentSummary: "their epic", ParentType: "Epic", ParentStatus: "Open", Category: "new"},
	}
	rows := m.buildJira(issues, false)
	if got := rowKeysViews(rows); got != "#No sprint| (ABC-9)|  ABC-5" {
		t.Fatalf("rows %s", got)
	}
	ctx := rows[1]
	if ctx.issue.Summary != "their epic" || ctx.issue.Level != 1 || ctx.count != 1 {
		t.Errorf("context parent %+v", ctx)
	}
	m.jiraPrefs.Collapsed = []string{"ABC-9"}
	rows = m.buildJira(issues, false)
	if got := plainViews(m.jiraSummaryCell(rows[1], false)); got != "their epic  (1)" {
		t.Errorf("folded context counts its children: %q", got)
	}
	if got := plainViews(m.jiraKeyCell(rows[1])); got != "  ▸ ABC-9" {
		t.Errorf("key cell %q", got)
	}
}

func TestJiraSprintOrder(t *testing.T) {
	m := &Model{}
	sp := func(id int, name, state string) *atlassian.Sprint {
		return &atlassian.Sprint{ID: id, Name: name, State: state}
	}
	issues := []atlassian.Issue{
		{Key: "ABC-1", Sprint: sp(1, "Old", "closed")},
		{Key: "ABC-2"},
		{Key: "ABC-3", Sprint: sp(5, "Later", "future")},
		{Key: "ABC-4", Sprint: sp(4, "Soon", "future")},
		{Key: "ABC-5", Sprint: sp(3, "Now", "active")},
	}
	if got, want := rowKeysViews(m.buildJira(issues, false)), "#Now| ABC-5|#Soon| ABC-4|#Later| ABC-3|#No sprint| ABC-2|#Old| ABC-1"; got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}

func TestJiraIssueActions(t *testing.T) {
	m := jiraModelViews(t)
	cursorToViews(t, m, "ABC-2")
	for _, k := range []string{"o", "y", "m", "i"} {
		if _, cmd := m.Update(keyViews(k)); cmd == nil {
			t.Errorf("%s returns a command", k)
		}
	}
	if _, cmd := m.Update(keyViews("e")); cmd == nil || m.flash != "exporting ABC-2…" {
		t.Errorf("e exports the brief, flash %q", m.flash)
	}
	pressViews(m, "b")
	if m.chooser == nil || m.chooser.title != "ABC-2 in" {
		t.Errorf("b picks a repo for a summary session: %+v", m.chooser)
	}
	pressViews(m, "esc")

	m.Update(transitionsMsg{key: "ABC-2", ts: []atlassian.Transition{{ID: "11", Name: "Start", To: "In Progress"}}})
	if _, cmd := m.Update(keyViews("enter")); cmd == nil || m.chooser != nil {
		t.Error("picking a transition sends it")
	}

	cursorToViews(t, m, "#Sprint 2")
	if _, cmd := m.Update(keyViews("o")); cmd != nil {
		t.Error("a sprint header has nothing to open")
	}
}

func TestNoSprintSummarySkipsEpicsShownElsewhere(t *testing.T) {
	m := newModelViews(t)
	s2 := &atlassian.Sprint{ID: 2, Name: "Sprint 2", State: "active"}
	m.jira.Sprint = []atlassian.Issue{
		{Key: "ABC-1", Summary: "epic", Level: 1, Category: "indeterminate"},
		{Key: "ABC-2", Summary: "story", Parent: "ABC-1", Sprint: s2, Category: "new"},
		{Key: "ABC-3", Summary: "loose", Category: "new"},
	}
	if s := plainViews(m.detailSprint(jrow{sprint: "No sprint"})); !strings.Contains(s, "1 to do  0 in progress  0 done") {
		t.Errorf("the epic sits under Sprint 2, not No sprint:\n%s", s)
	}
}

package ui

import (
	"strings"
	"testing"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/repos"
)

func TestJiraTree(t *testing.T) {
	active := &atlassian.Sprint{ID: 2, Name: "S2", State: "active"}
	next := &atlassian.Sprint{ID: 3, Name: "S3", State: "future"}
	issues := []atlassian.Issue{
		{Key: "ABC-1", Summary: "epic", Level: 1, Category: "indeterminate"},
		{Key: "ABC-2", Summary: "story", Parent: "ABC-1", Sprint: active, Category: "new"},
		{Key: "ABC-3", Summary: "sub", Parent: "ABC-2", Level: -1, Sprint: active, Category: "new"},
		{Key: "ABC-4", Summary: "later", Parent: "ABC-9", ParentSummary: "other epic", ParentType: "Epic", Sprint: next},
		{Key: "ABC-5", Summary: "loose", Category: "indeterminate"},
	}
	render := func(rows []jrow) string {
		var b []string
		for _, r := range rows {
			s := strings.Repeat(" ", r.depth)
			switch {
			case r.header():
				s += "#" + r.sprint
			case r.context:
				s += "(" + r.issue.Key + ")"
			default:
				s += r.issue.Key
			}
			if r.collapsed {
				s += "+"
			}
			b = append(b, s)
		}
		return strings.Join(b, "|")
	}
	m := &Model{}
	got := render(m.buildJira(issues, false))
	want := "#S2| ABC-1|  ABC-2|   ABC-3|#S3| (ABC-9)|  ABC-4|#No sprint| ABC-5"
	if got != want {
		t.Errorf("tree:\n got %s\nwant %s", got, want)
	}

	m.jiraPrefs.Collapsed = []string{"ABC-1", "sprint:S3"}
	if got, want := render(m.buildJira(issues, false)), "#S2| ABC-1+|#S3+|#No sprint| ABC-5"; got != want {
		t.Errorf("folded:\n got %s\nwant %s", got, want)
	}
	if got := render(m.buildJira(issues, true)); !strings.Contains(got, "ABC-3") {
		t.Errorf("filtering must unfold: %s", got)
	}
}

func TestSessionTree(t *testing.T) {
	sess := func(id, dir, status string, started int64) row {
		r := row{Session: claude.Session{ID: id, Transcript: &claude.Transcript{Cwd: dir}}}
		if status != "" {
			r.Live = &claude.Live{SessionID: id, Status: claude.Status(status), StartedAt: started}
		}
		return r
	}
	rows := []row{sess("a", "/c/pay", "", 0), sess("b", "/c/bill", "waiting", 1), sess("c", "/c/pay/sub", "busy", 2)}
	m := &Model{repos: []repos.Repo{{Dir: "/c/pay"}, {Dir: "/c/bill"}}}
	m.ix.sessionTicket = map[string]string{"a": "ABC-2", "c": "ABC-2"}
	m.ix.issues = map[string]atlassian.Issue{"ABC-2": {Key: "ABC-2", Summary: "story", Parent: "ABC-1"}, "ABC-1": {Key: "ABC-1", Summary: "epic", Level: 1}}
	render := func(rs []srow) string {
		var b []string
		for _, r := range rs {
			if r.isSess {
				b = append(b, strings.Repeat(" ", r.depth)+r.sess.ID)
			} else {
				b = append(b, strings.Repeat(" ", r.depth)+"#"+r.title)
			}
		}
		return strings.Join(b, "|")
	}
	// Live groups by when their newest session started; waiting doesn't move them.
	if got, want := render(m.buildSessions(rows, false)), "#pay| a| c|#bill| b"; got != want {
		t.Errorf("by repo: got %s want %s", got, want)
	}
	rows[1].Live.UpdatedAt = 1e12
	if got, want := render(m.buildSessions(rows, false)), "#pay| a| c|#bill| b"; got != want {
		t.Errorf("activity reordered: got %s want %s", got, want)
	}
	m.sessPrefs.Group = sessByTicket
	if got, want := render(m.buildSessions(rows, false)), "#epic| #story|  a|  c|#No ticket| b"; got != want {
		t.Errorf("by ticket: got %s want %s", got, want)
	}
}

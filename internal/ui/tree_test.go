package ui

import (
	"strings"
	"testing"

	"github.com/MartyJRE/claudia/internal/atlassian"
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

package ui

import (
	"strings"
	"testing"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/links"
)

func TestClaudeForTicketPicksIntent(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	review := gitlab.MR{WebURL: "r", IID: 1, SourceBranch: "feat/ABC-1/x", References: struct {
		Full string `json:"full"`
	}{"g/payments!1"}}
	mine := gitlab.MR{WebURL: "m", IID: 2, SourceBranch: "feat/ABC-2/x", Threads: []gitlab.Thread{{Body: "nit"}}, References: struct {
		Full string `json:"full"`
	}{"g/billing!2"}}
	gl := gitlab.Snapshot{Me: gitlab.User{Username: "me"}, Review: []gitlab.MR{review}, Mine: []gitlab.MR{mine}}
	js := atlassian.JiraSnapshot{Sprint: []atlassian.Issue{
		{Key: "ABC-3", Category: "new", Status: "Ready for analysis"},
		{Key: "ABC-4", Category: "indeterminate", Status: "Ready for testing"},
		{Key: "ABC-5", Category: "indeterminate", Status: "Dev in progress", Mine: true},
		{Key: "ABC-6", Category: "indeterminate", Status: "Code review"},
	}}
	for key, want := range map[string]string{
		"ABC-1": "review payments in",
		"ABC-2": "fix billing in",
		"ABC-3": "analyse ABC-3 in",
		"ABC-4": "test plan for ABC-4 in",
		"ABC-5": "continue ABC-5 in",
		"ABC-6": "ABC-6 in",
	} {
		m := &Model{gl: gl, jira: js, links: &links.Links{}, seen: seenStore{}, store: claude.NewStore([]claude.Profile{{Name: "work"}}, 0)}
		m.ix = buildIndex(nil, gl, js, m.links)
		m.claudeForTicket(key, "")
		it, ok := m.chooser.selected()
		if !ok {
			t.Fatalf("%s: no intent preselected", key)
		}
		m.chooser.pick(m, it)
		if m.chooser == nil || !strings.HasPrefix(m.chooser.title, want) {
			got := "<none>"
			if m.chooser != nil {
				got = m.chooser.title
			}
			t.Errorf("%s: chooser %q, want %q", key, got, want)
		}
	}
}

func TestSessionTitle(t *testing.T) {
	m := &Model{}
	m.ix.Issues = map[string]atlassian.Issue{"ABC-1": {Key: "ABC-1", Summary: "Expose gender on suggestions"}}
	for _, c := range []struct{ verb, key, fallback, want string }{
		{"review", "ABC-1", "feat(ABC-1): add gender", "review: Expose gender on suggestions"},
		{"review", "", "Draft: fix(api)!: drop the old routes", "review: drop the old routes"},
		{"fix threads", "", "", "fix threads: "},
	} {
		if got := m.sessionTitle("review", c.verb, c.key, c.fallback); got != c.want {
			t.Errorf("%q: got %q", c.want, got)
		}
	}
}

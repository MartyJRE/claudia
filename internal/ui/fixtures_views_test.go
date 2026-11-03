package ui

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/links"
)

const atlBaseViews = "https://acme.atlassian.net"

// config.Get is read once per process, so the site URL has to be in place
// before any test touches it; credentials are dropped so no client goes online.
func init() {
	os.Setenv("ATLASSIAN_BASE_URL", atlBaseViews)
	os.Unsetenv("ATLASSIAN_EMAIL")
	os.Unsetenv("ATLASSIAN_API_TOKEN")
}

func newModelViews(t *testing.T) *Model {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	km, _ := newKeymap(nil)
	f := textinput.New()
	return &Model{
		km:        km,
		filter:    f,
		store:     &claude.Store{},
		links:     &links.Links{},
		atl:       &atlassian.Client{Base: atlBaseViews},
		dismissed: dismissed{},
		seen:      seenStore{},
		width:     160,
		height:    40,
	}
}

func jiraFixtureViews() atlassian.JiraSnapshot {
	active := &atlassian.Sprint{ID: 2, Name: "Sprint 2", State: "active"}
	next := &atlassian.Sprint{ID: 3, Name: "Sprint 3", State: "future"}
	now := time.Now()
	return atlassian.JiraSnapshot{
		Sprint: []atlassian.Issue{
			{Key: "ABC-1", Summary: "Payments epic", Type: "Epos", Status: "In Progress", Category: "indeterminate", Level: 1},
			{Key: "ABC-2", Summary: "Card form", Type: "Příběh", Status: "To Do", Category: "new", Parent: "ABC-1", Sprint: active, Assignee: "Ann", Updated: now.Add(-3 * time.Hour)},
			{Key: "ABC-3", Summary: "Refund flow", Type: "Úkol", Status: "In Progress", Category: "indeterminate", Parent: "ABC-1", Sprint: active, Assignee: "Ann", Mine: true, Updated: now.Add(-time.Hour)},
			{Key: "ABC-10", Summary: "Old bug", Type: "Chyba", Status: "Done", Category: "done", Sprint: active, Updated: now.Add(-2 * time.Hour)},
			{Key: "ABC-4", Summary: "Next thing", Type: "Úkol", Status: "To Do", Category: "new", Sprint: next},
		},
		Team: []atlassian.Issue{
			{Key: "ABC-7", Summary: "Team story", Type: "Příběh", Status: "In Review", Category: "indeterminate", Sprint: active, Assignee: "Bob"},
		},
		FetchedAt: now,
	}
}

func jiraModelViews(t *testing.T) *Model {
	m := newModelViews(t)
	m.jira = jiraFixtureViews()
	m.view = vJira
	m.reindex()
	return m
}

func keyViews(k string) tea.KeyMsg {
	switch k {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	case "ctrl+u":
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

func pressViews(m *Model, keys ...string) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		_, cmd = m.Update(keyViews(k))
	}
	return cmd
}

func screenViews(m *Model) string { return ansi.Strip(m.View()) }

func plainViews(s string) string { return ansi.Strip(s) }

// rowKeysViews renders the jira rows compactly: headers as #name, context
// parents in parentheses, folded rows with a trailing +.
func rowKeysViews(rows []jrow) string {
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

func cursorToViews(t *testing.T, m *Model, key string) {
	t.Helper()
	for i, r := range m.vis.jira {
		if !r.header() && r.issue.Key == key || r.header() && "#"+r.sprint == key {
			m.cur[vJira].pos = i
			return
		}
	}
	t.Fatalf("%s not visible: %s", key, rowKeysViews(m.vis.jira))
}

func jiraMentionViews(id, key string, answered bool) atlassian.Mention {
	return atlassian.Mention{
		Key: key, Summary: "about " + key, Author: "Bob Builder", CommentID: id, Answered: answered,
		Text: "can you check " + key, URL: atlBaseViews + "/browse/" + key + "?focusedCommentId=" + id,
		Created: time.Now().Add(-time.Hour),
	}
}

func confMentionViews() atlassian.Page {
	return atlassian.Page{
		ID: "42", Title: "Runbook", Space: "OPS", URL: atlBaseViews + "/wiki/spaces/OPS/pages/42",
		Excerpt: "@@@hl@@@Martin@@@endhl@@@ please review &amp; sign&hellip;", Modified: time.Now().Add(-2 * time.Hour),
	}
}

package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/repos"
	"github.com/MartyJRE/claudia/internal/state"
)

func TestOpenLinkRoutes(t *testing.T) {
	mrURL := "https://" + gitlab.Host() + "/acme/pay/-/merge_requests/5"
	cases := []struct {
		name, link string
		chooser    string
		flash      string
		cmd        bool
	}{
		{"unknown MR is loaded", mrURL, "", "loading acme/pay!5…", true},
		{"MR sub-page", mrURL + "/diffs", "", "loading acme/pay!5…", true},
		{"Jira ticket", atlBaseViews + "/browse/ABC-2", "session for ABC-2", "", false},
		{"Jira board with a selected issue", atlBaseViews + "/jira/software/projects/ABC/boards/1?selectedIssue=ABC-77", "session for ABC-77", "", false},
		{"Atlassian page without a key", atlBaseViews + "/wiki/spaces/OPS", "take a look in", "", false},
		{"Confluence page titled like a key", atlBaseViews + "/wiki/spaces/OPS/pages/42/RFC-2119+notes", "take a look in", "", false},
		{"MR on another host", "https://gitlab.other.org/acme/pay/-/merge_requests/5", "take a look in", "", false},
		{"anything else", "https://example.com/a/b", "take a look in", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := jiraModelViews(t)
			m.repos = []repos.Repo{{Dir: "/code/pay"}}
			_, cmd := m.openLink(c.link)
			if (cmd != nil) != c.cmd {
				t.Errorf("cmd = %v", cmd != nil)
			}
			if c.flash != "" && m.flash != c.flash {
				t.Errorf("flash %q", m.flash)
			}
			title := ""
			if m.chooser != nil {
				title = m.chooser.title
			}
			if title != c.chooser {
				t.Errorf("chooser %q, want %q", title, c.chooser)
			}
		})
	}
}

func TestOpenLinkKnownMR(t *testing.T) {
	m := jiraModelViews(t)
	mr := gitlab.MR{IID: 5, Title: "ABC-2 card form", WebURL: "https://" + gitlab.Host() + "/acme/pay/-/merge_requests/5", SourceBranch: "feat/ABC-2"}
	m.gl.Review = []gitlab.MR{mr}
	_, cmd := m.openLink(mr.WebURL + "/diffs")
	if cmd != nil {
		t.Error("a known MR isn't fetched again")
	}
	if m.chooser == nil || m.chooser.title != "review pay in" {
		t.Errorf("without a clone the review asks for a repo: %+v", m.chooser)
	}
	if _, ok := m.seen[mr.WebURL]; !ok {
		t.Error("reviewing marks the MR seen")
	}

	m.chooser = nil
	m.Update(mrLoadedMsg{mr: mr})
	if m.chooser == nil || m.chooser.title != "review pay in" {
		t.Error("a loaded MR goes to review")
	}
}

func TestOpenLinkTakesSummary(t *testing.T) {
	m := jiraModelViews(t)
	m.openLink(atlBaseViews + "/browse/ABC-3")
	it, _ := m.chooser.selected()
	if it.label != "Continue" {
		t.Errorf("an in-progress ticket of mine fits continue, got %q", it.label)
	}
}

func TestOpenRequestRoundTrip(t *testing.T) {
	m := newModelViews(t)
	if _, ok := m.takeOpenRequest(); ok {
		t.Fatal("no request yet")
	}
	if err := RequestOpen("https://example.com/x"); err != nil {
		t.Fatal(err)
	}
	link, ok := m.takeOpenRequest()
	if !ok || link != "https://example.com/x" {
		t.Errorf("got %q %v", link, ok)
	}
	if _, ok := m.takeOpenRequest(); ok {
		t.Error("a request is taken once")
	}
	if err := state.Save(openRequestFile, map[string]string{"link": ""}); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.takeOpenRequest(); ok {
		t.Error("an empty link is no request")
	}
}

func TestShortLink(t *testing.T) {
	cases := map[string]string{
		"https://example.com/x":              "example.com/x",
		"http://example.com":                 "example.com",
		"https://" + strings.Repeat("a", 50): strings.Repeat("a", 39) + "…",
		"ftp://x":                            "ftp://x",
	}
	for in, want := range cases {
		if got := shortLink(in); got != want {
			t.Errorf("shortLink(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPrompt(t *testing.T) {
	m := newModelViews(t)
	var got string
	newPrompt := func() {
		m.prompt = &promptBox{title: "open a link in Claude", in: newInput("link › ", "GitLab MR"), submit: func(m *Model, s string) (tea.Model, tea.Cmd) {
			got = s
			return m, nil
		}}
		m.prompt.in.Focus()
	}
	newPrompt()
	if s := screenViews(m); !strings.Contains(s, "open a link in Claude") || !strings.Contains(s, "an MR gets reviewed") || !strings.Contains(s, "enter open") {
		t.Errorf("prompt screen:\n%s", s)
	}
	pressViews(m, "enter")
	if m.prompt != nil || got != "" {
		t.Error("enter on an empty prompt just closes it")
	}
	newPrompt()
	pressViews(m, " ", "h", "i", " ", "enter")
	if m.prompt != nil || got != "hi" {
		t.Errorf("submitted %q", got)
	}
	newPrompt()
	pressViews(m, "x", "esc")
	if m.prompt != nil || got != "hi" {
		t.Error("esc cancels")
	}
}

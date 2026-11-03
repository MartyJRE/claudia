package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/links"
	"github.com/MartyJRE/claudia/internal/repos"
)

func TestBriefTextShell(t *testing.T) {
	if got := briefRef("ABC-1", "", errors.New("x")); got != "Read the Jira issue ABC-1 with the Atlassian MCP, including its comments" {
		t.Errorf("no export: %q", got)
	}
	if got := briefRef("ABC-1", "/tmp/ABC-1.md", nil); !strings.Contains(got, "exported to /tmp/ABC-1.md") {
		t.Errorf("export: %q", got)
	}
	if got := fillBrief("do {key} from {brief}", "ABC-1", "/b.md", nil); got != "do ABC-1 from /b.md" {
		t.Errorf("fillBrief: %q", got)
	}
	if got := fillBrief("{brief}", "ABC-1", "", errors.New("x")); got != "Jira (use the Atlassian MCP)" {
		t.Errorf("fillBrief without export: %q", got)
	}
	for in, want := range map[string]string{"Eve Smith": "Eve", "eve.smith": "eve", "eve": "eve"} {
		if got := firstName(in); got != want {
			t.Errorf("firstName(%q) = %q", in, got)
		}
	}
}

func TestSessionTitleCutShell(t *testing.T) {
	m := &Model{}
	m.ix.issues = map[string]atlassian.Issue{"ABC-1": {Key: "ABC-1", Summary: strings.Repeat("é", 60)}}
	got := m.sessionTitle("x", "review", "ABC-1", "")
	if want := "review: " + strings.Repeat("é", 49) + "…"; got != want {
		t.Errorf("long summaries are cut on runes: %q", got)
	}
	if got := m.sessionTitle("x", "about", "ABC-9", ""); got != "about: ABC-9" {
		t.Errorf("nothing else known, the key: %q", got)
	}
}

func TestMRListShell(t *testing.T) {
	m := newModelShell(t)
	if got := m.mrList("ABC-1"); got != " ("+m.gl.Review[0].WebURL+")" {
		t.Errorf("mrList: %q", got)
	}
	if got := m.mrList("ABC-404"); got != "" {
		t.Errorf("no MRs: %q", got)
	}
}

func TestTicketSessionStartsLinkedShell(t *testing.T) {
	m := newModelShell(t)
	m.atlErr = errors.New("no Atlassian token")
	m.repos = []repos.Repo{{Dir: "/code/ledger", Branch: "devel"}, {Dir: "/code/payments", Branch: "feat/ABC-1/export", Project: "g/payments"}}
	m.briefSession("ABC-1", "")
	if m.chooser == nil || m.chooser.title != "ABC-1 in" {
		t.Fatal("briefSession picks a repo first")
	}
	first, _ := m.chooser.selected()
	if first.value != "/code/payments" || first.hint != "on feat/ABC-1/export" {
		t.Errorf("the repo on the ticket's branch comes first: %+v", first)
	}
	cmd := pressShell(m, "enter")
	if cmd == nil {
		t.Fatal("picking a repo exports the brief off the UI thread")
	}
	msg, ok := cmd().(startMsg)
	if !ok {
		t.Fatalf("got %T", cmd())
	}
	sp := msg.sp
	if sp.Dir != "/code/payments" || sp.Name != "about: Payments export" {
		t.Errorf("spec: dir %q name %q", sp.Dir, sp.Name)
	}
	prompt := sp.Args[len(sp.Args)-1]
	if !strings.HasPrefix(prompt, "Read the Jira issue ABC-1 with the Atlassian MCP") || !strings.Contains(prompt, noWrites) {
		t.Errorf("prompt: %q", prompt)
	}
	id := sp.Args[1]
	if k, ok := links.Load().Ticket(id); !ok || k != "ABC-1" {
		t.Errorf("the new session is linked to its ticket: %q", k)
	}
}

func TestTicketSessionQueryShell(t *testing.T) {
	m := newModelShell(t)
	m.repos = []repos.Repo{{Dir: "/code/ledger"}, {Dir: "/code/billing", Project: "g/billing"}}
	m.ticketSession("ABC-2", "", "continue", "continue", "continue ABC-2 in", func(string, error) string { return "" })
	if m.chooser == nil || m.chooser.in.Value() != "billing" {
		t.Fatalf("with one MR's repo and no branch, its name is typed in")
	}
	if it, _ := m.chooser.selected(); it.value != "/code/billing" || !strings.HasPrefix(it.hint, "!21") {
		t.Errorf("the MR's repo is preferred: %+v", it)
	}
}

func TestAllViewsRenderShell(t *testing.T) {
	m := newModelShell(t)
	for v := view(0); v < nViews; v++ {
		m.switchView(v)
		out := m.View()
		if !strings.Contains(plainShell(out), "q quit") {
			t.Errorf("%s: footer lost the quit key", viewNames[v])
		}
		if h := strings.Count(out, "\n") + 1; h > m.height {
			t.Errorf("%s: %d lines on a %d-line terminal", viewNames[v], h, m.height)
		}
	}
}

func TestMaybeUsageShell(t *testing.T) {
	m := newModelShell(t)
	m.usageLoading = true
	if m.maybeUsage() != nil {
		t.Errorf("one scan at a time")
	}
	m.usageLoading = false
	m.Update(usageMsg{})
	if m.maybeUsage() != nil || m.usageLoading {
		t.Errorf("at most once a minute")
	}
}

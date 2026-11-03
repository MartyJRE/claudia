package ui

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/launch"
	"github.com/MartyJRE/claudia/internal/repos"
)

// pickIntent chooses one of claudeForTicket's sessions, then the first repo,
// and returns the spec the brief export ends in.
func pickIntent(t *testing.T, m *Model, key, label string) launch.Spec {
	t.Helper()
	m.claudeForTicket(key, "")
	m.chooser.setQuery(label)
	pressShell(m, "enter")
	if m.chooser == nil {
		t.Fatalf("%s: no repo picker", label)
	}
	msg, ok := pressShell(m, "enter")().(startMsg)
	if !ok {
		t.Fatalf("%s: no start", label)
	}
	return msg.sp
}

func promptOf(sp launch.Spec) string { return sp.Args[len(sp.Args)-1] }

func TestTicketIntentsFull(t *testing.T) {
	m := newModelShell(t)
	m.repos = []repos.Repo{{Dir: "/code/payments", Branch: "feat/ABC-1/export", Project: "g/payments"}}
	m.atl = &atlassian.Client{Base: "https://x"}
	var exported []string
	fake(t, &exportJiraBrief, func(c *atlassian.Client, key, dir string) (string, error) {
		exported = append(exported, key)
		return "/b/" + key + ".md", nil
	})
	withConfig(t, func(c *config.Config) {
		c.AnalysisPrompt, c.ImplementationPrompt = "A {key} {brief}", "I {key} {brief}"
		c.SessionNames = map[string]string{"analysis": "{verb} {key}", "test": " - "}
	})

	sp := pickIntent(t, m, "ABC-1", "Analysis")
	if promptOf(sp) != "A ABC-1 /b/ABC-1.md" || sp.Name != "analyse ABC-1" {
		t.Errorf("analysis: %q named %q", promptOf(sp), sp.Name)
	}
	if sp := pickIntent(t, m, "ABC-1", "Implementation"); promptOf(sp) != "I ABC-1 /b/ABC-1.md" {
		t.Errorf("implementation: %q", promptOf(sp))
	}
	sp = pickIntent(t, m, "ABC-1", "Continue")
	if p := promptOf(sp); !strings.Contains(p, "I'm working on it") || !strings.Contains(p, m.gl.Review[0].WebURL) {
		t.Errorf("continue names the MRs: %q", p)
	}
	sp = pickIntent(t, m, "ABC-1", "Test plan")
	if p := promptOf(sp); !strings.Contains(p, "write a test plan") || sp.Name != "test plan: Payments export" {
		t.Errorf("test plan: %q named %q (an empty template falls back)", p, sp.Name)
	}
	if len(exported) != 4 {
		t.Errorf("every session gets the exported brief: %v", exported)
	}
}

func TestReplyToMentionFull(t *testing.T) {
	m := newModelShell(t)
	m.repos = []repos.Repo{{Dir: "/code/ops"}, {Dir: "/code/payments"}}
	started := fakeStart(t, "ops:1", nil)
	m.replyToMention(mention{title: "Runbook", url: "https://x/wiki/42", where: "OPS"})
	pressShell(m, "enter")
	if len(*started) != 1 || (*started)[0].Name != "reply: Runbook" || !strings.Contains(promptOf((*started)[0]), `Confluence page "Runbook" (https://x/wiki/42)`) {
		t.Errorf("a Confluence reply starts in the picked repo: %+v", *started)
	}

	m.replyToMention(mention{jira: true, where: "ABC-1", title: "Payments export", author: "Eve Smith", text: "can you\ncheck?", url: "u1"})
	msg := pressShell(m, "enter")().(startMsg)
	p := promptOf(msg.sp)
	if !strings.HasPrefix(p, "Eve Smith mentioned me on ABC-1 (u1):\n\n> can you\n> check?") || !strings.Contains(p, "Read the Jira issue ABC-1") {
		t.Errorf("a Jira reply quotes the comment: %q", p)
	}
	if msg.sp.Name != "reply to Eve: Payments export" {
		t.Errorf("name: %q", msg.sp.Name)
	}
}

func TestInCloneFull(t *testing.T) {
	m := newModelShell(t)
	started := fakeStart(t, "payments:1", nil)
	mr := m.gl.Review[0]

	m.repos = []repos.Repo{{Dir: "/code/other"}}
	m.reviewWithClaude(mr)
	if m.chooser == nil || !strings.HasPrefix(m.flash, "no local clone of g/payments") {
		t.Fatalf("no clone: pick a repo (%q)", m.flash)
	}
	if m.chooser.in.Value() != "payments" {
		t.Errorf("the project name is typed in: %q", m.chooser.in.Value())
	}
	m.chooser.setQuery("")
	pressShell(m, "enter")
	if len(*started) != 1 || (*started)[0].Dir != "/code/other" {
		t.Fatalf("started in the picked repo: %+v", *started)
	}
	id := (*started)[0].Args[1]
	if k, _ := m.links.Ticket(id); k != "ABC-1" || !slices.Contains(m.links.MRs(id), mr.WebURL) {
		t.Errorf("the session is linked to the MR and its ticket: %q %v", k, m.links.MRs(id))
	}

	m.repos = []repos.Repo{{Dir: "/code/payments", Project: "g/payments"}}
	m.reviewWithClaude(mr)
	if len(*started) != 2 || (*started)[1].Dir != "/code/payments" {
		t.Errorf("one clone: straight in: %+v", *started)
	}

	m.repos = append(m.repos, repos.Repo{Dir: "/code/payments-2", Project: "g/payments", Branch: mr.SourceBranch})
	m.addressThreads(mr)
	if m.chooser == nil || m.chooser.title != "fix in clone" {
		t.Fatal("several clones: pick one")
	}
	if it, _ := m.chooser.selected(); it.value != "/code/payments-2" {
		t.Errorf("the clone on the MR's branch first: %+v", it)
	}
	breakState(t)
	pressShell(m, "enter")
	if len(*started) != 3 || !m.flashErr {
		t.Errorf("a link that can't be saved is flashed, the session still starts: %q", m.flash)
	}
}

func TestClaudeForInboxFull(t *testing.T) {
	m := newModelShell(t)
	r := m.sessions[0]
	if _, cmd := m.claudeForInbox(inboxItem{kind: kClaude, sess: r}); !isQuitShell(cmd) || m.Result.Attach != r.pane.Target()+".0" {
		t.Errorf("a waiting session is jumped to: %q", m.Result.Attach)
	}
	if _, cmd := m.claudeForInbox(inboxItem{kind: inboxKind(99)}); cmd != nil {
		t.Error("an unknown kind does nothing")
	}
}

func TestReviewUsesTheRepoFoldersProfile(t *testing.T) {
	m := newModelShell(t)
	started := fakeStart(t, "payments:1", nil)
	m.store.Profiles = []claude.Profile{{Name: "work", Dir: "/p/work"}, {Name: "personal", Dir: "/p/personal"}}
	mr := m.gl.Review[0]
	root := config.Get().CodeRoot
	for _, c := range []struct{ dir, profile string }{
		{filepath.Join(root, "Personal", "payments"), "personal"},
		{filepath.Join(root, "Work", "payments"), "work"},
	} {
		m.repos = []repos.Repo{{Dir: c.dir, Project: mr.Project()}}
		m.reviewWithClaude(mr)
		if got := (*started)[len(*started)-1]; got.Dir != c.dir || got.Profile.Name != c.profile {
			t.Errorf("review in %s ran with %q, want %q", c.dir, got.Profile.Name, c.profile)
		}
	}
}

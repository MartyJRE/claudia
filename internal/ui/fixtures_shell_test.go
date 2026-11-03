package ui

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/links"
	"github.com/MartyJRE/claudia/internal/tmux"
)

const glShell = "https://gitlab.example.com/"

// isolateShell keeps state writes and tmux lookups away from the real machine:
// with TMUX unset and an empty socket dir, every tmux call finds no server.
func isolateShell(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	lastLive = ""
}

func refShell(full string) struct {
	Full string `json:"full"`
} {
	return struct {
		Full string `json:"full"`
	}{full}
}

func mrShell(project string, iid int, title, author, branch string, updated time.Duration) gitlab.MR {
	return gitlab.MR{
		IID: iid, ProjectID: len(project), Title: title,
		WebURL:       glShell + "g/" + project + "/-/merge_requests/" + strconv.Itoa(iid),
		References:   refShell("g/" + project + "!" + strconv.Itoa(iid)),
		Author:       gitlab.User{Username: author},
		SourceBranch: branch, TargetBranch: "devel", State: "opened",
		SHA:       "sha-" + strconv.Itoa(iid),
		CreatedAt: time.Now().Add(-updated - time.Hour), UpdatedAt: time.Now().Add(-updated),
	}
}

func glSnapshotShell() gitlab.Snapshot {
	now := time.Now()
	review := mrShell("payments", 11, "feat(ABC-1): add payments export", "alice", "feat/ABC-1/export", time.Hour)
	review.Pipeline = &gitlab.Pipeline{Status: "success", WebURL: "pipe-11"}
	review.ApprovalsLeft = 1

	draft := mrShell("billing", 12, "Draft: wip billing", "bob", "wip", 10*time.Minute)
	draft.Draft = true

	approved := mrShell("ledger", 13, "fix: rounding", "alice", "fix/rounding", 2*time.Hour)
	approved.ApprovedBy, approved.Approved = []string{"me"}, true

	mine := mrShell("billing", 21, "feat(ABC-2): invoices", "me", "feat/ABC-2/invoices", 30*time.Minute)
	mine.Pipeline = &gitlab.Pipeline{Status: "failed", WebURL: "pipe-21"}
	mine.Reviewers = []gitlab.User{{Username: "carol"}, {Username: "dave"}}
	mine.ApprovedBy = []string{"dave"}
	mine.ApprovalsLeft = 1
	mine.Threads = []gitlab.Thread{
		{Author: "carol", Body: "rename\nthis", Path: "internal/pay/export.go", Line: 7, At: now.Add(-3 * time.Hour), URL: "thread-1", Replies: 2, LastBy: "me"},
		{Author: "dave", Body: "done already", Resolved: true, At: now.Add(-4 * time.Hour), URL: "thread-2"},
	}

	merged := mrShell("ledger", 22, "chore: bump deps", "me", "chore/bump", 26*time.Hour)
	merged.State = "merged"
	merged.MergePipeline = &gitlab.Pipeline{Status: "failed", WebURL: "pipe-22"}

	return gitlab.Snapshot{
		Me: gitlab.User{Username: "me"}, Review: []gitlab.MR{review, draft, approved},
		Mine: []gitlab.MR{mine}, Merged: []gitlab.MR{merged}, FetchedAt: now,
	}
}

func jiraSnapshotShell() atlassian.JiraSnapshot {
	return atlassian.JiraSnapshot{
		Sprint: []atlassian.Issue{
			{Key: "ABC-1", Summary: "Payments export", Status: "Dev in progress", Category: "indeterminate", Mine: true},
			{Key: "ABC-2", Summary: "Invoices", Status: "Code review", Category: "indeterminate"},
			{Key: "ABC-3", Summary: "Old thing", Status: "Done", Category: "done"},
		},
		Mentions: []atlassian.Mention{
			{Key: "ABC-1", Summary: "Payments export", Author: "Eve Smith", Text: "can you check?", URL: "mention-1", Created: time.Now().Add(-30 * time.Hour), CommentID: "c1"},
			{Key: "ABC-2", Summary: "Invoices", Author: "Finn", Text: "thanks", URL: "mention-2", Created: time.Now().Add(-time.Hour), CommentID: "c2", Answered: true},
		},
		FetchedAt: time.Now(),
	}
}

func msAgoShell(d time.Duration) int64 { return time.Now().Add(-d).UnixMilli() }

func sessionsShell() []row {
	now := time.Now()
	return []row{
		{
			Session: claude.Session{
				ID: "aaaaaaaa-wait", Profile: claude.Profile{Name: "work"},
				Transcript: &claude.Transcript{
					Cwd: "/code/payments", Branch: "feat/ABC-1/export", AITitle: "export csv",
					LastPrompt: "please add\ncsv", Model: "claude-opus-5-5", Models: []string{"claude-sonnet-4-5", "claude-opus-5-5"},
					Usage:   claude.Usage{Requests: 3, Output: 12_000, CacheRead: 1_500_000},
					Updated: now.Add(-2 * time.Minute),
				},
				Live: &claude.Live{SessionID: "aaaaaaaa-wait", Cwd: "/code/payments", Status: claude.StatusWaiting, Entrypoint: "cli", StartedAt: msAgoShell(time.Hour), UpdatedAt: msAgoShell(20 * time.Minute)},
			},
			pane: &tmux.Pane{ID: "%1", Session: "work", Window: "3", Index: "0"},
		},
		{
			Session: claude.Session{
				ID: "bbbbbbbb-busy", Profile: claude.Profile{Name: "personal"},
				Transcript: &claude.Transcript{Cwd: "/code/billing", Branch: "feat/ABC-2/invoices", AITitle: "invoice pdf", Model: "claude-sonnet-4-5", Updated: now.Add(-time.Minute)},
				Live:       &claude.Live{SessionID: "bbbbbbbb-busy", Cwd: "/code/billing", Status: claude.StatusBusy, Entrypoint: "cli", StartedAt: msAgoShell(2 * time.Hour), UpdatedAt: msAgoShell(time.Minute)},
			},
			host: "Ghostty",
		},
		{
			Session: claude.Session{
				ID: "cccccccc-idle", Profile: claude.Profile{Name: "work"},
				Live: &claude.Live{SessionID: "cccccccc-idle", Cwd: "/code/ledger", Name: "ledger chat", Status: claude.StatusIdle, Entrypoint: "sdk-cli", StartedAt: msAgoShell(3 * time.Hour), UpdatedAt: msAgoShell(5 * time.Minute)},
			},
		},
		{
			Session: claude.Session{
				ID: "dddddddd-closed", Profile: claude.Profile{Name: "work"},
				Transcript: &claude.Transcript{Cwd: "/code/payments", Branch: "devel", AITitle: "old spike", Updated: now.Add(-3 * time.Hour)},
			},
		},
	}
}

// newModelShell is a model as it looks after every source has loaded once.
func newModelShell(t *testing.T) *Model {
	t.Helper()
	isolateShell(t)
	km, problems := newKeymap(nil)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	f := textinput.New()
	f.Prompt = "/ "
	m := &Model{
		km:          km,
		atlErr:      errors.New("ATLASSIAN_TOKEN not set"),
		store:       claude.NewStore([]claude.Profile{{Name: "work"}, {Name: "personal"}}, 0),
		links:       links.Load(),
		seen:        seenStore{},
		seenExisted: true,
		dismissed:   dismissed{},
		filter:      f,
		view:        vInbox,
		width:       160,
		height:      40,
		sessLoaded:  true,
		gl:          glSnapshotShell(),
		jira:        jiraSnapshotShell(),
		sessions:    sessionsShell(),
	}
	m.fetchedAt[srcGitlab] = m.gl.FetchedAt
	m.fetchedAt[srcJira] = m.jira.FetchedAt
	m.reindex()
	return m
}

func keyShell(k string) tea.KeyMsg {
	named := map[string]tea.KeyType{
		"enter": tea.KeyEnter, "esc": tea.KeyEsc, "tab": tea.KeyTab, "shift+tab": tea.KeyShiftTab,
		"up": tea.KeyUp, "down": tea.KeyDown, " ": tea.KeySpace, "ctrl+x": tea.KeyCtrlX,
		"ctrl+c": tea.KeyCtrlC, "backspace": tea.KeyBackspace, "home": tea.KeyHome, "end": tea.KeyEnd,
	}
	if t, ok := named[k]; ok {
		return tea.KeyMsg{Type: t}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

// pressShell sends keys one by one and returns the last command.
func pressShell(m *Model, keys ...string) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		_, cmd = m.Update(keyShell(k))
	}
	return cmd
}

func typeShell(m *Model, s string) {
	for _, r := range s {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func plainShell(s string) string { return ansi.Strip(s) }

func screenShell(m *Model) string { return plainShell(m.View()) }

func isQuitShell(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func containsAllShell(t *testing.T, what, s string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(s, w) {
			t.Errorf("%s: missing %q in:\n%s", what, w, s)
		}
	}
}

func lacksShell(t *testing.T, what, s string, bad ...string) {
	t.Helper()
	for _, w := range bad {
		if strings.Contains(s, w) {
			t.Errorf("%s: unexpected %q in:\n%s", what, w, s)
		}
	}
}

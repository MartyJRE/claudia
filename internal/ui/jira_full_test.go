package ui

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/state"
	"github.com/MartyJRE/claudia/internal/tmux"
)

// fakeExport writes the brief (and any attachments) where ExportBrief would.
func fakeExport(t *testing.T, attachments ...string) *[]string {
	var got []string
	fake(t, &exportJiraBrief, func(_ *atlassian.Client, key, dir string) (string, error) {
		got = append(got, key+" → "+dir)
		d := filepath.Join(t.TempDir(), key)
		if err := os.MkdirAll(d, 0o755); err != nil {
			return "", err
		}
		for _, a := range attachments {
			if err := os.WriteFile(filepath.Join(d, a), nil, 0o644); err != nil {
				return "", err
			}
		}
		p := filepath.Join(d, key+".md")
		return p, os.WriteFile(p, []byte("# "+key+" brief"), 0o644)
	})
	return &got
}

func TestJiraCopyBriefFull(t *testing.T) {
	m := jiraModelViews(t)
	cursorToViews(t, m, "ABC-3")
	exported := fakeExport(t)
	copied := fakeClipboard(t, nil)
	msg := pressViews(m, "e")()
	if msg != (flashMsg{"copied ABC-3 brief (with image paths)", false}) {
		t.Errorf("copy brief: %#v", msg)
	}
	if len(*exported) != 1 || (*exported)[0] != "ABC-3 → "+state.Path("jira-export") {
		t.Errorf("exported into the state dir: %v", *exported)
	}
	if len(*copied) != 1 || (*copied)[0] != "pbcopy < # ABC-3 brief" {
		t.Errorf("the brief's text is copied: %v", *copied)
	}

	fake(t, &exportJiraBrief, func(*atlassian.Client, string, string) (string, error) { return "/nowhere/ABC-3.md", nil })
	if msg, ok := pressViews(m, "e")().(flashMsg); !ok || !msg.err || !strings.Contains(msg.text, "/nowhere/ABC-3.md") {
		t.Errorf("an unreadable export is flashed: %#v", msg)
	}
	fake(t, &exportJiraBrief, func(*atlassian.Client, string, string) (string, error) { return "", errors.New("403") })
	if msg := pressViews(m, "e")(); msg != (flashMsg{"403", true}) {
		t.Errorf("a failed export is flashed: %#v", msg)
	}
}

func TestJiraImagesOffWithoutKitty(t *testing.T) {
	m := jiraModelViews(t)
	cursorToViews(t, m, "ABC-3")
	fake(t, &imagesShown, func() bool { return false })
	if cmd := pressViews(m, "i"); cmd != nil {
		t.Error("i does nothing in a terminal without the kitty protocol")
	}
	for _, h := range m.help() {
		if h[1] == "images" {
			t.Error("no images hint in a terminal without the kitty protocol")
		}
	}
}

func TestJiraImagesFull(t *testing.T) {
	m := jiraModelViews(t)
	cursorToViews(t, m, "ABC-3")
	fakeExport(t, "shot.PNG", "b.jpg", "notes.txt")
	msg, ok := pressViews(m, "i")().(imagesMsg)
	if !ok || msg.key != "ABC-3" || len(msg.files) != 2 || filepath.Base(msg.files[0]) != "b.jpg" || filepath.Base(msg.files[1]) != "shot.PNG" {
		t.Fatalf("only images are shown: %#v", msg)
	}

	fake(t, &executable, func() (string, error) { return "/bin/claudia", nil })
	var ran *exec.Cmd
	var done func(error) tea.Msg
	fake(t, &execProcess, func(c *exec.Cmd, fn tea.ExecCallback) tea.Cmd {
		ran, done = c, fn
		return func() tea.Msg { return nil }
	})
	m.setFlash("exporting…", false)
	if _, cmd := m.Update(msg); cmd == nil || m.flash != "" {
		t.Error("the terminal goes to claudia img")
	}
	if got := cmdLine(ran); got != "/bin/claudia img "+strings.Join(msg.files, " ") {
		t.Errorf("command: %s", got)
	}
	if done(nil) != nil || done(errors.New("kitty")) != (flashMsg{"kitty", true}) {
		t.Error("only a failure is reported")
	}

	fake(t, &executable, func() (string, error) { return "", errors.New("no self") })
	if m.openImages(msg) != nil || m.flash != "no self" {
		t.Errorf("no executable: %q", m.flash)
	}

	fake(t, &exportJiraBrief, func(*atlassian.Client, string, string) (string, error) { return "/nowhere/ABC-3.md", nil })
	if msg, ok := pressViews(m, "i")().(flashMsg); !ok || !msg.err {
		t.Errorf("a missing export dir is flashed: %#v", msg)
	}
}

func TestJiraMoveFull(t *testing.T) {
	m := jiraModelViews(t)
	cursorToViews(t, m, "ABC-3")
	fake(t, &jiraTransitions, func(c *atlassian.Client, key string) ([]atlassian.Transition, error) {
		if c != m.atl || key != "ABC-3" {
			t.Errorf("transitions of %s via %v", key, c)
		}
		return []atlassian.Transition{{ID: "31", Name: "Review", To: "In Review"}}, nil
	})
	m.Update(pressViews(m, "m")())
	if m.chooser == nil || m.chooser.title != "move ABC-3" {
		t.Fatal("m lists the transitions")
	}
	var moved string
	fake(t, &transitionIssue, func(_ *atlassian.Client, key, id string) error {
		moved = key + ":" + id
		return nil
	})
	if msg := pressViews(m, "enter")(); msg != refreshMsg(srcJira) || moved != "ABC-3:31" {
		t.Errorf("moving refreshes Jira: %#v %q", msg, moved)
	}

	m.Update(pressViews(m, "m")())
	fake(t, &transitionIssue, func(*atlassian.Client, string, string) error { return errors.New("not allowed") })
	if msg := pressViews(m, "enter")(); msg != (flashMsg{"not allowed", true}) {
		t.Errorf("a refused move is flashed: %#v", msg)
	}
}

func TestJiraSessionsAndOtherKeysFull(t *testing.T) {
	m := jiraModelViews(t)
	m.sessions = []row{{
		Session: claude.Session{ID: "s1", Profile: claude.Profile{Name: "work"}, Transcript: &claude.Transcript{Cwd: "/code/pay", Branch: "feat/ABC-3/refund"},
			Live: &claude.Live{SessionID: "s1", Cwd: "/code/pay", Status: claude.StatusIdle}},
		pane: &tmux.Pane{ID: "%3", Session: "pay", Window: "2", Index: "1"},
	}}
	m.reindex()
	cursorToViews(t, m, "ABC-3")
	pressViews(m, "enter")
	if m.chooser == nil || m.chooser.title != "sessions for ABC-3" {
		t.Fatal("enter lists the ticket's sessions")
	}
	if _, cmd := m.Update(keyViews("enter")); !isQuitShell(cmd) || m.Result.Attach != "pay:2.1" {
		t.Errorf("picking one jumps to it: %q", m.Result.Attach)
	}
	if cmd := pressViews(m, "Z"); cmd != nil || m.chooser != nil {
		t.Error("an unbound key on an issue does nothing")
	}

	m.teamJira = true
	m.refilter()
	m.chooseStatuses()
	var labels []string
	for _, it := range m.chooser.all {
		labels = append(labels, it.search)
	}
	if !strings.Contains(strings.Join(labels, "|"), "In Review") {
		t.Errorf("the team view offers the team's statuses: %v", labels)
	}
}

func TestJiraContextRowsFull(t *testing.T) {
	m := jiraModelViews(t)
	m.jira.Sprint[2].ParentSummary, m.jira.Sprint[2].ParentStatus = "Payments epic", "In Progress"
	m.jira.Sprint[1].ParentSummary, m.jira.Sprint[1].ParentStatus = "Payments epic", "In Progress"
	m.filter.SetValue("ann")
	m.refilter()
	if got := rowKeysViews(m.vis.jira); !strings.Contains(got, "(ABC-1)") {
		t.Fatalf("the parent is shown as context: %s", got)
	}
	s := screenViews(m)
	if !strings.Contains(s, "Payments epic") || !strings.Contains(s, "Refund flow") {
		t.Errorf("context rows render:\n%s", s)
	}
}

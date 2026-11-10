package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/launch"
	"github.com/MartyJRE/claudia/internal/repos"
	"github.com/MartyJRE/claudia/internal/state"
	"github.com/MartyJRE/claudia/internal/tmux"
)

// breakState makes every state write fail from here on.
func breakState(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", f)
}

func TestCloseSessionFull(t *testing.T) {
	m := sessionsModelShell(t)
	m.sessions[0].Live.PID = 4242
	m.marked = map[string]bool{"aaaaaaaa-wait": true}
	m.reindex()
	var killed []string
	fake(t, &killProcess, func(pid int, sig syscall.Signal) error {
		killed = append(killed, fmt.Sprint(pid, " ", sig))
		return nil
	})
	fakeSessions(t, nil, nil, nil)
	cursorOnShell(t, m, "aaaaaaaa-wait")
	cmd := pressShell(m, "K")
	if !slices.Equal(killed, []string{"4242 terminated"}) || m.marked["aaaaaaaa-wait"] || m.flash != "closed export csv · R resumes it" {
		t.Errorf("K ends the process: %v %q", killed, m.flash)
	}
	if _, ok := cmd().(sessionsMsg); !ok {
		t.Error("and reloads the sessions")
	}

	fake(t, &killProcess, func(int, syscall.Signal) error { return errors.New("operation not permitted") })
	if cmd := pressShell(m, "K"); cmd != nil || m.flash != "close export csv: operation not permitted" {
		t.Errorf("a failed kill is flashed: %q", m.flash)
	}
}

func TestSplitFull(t *testing.T) {
	m := sessionsModelShell(t)
	m.sessions[2].pane = &tmux.Pane{ID: "%2", Session: "work", Window: "4", Index: "0"}
	m.reindex()
	var joined []string
	fake(t, &joinSplit, func(ps []tmux.Pane) (string, error) {
		for _, p := range ps {
			joined = append(joined, p.ID)
		}
		return "claudia-split:1", nil
	})
	cursorOnShell(t, m, "cccccccc-idle")
	pressShell(m, "m")
	cursorOnShell(t, m, "aaaaaaaa-wait")
	pressShell(m, "m")
	cursorOnShell(t, m, "aaaaaaaa-wait")
	cmd := pressShell(m, "S")
	if !slices.Equal(joined, []string{"%1", "%2"}) || m.marked != nil {
		t.Errorf("the current and marked panes are tiled once each: %v", joined)
	}
	if !isQuitShell(cmd) || m.Result.Attach != "claudia-split:1" {
		t.Errorf("and claudia goes to the split: %q", m.Result.Attach)
	}

	m.marked = map[string]bool{"cccccccc-idle": true}
	fake(t, &joinSplit, func([]tmux.Pane) (string, error) { return "", errors.New("pane gone") })
	if cmd := pressShell(m, "S"); cmd != nil || m.flash != "split: pane gone" || !m.marked["cccccccc-idle"] {
		t.Errorf("a failed split keeps the marks: %q", m.flash)
	}
}

func TestUnsplitFull(t *testing.T) {
	m := sessionsModelShell(t)
	m.sessions[0].pane = &tmux.Pane{ID: "%1", Session: tmux.SplitSession, Window: "1", WindowID: "@7"}
	m.reindex()
	var windows []string
	fake(t, &unsplitWindow, func(w string) error {
		windows = append(windows, w)
		return nil
	})
	fakeSessions(t, nil, nil, nil)
	cursorOnShell(t, m, "aaaaaaaa-wait")
	cmd := pressShell(m, "U")
	if !slices.Equal(windows, []string{"@7"}) || m.flash != "sent the split's sessions back to their windows" {
		t.Errorf("U unsplits the window: %v %q", windows, m.flash)
	}
	if _, ok := cmd().(sessionsMsg); !ok {
		t.Error("and reloads the sessions")
	}
	fake(t, &unsplitWindow, func(string) error { return errors.New("no window") })
	if cmd := pressShell(m, "U"); cmd != nil || m.flash != "unsplit: no window" {
		t.Errorf("a failed unsplit is flashed: %q", m.flash)
	}
}

func TestResumeAndNewSessionFull(t *testing.T) {
	m := sessionsModelShell(t)
	m.allSessions = true
	m.refilter()
	started := fakeStart(t, "payments:5", nil)
	cursorOnShell(t, m, "dddddddd-closed")
	if cmd := pressShell(m, "enter"); !isQuitShell(cmd) || m.Result.Attach != "payments:5" {
		t.Errorf("enter on a closed session resumes it: %q", m.Result.Attach)
	}
	if len(*started) != 1 || (*started)[0].Dir != "/code/payments" || !slices.Contains((*started)[0].Args, "dddddddd-closed") {
		t.Errorf("resume spec: %+v", *started)
	}

	m.repos = []repos.Repo{{Dir: "/code/ledger"}, {Dir: "/code/payments"}}
	pressShell(m, "n")
	if m.chooser == nil || m.chooser.title != "new session in" {
		t.Fatal("n picks a repo")
	}
	if it, _ := m.chooser.selected(); it.value != "/code/payments" {
		t.Errorf("the current session's repo comes first: %+v", it)
	}
	pressShell(m, "enter")
	sp := (*started)[1]
	if sp.Dir != "/code/payments" || sp.Name != "payments" || sp.Args[0] != "--session-id" {
		t.Errorf("a plain new session: %+v", sp)
	}
}

func TestJumpWithNothingFull(t *testing.T) {
	m := sessionsModelShell(t)
	m.sessions = nil
	m.reindex()
	if _, cmd := m.jumpSession(); cmd != nil || m.Result.Attach != "" {
		t.Error("nothing under the cursor, nothing to jump to")
	}
}

func TestPickTicketFull(t *testing.T) {
	m := sessionsModelShell(t)
	m.jira.Team = []atlassian.Issue{m.jira.Sprint[0], {Key: "ABC-7", Summary: "Team thing"}}
	cursorOnShell(t, m, "aaaaaaaa-wait")
	pressShell(m, "t")
	var keys []string
	for _, it := range m.chooser.all {
		keys = append(keys, it.value.(string))
	}
	if strings.Join(keys, " ") != " - ABC-1 ABC-2 ABC-3 ABC-7" {
		t.Errorf("each ticket once, team ones after the sprint: %q", keys)
	}
	breakState(t)
	m.chooser.setQuery("ABC-7")
	pressShell(m, "enter")
	if !m.flashErr || m.flash == "" {
		t.Errorf("a failed save is flashed: %q", m.flash)
	}
}

func TestMRRefPlainLinkFull(t *testing.T) {
	m := newModelShell(t)
	if got := plainShell(m.mrRef("https://example.org/some/review")); got != "example.org/some/review" {
		t.Errorf("an unknown kind of link is shown short: %q", got)
	}
}

func TestAssistantFull(t *testing.T) {
	m := newModelShell(t)
	withConfig(t, func(c *config.Config) { c.AssistantDir = "/code/mono" })
	day := time.Date(2026, 11, 10, 9, 0, 0, 0, time.Local)
	fake(t, &clockNow, func() time.Time { return day })
	m.repos = []repos.Repo{{Dir: "/code/billing"}, {Dir: "/code/mono"}}
	started := fakeStart(t, "assistant:1", nil)

	m.Update(keyShell("@"))
	if m.chooser == nil || m.chooser.title != "assistant in" {
		t.Fatal("@ with no assistant asks where to start it")
	}
	if first, _ := m.chooser.selected(); first.value != "/code/mono" || first.hint != "default" {
		t.Errorf("the assistant dir comes first: %+v", first)
	}
	if n := len(m.chooser.all); n != 2 {
		t.Errorf("the default dir isn't listed twice: %d choices", n)
	}
	m.Update(keyShell("enter"))
	var saved assistant
	if !state.Load(assistantFile, &saved) || saved.ID == "" || saved.Dir != "/code/mono" || saved.Day != "2026-11-10" {
		t.Fatalf("a new assistant is remembered with its dir and day: %+v", saved)
	}
	if sp := (*started)[0]; sp.Session != launch.AssistantSession || sp.Dir != "/code/mono" || !slices.Contains(sp.Args, saved.ID) || slices.Contains(sp.Args, "--resume") {
		t.Errorf("first start: %+v", sp)
	}

	m.sessions = append(m.sessions, row{Session: claude.Session{ID: saved.ID, Profile: claude.Profile{Name: "personal"}, Transcript: &claude.Transcript{Cwd: "/code/mono"}}})
	m.Update(keyShell("@"))
	if sp := (*started)[1]; sp.Profile.Name != "personal" || sp.Dir != "/code/mono" || !slices.Contains(sp.Args, "--resume") {
		t.Errorf("an assistant that ended today is resumed in its dir and profile: %+v", sp)
	}

	day = day.AddDate(0, 0, 1)
	m.Update(keyShell("@"))
	if len(*started) != 2 || m.chooser == nil {
		t.Fatal("yesterday's assistant isn't resumed; a new one is picked")
	}
	m.chooser = nil
	day = day.AddDate(0, 0, -1)

	m.sessions[len(m.sessions)-1].Live = &claude.Live{SessionID: saved.ID, PID: 4242}
	m.sessions[len(m.sessions)-1].pane = &tmux.Pane{Session: "assistant", Window: "1", Index: "0"}
	m.Result.Attach = ""
	m.Update(keyShell("@"))
	if len(*started) != 2 || m.Result.Attach != "assistant:1.0" {
		t.Errorf("a live assistant is jumped to: %q", m.Result.Attach)
	}

	var killed []int
	fake(t, &killProcess, func(pid int, _ syscall.Signal) error { killed = append(killed, pid); return nil })
	m.Update(keyShell("!"))
	if !slices.Equal(killed, []int{4242}) || m.chooser == nil || m.chooser.title != "assistant in" {
		t.Fatalf("! closes the live assistant and asks where to start a new one: killed %v", killed)
	}
	m.Update(keyShell("enter"))
	var fresh assistant
	state.Load(assistantFile, &fresh)
	if fresh.ID == saved.ID || len(*started) != 3 || slices.Contains((*started)[2].Args, "--resume") {
		t.Errorf("! starts a clean assistant: %+v", fresh)
	}

	fake(t, &killProcess, func(int, syscall.Signal) error { return errors.New("operation not permitted") })
	m.sessions[len(m.sessions)-1].Session.ID = fresh.ID
	if m.Update(keyShell("!")); m.chooser != nil || !m.flashErr {
		t.Errorf("a failed close starts nothing: %q", m.flash)
	}

	breakState(t)
	m.sessions = nil
	m.pickAssistantDir()
	if _, cmd := m.chooser.pick(m, m.chooser.all[0]); cmd != nil || !m.flashErr || len(*started) != 3 {
		t.Errorf("an unsaved id starts nothing: %q", m.flash)
	}
}

func TestRestoreAllFull(t *testing.T) {
	m := sessionsModelShell(t)
	m.restore = []restoreItem{
		{ID: "aaaaaaaa-wait", Dir: "/code/payments"},
		{ID: "r1", Dir: "/code/a", Profile: "work", Name: "a"},
		{ID: "r2", Dir: "/code/b", Name: "b"},
		{ID: "r3", Dir: "/code/c", Name: "c"},
	}
	var started []string
	fake(t, &startSession, func(sp launch.Spec) (string, error) {
		started = append(started, sp.Args[1])
		if sp.Args[1] == "r1" {
			return "", errors.New("no such dir")
		}
		return sp.Name + ":1", nil
	})
	cmd := pressShell(m, "X")
	if !slices.Equal(started, []string{"r1", "r2", "r3"}) {
		t.Errorf("only sessions not running again are resumed: %v", started)
	}
	if m.restore != nil || m.flash != "restored 2 sessions" {
		t.Errorf("the offer is gone: %q", m.flash)
	}
	if !isQuitShell(cmd) || m.Result.Attach != "b:1" {
		t.Errorf("outside tmux claudia attaches to the first one: %q", m.Result.Attach)
	}

	t.Setenv("TMUX", "/tmp/tmux-1/default,1,0")
	m.restore = []restoreItem{{ID: "r4", Dir: "/code/d", Name: "d"}}
	if _, cmd := m.restoreAll(); cmd != nil {
		t.Error("inside tmux the windows just open")
	}
}

func TestSessionGroupsFull(t *testing.T) {
	m := sessionsModelShell(t)
	m.allSessions = true
	now := time.Now()
	m.sessions = append(m.sessions,
		row{Session: claude.Session{ID: "eeeeeeee-old", Transcript: &claude.Transcript{Cwd: "/code/zeta", Updated: now.Add(-5 * time.Hour)}}},
		row{Session: claude.Session{ID: "ffffffff-older", Transcript: &claude.Transcript{Cwd: "/code/eta", Updated: now.Add(-9 * time.Hour)}}},
	)
	m.reindex()
	if got := sessionIDsShell(m); !strings.HasSuffix(got, "cccc eeee ffff") {
		t.Errorf("closed-only repos after live ones, newest first: %s", got)
	}

	m.sessPrefs.Group = sessByTicket
	m.jira.Sprint = append(m.jira.Sprint,
		atlassian.Issue{Key: "ABC-5", Summary: "Epic five", Level: 1},
		atlassian.Issue{Key: "ABC-6", Summary: "Child", Parent: "ABC-50", ParentSummary: "Far epic"},
	)
	m.links.SetTicket("eeeeeeee-old", "ABC-5")
	m.links.SetTicket("ffffffff-older", "ABC-6")
	m.reindex()
	if s := screenShell(m); !strings.Contains(s, "eeeeeeee  Epic five") || !strings.Contains(s, "ffffffff  Far epic › Child") {
		t.Errorf("an epic holds its own sessions; an unknown epic takes its title from the child:\n%s", s)
	}

	m.sessPrefs.Collapsed = []string{"ticket:-"}
	m.refilter()
	if s := screenShell(m); !strings.Contains(s, "▸ No ticket") {
		t.Errorf("a folded group shows ▸:\n%s", s)
	}

	m.sessPrefs.Group = sessFlat
	m.refilter()
	m.cur[vSessions].pos = 0
	before := slices.Clone(m.sessPrefs.Collapsed)
	m.toggleSessionFold()
	if !slices.Equal(m.sessPrefs.Collapsed, before) {
		t.Error("in a flat list there is no group to fold")
	}
	if sessionIndent(m.vis.sessions[0]) != "" {
		t.Error("top-level rows are not indented")
	}
	m.cur[vSessions].pos = 99
	m.toggleSessionFold()
	if !slices.Equal(m.sessPrefs.Collapsed, before) {
		t.Error("a cursor past the end folds nothing")
	}
}

func TestBoolCmpFull(t *testing.T) {
	if boolCmp(true, true) != 0 || boolCmp(false, false) != 0 || boolCmp(true, false) != 1 || boolCmp(false, true) != -1 {
		t.Error("false sorts before true")
	}
}

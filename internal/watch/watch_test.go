package watch

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/state"
)

func TestMain(m *testing.M) {
	dir, _ := os.MkdirTemp("", "claudia-test")
	os.Setenv("XDG_CONFIG_HOME", dir)
	os.Setenv("XDG_STATE_HOME", dir)
	os.Setenv("CLAUDIA_GITLAB_HOST", "gitlab.example.com")
	os.Setenv("HOME", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type sent struct {
	mu    sync.Mutex
	notes []note
}

func (s *sent) take() []note {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.notes
	s.notes = nil
	return n
}

func catchNotes(t *testing.T) *sent {
	t.Helper()
	s := &sent{}
	old := notify
	notify = func(n note) {
		s.mu.Lock()
		s.notes = append(s.notes, n)
		s.mu.Unlock()
	}
	t.Cleanup(func() { notify = old })
	return s
}

func newWatcher(profiles ...claude.Profile) *watcher {
	return &watcher{
		store:    claude.NewStore(profiles, 0),
		status:   map[string]claude.Status{},
		reviews:  map[string]bool{},
		pipeline: map[string]string{},
		approved: map[string]bool{},
		mentions: map[string]bool{},
	}
}

func mr(iid int, title string) gitlab.MR {
	return gitlab.MR{IID: iid, Title: title, WebURL: "https://gitlab.example.com/acme/pay/-/merge_requests/" + strconv.Itoa(iid), Author: gitlab.User{Username: "ann"}}
}

func TestGitLabNotifications(t *testing.T) {
	got := catchNotes(t)
	w := newWatcher()
	failed := &gitlab.Pipeline{Status: "failed", WebURL: "https://p/1"}

	old := mr(1, "old review")
	mine := mr(2, "mine")
	w.gitlab(gitlab.Snapshot{Review: []gitlab.MR{old}, Mine: []gitlab.MR{mine}})
	if n := got.take(); len(n) != 0 {
		t.Fatalf("first poll is the baseline, got %+v", n)
	}

	draft := mr(3, "draft")
	draft.Draft = true
	fresh := mr(4, "please look")
	mine.Pipeline = failed
	merged := mr(5, "merged")
	merged.MergePipeline = failed
	w.gitlab(gitlab.Snapshot{Review: []gitlab.MR{old, draft, fresh}, Mine: []gitlab.MR{mine}, Merged: []gitlab.MR{merged, mr(6, "no ci")}})
	n := got.take()
	if len(n) != 3 {
		t.Fatalf("got %+v", n)
	}
	if n[0].title != "ann asks for your review" || n[0].subtitle != "pay !4" || n[0].body != "please look" || n[0].id != "review:"+fresh.WebURL || n[0].url != fresh.WebURL {
		t.Errorf("review %+v", n[0])
	}
	if n[1].title != "Pipeline failed" || n[1].url != "https://p/1" || n[1].thread != "pipeline" {
		t.Errorf("pipeline %+v", n[1])
	}
	if n[2].title != "Pipeline failed after merge" || n[2].id != "merge:"+merged.WebURL {
		t.Errorf("merge pipeline %+v", n[2])
	}

	mine.Approved, mine.ApprovedBy = true, []string{"bob", "cy"}
	w.gitlab(gitlab.Snapshot{Review: []gitlab.MR{old, draft, fresh}, Mine: []gitlab.MR{mine}, Merged: []gitlab.MR{merged}})
	n = got.take()
	if len(n) != 1 || n[0].title != "bob, cy approved your MR" || n[0].url != mine.WebURL {
		t.Fatalf("only the approval is new: %+v", n)
	}

	// A draft leaving draft state was already seen, and a pipeline that
	// recovers and fails again notifies again.
	draft.Draft = false
	mine.Pipeline = &gitlab.Pipeline{Status: "success"}
	w.gitlab(gitlab.Snapshot{Review: []gitlab.MR{draft}, Mine: []gitlab.MR{mine}})
	mine.Pipeline = failed
	w.gitlab(gitlab.Snapshot{Review: []gitlab.MR{draft}, Mine: []gitlab.MR{mine}})
	n = got.take()
	if len(n) != 1 || n[0].title != "Pipeline failed" {
		t.Errorf("got %+v", n)
	}
}

func TestJiraNotifications(t *testing.T) {
	got := catchNotes(t)
	w := newWatcher()
	old := atlassian.Mention{CommentID: "1", Key: "PAY-1", Summary: "old", Author: "Ann"}
	w.jira(atlassian.JiraSnapshot{Mentions: []atlassian.Mention{old}})
	if n := got.take(); len(n) != 0 {
		t.Fatalf("baseline notified %+v", n)
	}
	answered := atlassian.Mention{CommentID: "2", Answered: true}
	fresh := atlassian.Mention{CommentID: "3", Key: "PAY-2", Summary: "Pay faster", Author: "Bob", Text: "can you?", URL: "https://j/3"}
	w.jira(atlassian.JiraSnapshot{Mentions: []atlassian.Mention{old, answered, fresh}})
	w.jira(atlassian.JiraSnapshot{Mentions: []atlassian.Mention{old, answered, fresh}})
	n := got.take()
	if len(n) != 1 {
		t.Fatalf("got %+v", n)
	}
	if n[0] != (note{title: "Bob mentioned you", subtitle: "PAY-2 Pay faster", body: "can you?", thread: "mention", id: "mention:3", url: "https://j/3"}) {
		t.Errorf("got %+v", n[0])
	}
}

func TestClickCommand(t *testing.T) {
	tm, err := exec.LookPath("tmux")
	if err != nil {
		tm = "tmux"
	}
	q := strconv.Quote
	if got := clickCommand(note{target: "pay:1.0"}); got != q(tm)+` switch-client -t "pay:1.0"; open -a Ghostty` {
		t.Errorf("got %s", got)
	}
	want := q(tm) + ` switch-client -t "=bill:@3" || ` + q(tm) + ` switch-client -t "pay:1.0"; open -a Ghostty`
	if got := clickCommand(note{target: "pay:1.0", borrowed: "=bill:@3"}); got != want {
		t.Errorf("got %s", got)
	}
}

// fakeBin writes executables that log their arguments, one line each, to log.
func fakeBin(t *testing.T, dir string, names ...string) string {
	t.Helper()
	log := filepath.Join(t.TempDir(), "calls.log")
	for _, name := range names {
		script := "#!/bin/sh\nprintf '%s' \"" + name + "\" >> " + strconv.Quote(log) + "\nfor a in \"$@\"; do printf ' [%s]' \"$a\" >> " + strconv.Quote(log) + "; done\necho >> " + strconv.Quote(log) + "\n"
		if name == "claudia-notify-fails" {
			name = "claudia-notify"
			script += "echo denied; exit 1\n"
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return log
}

func TestPost(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	if Notifier() != "" {
		t.Fatal("notifier found in an empty home")
	}
	appBin := filepath.Join(home, "Applications", "Claudia.app", "Contents", "MacOS")
	log := fakeBin(t, appBin, "claudia-notify")
	if Notifier() != filepath.Join(appBin, "claudia-notify") {
		t.Fatalf("notifier %q", Notifier())
	}

	post(note{title: "T", subtitle: "S", body: "B", thread: "claude", id: "claude:1", target: "pay:1.0"})
	post(note{title: "T", subtitle: "S", body: "B", thread: "review", id: "review:x", url: "https://x"})
	calls, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
	if len(lines) != 2 {
		t.Fatalf("calls %q", calls)
	}
	if want := `claudia-notify [post] [--title] [T] [--subtitle] [S] [--body] [B] [--thread] [claude] [--id] [claude:1] [--exec] ["tmux" switch-client -t "pay:1.0"; open -a Ghostty]`; lines[0] != want {
		t.Errorf("got  %s\nwant %s", lines[0], want)
	}
	if !strings.HasSuffix(lines[1], "[--id] [review:x] [--url] [https://x]") {
		t.Errorf("url note %s", lines[1])
	}

	// A failing helper falls back to terminal-notifier, and without that to osascript.
	fakeBin(t, appBin, "claudia-notify-fails")
	log = fakeBin(t, bin, "terminal-notifier", "osascript")
	post(note{title: "T", subtitle: "S", body: "B", id: "pipeline:x", url: "https://p"})
	post(note{title: "T", subtitle: "S", body: "B", id: "claude:1", target: "pay:1.0"})
	os.Remove(filepath.Join(bin, "terminal-notifier"))
	post(note{title: `say "hi"`, subtitle: "S", body: "B"})
	calls, _ = os.ReadFile(log)
	lines = strings.Split(strings.TrimSpace(string(calls)), "\n")
	want := []string{
		`terminal-notifier [-title] [T] [-subtitle] [S] [-message] [B] [-group] [pipeline:x] [-open] [https://p]`,
		`terminal-notifier [-title] [T] [-subtitle] [S] [-message] [B] [-group] [claude:1] [-execute] ["tmux" switch-client -t "pay:1.0"; open -a Ghostty]`,
		`osascript [-e] [display notification "B" with title "say \"hi\"" subtitle "S"]`,
	}
	if len(lines) != len(want) {
		t.Fatalf("calls %q", calls)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("got  %s\nwant %s", lines[i], want[i])
		}
	}
}

func TestRunningAndStop(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if Running() {
		t.Fatal("running without a pid file")
	}
	Stop()
	child := exec.Command("sleep", "60")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill() })
	if err := state.Save(pidFile, child.Process.Pid); err != nil {
		t.Fatal(err)
	}
	if !Running() {
		t.Error("live pid not running")
	}
	Spawn()
	if err := Run(func() {}); err == nil || err.Error() != "already running" {
		t.Errorf("second watcher: %v", err)
	}
	Stop()
	err := child.Wait()
	ws, ok := child.ProcessState.Sys().(syscall.WaitStatus)
	if err == nil || !ok || !ws.Signaled() || ws.Signal() != syscall.SIGTERM {
		t.Errorf("stop didn't SIGTERM the watcher: %v", err)
	}
	if Running() {
		t.Error("dead pid still running")
	}
	if err := state.Save(pidFile, "garbage"); err != nil {
		t.Fatal(err)
	}
	if Running() {
		t.Error("garbage pid running")
	}
	Stop()
}

func TestOneLine(t *testing.T) {
	if got := oneLine("  fix\n the\tthing "); got != "fix the thing" {
		t.Errorf("got %q", got)
	}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

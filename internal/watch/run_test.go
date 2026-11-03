package watch

import (
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/brief"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/state"
)

// fakeGlab answers `glab api … user` with script's user and every list with [].
func fakeGlab(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "glab"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
}

func serveJira(t *testing.T, h http.HandlerFunc) {
	t.Setenv("ATLASSIAN_EMAIL", "me@acme.io")
	t.Setenv("ATLASSIAN_API_TOKEN", "tok")
	jira.Lock()
	jira.handler = h
	jira.Unlock()
	t.Cleanup(func() {
		jira.Lock()
		jira.handler = nil
		jira.Unlock()
	})
}

// runWatcher runs Run, polling every millisecond, until done reports true.
func runWatcher(t *testing.T, makeBrief func(), done func() bool) error {
	t.Helper()
	oldEvery, oldStop := pollEvery, stopRun
	pollEvery, stopRun = time.Millisecond, make(chan struct{})
	t.Cleanup(func() { pollEvery, stopRun = oldEvery, oldStop })
	ended := make(chan error, 1)
	go func() { ended <- Run(makeBrief) }()
	for deadline := time.Now().Add(10 * time.Second); !done(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Error("watcher never got there")
			break
		}
	}
	close(stopRun)
	return <-ended
}

func exists(name string) bool {
	_, err := os.Stat(state.Path(name))
	return err == nil
}

func TestRun(t *testing.T) {
	privateServer(t)
	catchNotes(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	brief.Save(brief.Brief{Text: "old", At: time.Now().Add(-3 * time.Hour)})
	fakeGlab(t, `case "$*" in *" user") echo '{"username":"me"}';; *) echo '[]';; esac`)
	serveJira(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/3/myself":
			io.WriteString(w, `{"accountId":"me"}`)
		case "/rest/api/3/search/jql":
			io.WriteString(w, `{"issues":[]}`)
		default:
			http.NotFound(w, r)
		}
	})
	oldBuild, oldCheck := currentBuild, checkUpdate
	t.Cleanup(func() { currentBuild, checkUpdate = oldBuild, oldCheck })
	currentBuild = func() (string, bool) { return "1133a71d4dc3", true }
	var checks, briefs atomic.Int32
	checkUpdate = func() error { checks.Add(1); return nil }

	sawPid := false
	err := runWatcher(t, func() { briefs.Add(1) }, func() bool {
		var pid int
		sawPid = sawPid || state.Load(pidFile, &pid) && pid == os.Getpid()
		return sawPid && briefs.Load() > 0 && checks.Load() > 0 && exists("cache-gitlab.json") && exists("cache-jira.json")
	})
	if err != nil {
		t.Fatal(err)
	}
	if exists(pidFile) {
		t.Error("pid file left behind")
	}
	// The brief isn't due again for another interval.
	if n := briefs.Load(); n != 1 {
		t.Errorf("briefed %d times", n)
	}
}

func TestRunLogsFetchFailures(t *testing.T) {
	privateServer(t)
	catchNotes(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	brief.Request()
	fakeGlab(t, "echo 'not logged in' >&2; exit 1\n")
	serveJira(t, func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "down", 500) })

	r, w, _ := os.Pipe()
	oldErr := os.Stderr
	os.Stderr = w
	logged := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		logged <- string(b)
	}()
	var briefs atomic.Int32
	err := runWatcher(t, func() { briefs.Add(1) }, func() bool { return briefs.Load() > 0 })
	os.Stderr = oldErr
	w.Close()
	out := <-logged
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "glab user: not logged in") || !strings.Contains(out, "/rest/api/3/myself: 500") {
		t.Errorf("stderr %q", out)
	}
	if exists("cache-gitlab.json") || exists("cache-jira.json") {
		t.Error("failed fetch cached")
	}
}

func TestRunCantWritePid(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	os.WriteFile(filepath.Join(dir, "claudia"), nil, 0o600)
	if err := Run(func() { t.Error("briefed") }); err == nil {
		t.Error("ran without a pid file")
	}
}

func TestSpawn(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	bin := t.TempDir()
	self := filepath.Join(bin, "claudia")
	args := filepath.Join(bin, "args")
	os.WriteFile(self, []byte("#!/bin/sh\necho \"$@\" > "+args+".tmp; mv "+args+".tmp "+args+"\necho started\n"), 0o755)
	old := executable
	t.Cleanup(func() { executable = old })

	executable = func() (string, error) { return "", errors.New("gone") }
	Spawn()
	if exists("watch.log") {
		t.Error("log opened without a binary")
	}

	executable = func() (string, error) { return self, nil }
	Spawn()
	var got []byte
	for i := 0; i < 500 && len(got) == 0; i++ {
		time.Sleep(10 * time.Millisecond)
		got, _ = os.ReadFile(args)
	}
	if string(got) != "watch\n" {
		t.Errorf("spawned with %q", got)
	}
	var log []byte
	for i := 0; i < 500 && len(log) == 0; i++ {
		log, _ = os.ReadFile(state.Path("watch.log"))
		time.Sleep(10 * time.Millisecond)
	}
	if string(log) != "started\n" {
		t.Errorf("log %q", log)
	}
}

func TestSpawnWithoutStateDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	os.WriteFile(filepath.Join(dir, "claudia"), nil, 0o600)
	marker := filepath.Join(dir, "ran")
	self := filepath.Join(dir, "claudia-bin")
	os.WriteFile(self, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755)
	old := executable
	t.Cleanup(func() { executable = old })
	executable = func() (string, error) { return self, nil }
	Spawn()
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Error("started without a log to write to")
	}
}

// frontApp fakes lsappinfo, reporting app as the frontmost one.
func frontApp(t *testing.T, app string) *[]string {
	var ran []string
	old := execCommand
	execCommand = func(name string, args ...string) *exec.Cmd {
		ran = append(ran, name+" "+strings.Join(args, " "))
		if len(args) > 0 && args[0] == "front" {
			return exec.Command("echo", "ASN:0x0-0x1d01d:")
		}
		return exec.Command("echo", `"`+app+`"`)
	}
	t.Cleanup(func() { execCommand = old })
	return &ran
}

func TestGhosttyInFront(t *testing.T) {
	ran := frontApp(t, "Ghostty")
	if !ghosttyInFront() {
		t.Error("Ghostty in front")
	}
	if want := []string{"lsappinfo front", "lsappinfo info -only name ASN:0x0-0x1d01d:"}; strings.Join(*ran, "|") != strings.Join(want, "|") {
		t.Errorf("ran %q", *ran)
	}
	frontApp(t, "Safari")
	if ghosttyInFront() {
		t.Error("Safari isn't Ghostty")
	}
}

func TestSessionsQuietWhileLooking(t *testing.T) {
	privateServer(t)
	got := catchNotes(t)
	panes := startSessions(t, "pay", "bill")
	prof := t.TempDir()
	reg := registry{t, prof}
	reg.set("aaaaaaaa-pay", panes["pay"].PID, "/code/pay", claude.StatusBusy)
	w := newWatcher(claude.Profile{Name: "work", Dir: prof})
	w.sessions()

	// On screen in its own attached session.
	attachClient(t, "pay")
	frontApp(t, "Ghostty")
	reg.set("aaaaaaaa-pay", panes["pay"].PID, "/code/pay", claude.StatusWaiting)
	w.sessions()
	if n := got.take(); len(n) != 0 {
		t.Errorf("notified while looking: %+v", n)
	}
	if !looking(panes["pay"].Target() + "." + panes["pay"].Index) {
		t.Error("attached active pane with Ghostty in front isn't looked at")
	}
	frontApp(t, "Safari")
	if looking(panes["pay"].Target() + "." + panes["pay"].Index) {
		t.Error("looking with another app in front")
	}
}

func TestSessionsQuietWhileLookingAtBorrowedTab(t *testing.T) {
	privateServer(t)
	got := catchNotes(t)
	panes := startSessions(t, "pay", "bill")
	attachClient(t, "bill")
	tm(t, "set-option", "-w", "-t", panes["pay"].WindowID, "@claudia_home", "pay")
	tm(t, "link-window", "-s", panes["pay"].WindowID, "-t", "=bill:")
	tm(t, "select-window", "-t", "=bill:"+panes["pay"].WindowID)
	prof := t.TempDir()
	reg := registry{t, prof}
	reg.set("aaaaaaaa-pay", panes["pay"].PID, "/code/pay", claude.StatusBusy)
	w := newWatcher(claude.Profile{Name: "work", Dir: prof})
	w.sessions()

	frontApp(t, "Ghostty")
	reg.set("aaaaaaaa-pay", panes["pay"].PID, "/code/pay", claude.StatusWaiting)
	w.sessions()
	if n := got.take(); len(n) != 0 {
		t.Errorf("notified while looking at the tab: %+v", n)
	}
}

func TestSessionsSkipsEndedSessions(t *testing.T) {
	privateServer(t)
	got := catchNotes(t)
	prof := t.TempDir()
	writeJSON(t, filepath.Join(prof, "projects", "pay", "aaaaaaaa-old.jsonl"), map[string]string{"type": "ai-title", "aiTitle": "done"})
	w := newWatcher()
	w.store = claude.NewStore([]claude.Profile{{Name: "work", Dir: prof}}, time.Hour)
	w.sessions()
	w.sessions()
	if len(w.status) != 0 || len(got.take()) != 0 {
		t.Errorf("a session without a process is tracked: %+v", w.status)
	}
}

package watch

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/state"
	"github.com/MartyJRE/claudia/internal/summary"
	"github.com/MartyJRE/claudia/internal/tmux"
)

// privateServer points tmux at a server of the test's own, never the user's.
func privateServer(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux")
	}
	t.Setenv("TMUX", "")
	dir, err := os.MkdirTemp("/tmp", "cl") // t.TempDir is too long for a socket path
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-server").Run() })
}

func tm(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %v: %s %v", args, out, err)
	}
	return strings.TrimSpace(string(out))
}

func startSessions(t *testing.T, names ...string) map[string]tmux.Pane {
	t.Helper()
	for _, s := range names {
		tm(t, "new-session", "-d", "-s", s, "sleep 60")
	}
	panes := map[string]tmux.Pane{}
	for _, p := range tmux.Panes() {
		panes[p.Session] = p
	}
	return panes
}

// attachClient attaches a control-mode client, which tmux counts as a client
// like a terminal, so ActiveSession sees it.
func attachClient(t *testing.T, session string) {
	t.Helper()
	c := exec.Command("tmux", "-C", "attach", "-t", "="+session)
	stdin, err := c.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	c.Stdout = io.Discard
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdin.Close(); _ = c.Wait() })
	for range 1000 {
		if s, ok := tmux.ActiveSession(); ok && s == session {
			return
		}
	}
	t.Fatal("control client never attached")
}

type registry struct {
	t   *testing.T
	dir string
}

func (r registry) set(id string, pid int, cwd string, st claude.Status) {
	writeJSON(r.t, filepath.Join(r.dir, "sessions", id+".json"), claude.Live{PID: pid, SessionID: id, Cwd: cwd, Status: st})
}

func (r registry) drop(id string) {
	if err := os.Remove(filepath.Join(r.dir, "sessions", id+".json")); err != nil {
		r.t.Fatal(err)
	}
}

func TestSessionsNotifyWhenWaiting(t *testing.T) {
	privateServer(t)
	got := catchNotes(t)
	panes := startSessions(t, "pay", "bill")
	pay := panes["pay"]

	prof := t.TempDir()
	os.MkdirAll(filepath.Join(prof, "projects", "pay"), 0o755)
	os.WriteFile(filepath.Join(prof, "projects", "pay", "aaaaaaaa-pay.jsonl"), []byte(
		`{"type":"ai-title","aiTitle":"fix-thing"}`+"\n"+`{"type":"last-prompt","lastPrompt":"run\n  the tests"}`+"\n"), 0o600)
	reg := registry{t, prof}
	reg.set("aaaaaaaa-pay", pay.PID, "/code/pay", claude.StatusBusy)
	reg.set("bbbbbbbb-out", os.Getpid(), "/code/out", claude.StatusBusy)
	reg.set("cccccccc-wrk", os.Getpid(), state.WorkerDir("brief"), claude.StatusBusy)
	w := newWatcher(claude.Profile{Name: "work", Dir: prof})

	w.sessions()
	if n := got.take(); len(n) != 0 {
		t.Fatalf("baseline notified %+v", n)
	}

	reg.set("aaaaaaaa-pay", pay.PID, "/code/pay", claude.StatusWaiting)
	reg.set("bbbbbbbb-out", os.Getpid(), "/code/out", claude.StatusWaiting)
	reg.set("cccccccc-wrk", os.Getpid(), state.WorkerDir("brief"), claude.StatusWaiting)
	w.sessions()
	n := got.take()
	if len(n) != 2 {
		t.Fatalf("got %+v", n)
	}
	byID := map[string]note{n[0].id: n[0], n[1].id: n[1]}
	want := note{title: "Claude needs you", subtitle: "fix-thing", body: "pay · run the tests", thread: "claude", id: "claude:aaaaaaaa-pay",
		target: "pay:" + pay.Window + "." + pay.Index}
	if byID["claude:aaaaaaaa-pay"] != want {
		t.Errorf("in tmux:\ngot  %+v\nwant %+v", byID["claude:aaaaaaaa-pay"], want)
	}
	if out := byID["claude:bbbbbbbb-out"]; out.target != "" || out.subtitle != "bbbbbbbb" || out.body != "out" {
		t.Errorf("outside tmux %+v", out)
	}

	w.sessions()
	if n := got.take(); len(n) != 0 {
		t.Errorf("still waiting notified again: %+v", n)
	}

	reg.drop("bbbbbbbb-out")
	reg.set("aaaaaaaa-pay", pay.PID, "/code/pay", claude.StatusIdle)
	w.sessions()
	if _, ok := w.status["bbbbbbbb-out"]; ok {
		t.Error("ended session kept")
	}
	reg.set("aaaaaaaa-pay", pay.PID, "/code/pay", claude.StatusWaiting)
	w.sessions()
	if n := got.take(); len(n) != 1 || n[0].id != "claude:aaaaaaaa-pay" {
		t.Errorf("waiting again: %+v", n)
	}
}

func TestSessionsUnborrowsAnsweredTabs(t *testing.T) {
	privateServer(t)
	catchNotes(t)
	panes := startSessions(t, "pay", "bill", "cy")
	prof := t.TempDir()
	reg := registry{t, prof}
	reg.set("aaaaaaaa-pay", panes["pay"].PID, "/code/pay", claude.StatusWaiting)
	reg.set("cccccccc-cy", panes["cy"].PID, "/code/cy", claude.StatusIdle)
	if err := tmux.Borrow(panes["pay"].WindowID, "pay", "bill"); err != nil {
		t.Fatal(err)
	}
	if err := tmux.Borrow(panes["cy"].WindowID, "cy", "bill"); err != nil {
		t.Fatal(err)
	}
	w := newWatcher(claude.Profile{Name: "work", Dir: prof})
	w.sessions()
	ls := tmux.Borrowed()
	if len(ls) != 1 || ls[0].WindowID != panes["pay"].WindowID {
		t.Fatalf("only the waiting session's tab stays: %+v", ls)
	}
	reg.set("aaaaaaaa-pay", panes["pay"].PID, "/code/pay", claude.StatusBusy)
	w.sessions()
	if ls := tmux.Borrowed(); len(ls) != 0 {
		t.Errorf("answered tab kept: %+v", ls)
	}
	if len(tmux.Panes()) != 3 {
		t.Errorf("a session lost its window: %+v", tmux.Panes())
	}
}

func TestBorrowIntoActiveSession(t *testing.T) {
	privateServer(t)
	panes := startSessions(t, "pay", "bill")
	if got := borrow(panes["pay"]); got != "" {
		t.Errorf("borrowed without a client: %q", got)
	}
	attachClient(t, "bill")
	want := "=bill:" + panes["pay"].WindowID
	if got := borrow(panes["pay"]); got != want {
		t.Errorf("got %q want %q", got, want)
	}
	if got := borrow(panes["pay"]); got != want || len(tmux.Borrowed()) != 1 {
		t.Errorf("second borrow %q, links %+v", got, tmux.Borrowed())
	}
	if got := borrow(panes["bill"]); got != "" {
		t.Errorf("borrowed into its own session: %q", got)
	}
	if got := borrow(tmux.Pane{Session: "gone", WindowID: "@999"}); got != "" {
		t.Errorf("borrowed a missing window: %q", got)
	}
}

func TestSessionsBorrowForAttachedClient(t *testing.T) {
	privateServer(t)
	got := catchNotes(t)
	panes := startSessions(t, "pay", "bill")
	attachClient(t, "bill")
	prof := t.TempDir()
	reg := registry{t, prof}
	reg.set("aaaaaaaa-pay", panes["pay"].PID, "/code/pay", claude.StatusBusy)
	w := newWatcher(claude.Profile{Name: "work", Dir: prof})
	w.sessions()
	reg.set("aaaaaaaa-pay", panes["pay"].PID, "/code/pay", claude.StatusWaiting)
	w.sessions()
	n := got.take()
	if len(n) != 1 || n[0].borrowed != "=bill:"+panes["pay"].WindowID {
		t.Fatalf("got %+v", n)
	}
	if ls := tmux.Borrowed(); len(ls) != 1 || ls[0].Session != "bill" || ls[0].Viewed {
		t.Errorf("links %+v", ls)
	}
}

func TestSummarizeStoppedSessions(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	calls := make(chan string, 4)
	release := make(chan summary.Summary)
	old := summarize
	summarize = func(s claude.Session) summary.Summary { calls <- s.ID; return <-release }
	t.Cleanup(func() { summarize = old })
	at := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	sess := func(id string, at time.Time) claude.Session {
		return claude.Session{ID: id, Transcript: &claude.Transcript{Updated: at, Tail: []claude.Turn{{Role: "user", Text: "hi"}}}}
	}
	w := &watcher{summaries: map[string]summary.Summary{}, summarizing: make(chan struct{}, 1)}
	w.summarize(sess("a", at))
	w.summarize(sess("b", at)) // one at a time: b waits for a later poll
	if id := <-calls; id != "a" {
		t.Fatalf("summarized %s", id)
	}
	release <- summary.Summary{Text: "Needs you: merge?", For: at}
	w.summarizing <- struct{}{} // wait until a's run let go
	<-w.summarizing
	if got := summary.Load()["a"]; got.Text != "Needs you: merge?" {
		t.Errorf("stored %+v", got)
	}
	w.summarize(sess("a", at))
	select {
	case id := <-calls:
		t.Fatalf("summarized %s again without a new turn", id)
	default:
	}
	w.summarize(sess("a", at.Add(time.Minute)))
	<-calls
	release <- summary.Summary{For: at.Add(time.Minute), Err: "not logged in"}
	w.summarizing <- struct{}{}
	<-w.summarizing
	if got := summary.Load()["a"]; got.Text != "Needs you: merge?" || got.Err != "not logged in" {
		t.Errorf("a failure keeps the last text: %+v", got)
	}
	if len(calls) != 0 {
		t.Errorf("b was not skipped: %d calls", len(calls))
	}
	w.summarize(sess("a", at.Add(time.Minute)))
	if len(calls) != 0 || len(w.summarizing) != 0 {
		t.Error("a failure waits for the next turn")
	}
	w.summarize(claude.Session{ID: "c", Transcript: &claude.Transcript{Updated: at}})
	if len(w.summarizing) != 0 {
		t.Error("a session without a tail took the slot")
	}
	w.summarize(sess("a", at.Add(2*time.Minute)))
	<-calls
	release <- summary.Summary{Text: "Done: merged", For: at.Add(2 * time.Minute)}
	w.summarizing <- struct{}{}
	<-w.summarizing
	want := summary.Summary{Text: "Done: merged", For: at.Add(2 * time.Minute)}
	if got := summary.Load()["a"]; got != want || w.summaries["a"] != want {
		t.Errorf("a later success clears the error: stored %+v, held %+v", got, w.summaries["a"])
	}
}

func TestSessionsSummarizeOnlyStopped(t *testing.T) {
	privateServer(t)
	catchNotes(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	calls := make(chan string, 4)
	old := summarize
	summarize = func(s claude.Session) summary.Summary {
		calls <- s.ID
		return summary.Summary{Text: "Done: " + s.ID, For: s.Updated}
	}
	t.Cleanup(func() { summarize = old })
	prof := t.TempDir()
	reg := registry{t, prof}
	os.MkdirAll(filepath.Join(prof, "projects", "p"), 0o755)
	for id, st := range map[string]claude.Status{"aaaaaaaa-busy": claude.StatusBusy, "bbbbbbbb-shell": claude.StatusShell, "cccccccc-idle": claude.StatusIdle, "dddddddd-wait": claude.StatusWaiting} {
		os.WriteFile(filepath.Join(prof, "projects", "p", id+".jsonl"), []byte(
			`{"type":"user","timestamp":"2026-11-05T09:00:00Z","message":{"content":"go"}}`+"\n"), 0o600)
		reg.set(id, os.Getpid(), "/code/p", st)
	}
	w := newWatcher(claude.Profile{Name: "work", Dir: prof})
	w.summaries = map[string]summary.Summary{}
	w.summarizing = make(chan struct{}, 4)
	w.sessions()
	for range cap(w.summarizing) {
		w.summarizing <- struct{}{}
	}
	close(calls)
	var got []string
	for id := range calls {
		got = append(got, id)
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"cccccccc-idle", "dddddddd-wait"}) {
		t.Errorf("summarized %v", got)
	}
	if s := summary.Load()["dddddddd-wait"]; s.Text != "Done: dddddddd-wait" || !s.For.Equal(time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("stored %+v", s)
	}
}

package watch

import (
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/runs"
	"github.com/MartyJRE/claudia/internal/state"
	"github.com/MartyJRE/claudia/internal/summary"
)

func reply(text string) string {
	return `{"type":"user","message":{"content":"go"}}` + "\n" +
		`{"type":"assistant","message":{"content":[{"type":"text","text":"` + text + `"}]}}` + "\n"
}

func outcome(t *testing.T, st *runs.Store, subject string) string {
	t.Helper()
	got := st.For(subject)
	if len(got) != 1 {
		t.Fatalf("%s: %+v", subject, got)
	}
	return got[0].Outcome + " " + got[0].Reason
}

// The watcher settles runs as their sessions stop, so the log is current with
// the TUI closed.
func TestSessionsSettleRuns(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	catchNotes(t)
	st, err := runs.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	prof := t.TempDir()
	write := func(id, body string) {
		os.MkdirAll(filepath.Join(prof, "projects", "x"), 0o755)
		if err := os.WriteFile(filepath.Join(prof, "projects", "x", id+".jsonl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	long := time.Now().Add(-time.Hour)
	for _, id := range []string{"busy", "ended", "exits"} {
		if err := st.Start(runs.Run{ID: id, Kind: "ticket", Subject: id, Step: "plan", Session: id, Started: long}); err != nil {
			t.Fatal(err)
		}
	}
	reg := registry{t, prof}
	write("busy", reply("working on it"))
	write("ended", reply("Plan ready.\\nCLAUDIA_RESULT: ok — plan posted"))
	write("exits", reply("Here you go"))
	reg.set("busy", os.Getpid(), "/code/x", claude.StatusBusy)
	reg.set("ended", os.Getpid(), "/code/x", claude.StatusIdle)
	reg.set("exits", os.Getpid(), "/code/x", claude.StatusBusy)
	w := newWatcher(claude.Profile{Name: "work", Dir: prof})
	w.store.Window = 24 * time.Hour
	w.runs = st
	w.summaries = map[string]summary.Summary{}

	w.sessions()
	if got := outcome(t, st, "ended"); got != "ok plan posted" {
		t.Errorf("idle with a result line: %q", got)
	}
	if got := outcome(t, st, "busy"); got != "running " {
		t.Errorf("busy: %q", got)
	}

	// The busy session writes its line; nothing stopped, so nothing is read yet.
	f, _ := os.OpenFile(filepath.Join(prof, "projects", "x", "busy.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"type":"assistant","message":{"content":[{"type":"text","text":"CLAUDIA_RESULT: failed — no access"}]}}` + "\n")
	f.Close()
	w.sessions()
	if got := outcome(t, st, "busy"); got != "running " {
		t.Errorf("read without an edge: %q", got)
	}
	reg.set("busy", os.Getpid(), "/code/x", claude.StatusIdle)
	w.sessions()
	if got := outcome(t, st, "busy"); got != "failed no access" {
		t.Errorf("busy → idle: %q", got)
	}

	// An exit is an edge too; its summary says how it went.
	for _, s := range w.store.Sessions() {
		if s.ID == "exits" {
			w.summaries["exits"] = summary.Summary{Text: "Done: MR !5 open", For: s.Updated}
		}
	}
	reg.drop("exits")
	w.sessions()
	if got := outcome(t, st, "exits"); got != "ok MR !5 open" {
		t.Errorf("exited: %q", got)
	}

	// Without an edge it waits a minute before looking again.
	_ = st.Start(runs.Run{ID: "late", Subject: "late", Session: "late", Started: long})
	w.sessions()
	if got := outcome(t, st, "late"); got != "running " {
		t.Errorf("looked again too soon: %q", got)
	}
	w.settledAt = time.Now().Add(-2 * time.Minute)
	w.sessions()
	if got := outcome(t, st, "late"); got != "failed ended without a result" {
		t.Errorf("backstop: %q", got)
	}

	// A log that won't take the outcome is reported, not fatal; without one
	// there's nothing to do.
	_ = st.Start(runs.Run{ID: "stuck", Subject: "stuck", Session: "stuck", Started: long})
	db, err := sql.Open("sqlite", state.Path("state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER no BEFORE UPDATE ON runs BEGIN SELECT RAISE(ABORT, 'read only'); END`); err != nil {
		t.Fatal(err)
	}
	logged := stderr(t, func() { w.settle(nil, true) })
	if !strings.Contains(logged, "runs: ") || !strings.Contains(logged, "read only") {
		t.Errorf("stderr %q", logged)
	}
	w.runs = nil
	w.settle(nil, true)
}

func stderr(t *testing.T, f func()) string {
	t.Helper()
	r, w, _ := os.Pipe()
	old := os.Stderr
	os.Stderr = w
	f()
	os.Stderr = old
	w.Close()
	b, _ := io.ReadAll(r)
	return string(b)
}

func TestRunWithoutRunLog(t *testing.T) {
	privateServer(t)
	catchNotes(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	fakeGlab(t, "exit 1\n")
	old := openRuns
	var opened atomic.Bool
	openRuns = func() (*runs.Store, error) {
		opened.Store(true)
		return nil, errors.New("disk full")
	}
	t.Cleanup(func() { openRuns = old })
	var err error
	logged := stderr(t, func() { err = runWatcher(t, func() {}, opened.Load) })
	if err != nil || !strings.Contains(logged, "runs: disk full") {
		t.Errorf("err %v, stderr %q", err, logged)
	}
}

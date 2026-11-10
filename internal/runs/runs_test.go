package runs

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/state"
	"github.com/MartyJRE/claudia/internal/statedb"
)

func open(t *testing.T) *Store {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

var t0 = time.Date(2026, 11, 10, 9, 0, 0, 0, time.Local)

func TestMigrations(t *testing.T) {
	s := open(t)
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != len(statedb.Migrations) {
		t.Fatalf("user_version %d %v", v, err)
	}
	var mode string
	if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Errorf("journal mode %q %v", mode, err)
	}
	if err := s.Start(Run{ID: "r1", Kind: "ticket", Subject: "ABC-1", Step: "plan", Started: t0}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	// Reopening an up-to-date database migrates nothing and keeps the rows.
	again, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if got := again.For("ABC-1"); len(got) != 1 || got[0].Outcome != Running {
		t.Errorf("reopened %+v", got)
	}

	old := statedb.Migrations
	t.Cleanup(func() { statedb.Migrations = old })
	statedb.Migrations = append(slices.Clone(old), `CREATE TABLE broken (`)
	if _, err := Open(); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("migration %d", len(old)+1)) {
		t.Errorf("broken migration: %v", err)
	}
}

func TestOpenErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	if err := os.WriteFile(filepath.Join(dir, "claudia"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(); err == nil {
		t.Error("opened under a file")
	}
	os.Remove(filepath.Join(dir, "claudia"))
	if err := os.MkdirAll(state.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state.Path(statedb.File), []byte(strings.Repeat("not a database ", 100)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(); err == nil {
		t.Error("opened a file that isn't a database")
	}
}

func TestStartSettleFor(t *testing.T) {
	s := open(t)
	runs := []Run{
		{ID: "a", Kind: "ticket", Subject: "ABC-1", Step: "readiness", Session: "s-a", Started: t0},
		{ID: "b", Kind: "ticket", Subject: "ABC-1", Step: "plan", Session: "s-b", Started: t0.Add(time.Hour)},
		{ID: "c", Kind: "review", Subject: "https://gl/x/-/merge_requests/1", Step: "review", Session: "s-c", SHA: "abc", Started: t0.Add(2 * time.Hour)},
		{ID: "m", Kind: "ticket", Subject: "ABC-2", Step: "merge", Started: t0, Finished: t0, Outcome: OK, Reason: "merged"},
	}
	for _, r := range runs {
		if err := s.Start(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Start(runs[0]); err == nil {
		t.Error("the same id twice")
	}
	got := s.For("ABC-1")
	if len(got) != 2 || got[0].ID != "b" || got[1].ID != "a" || !got[0].Finished.IsZero() || !got[0].Started.Equal(t0.Add(time.Hour)) {
		t.Fatalf("For newest first: %+v", got)
	}
	if r := s.For("https://gl/x/-/merge_requests/1"); len(r) != 1 || r[0].SHA != "abc" || r[0].Kind != "review" {
		t.Errorf("review run %+v", r)
	}
	if r := s.For("ABC-2"); len(r) != 1 || r[0].Outcome != OK || !r[0].Settled() || !r[0].Finished.Equal(t0) {
		t.Errorf("merge run settled at once: %+v", r)
	}
	if r := s.Running(); len(r) != 3 || r[0].ID != "c" {
		t.Errorf("running %+v", r)
	}

	if err := s.Settle("s-a", NeedsYou, "asked 2 questions", t0.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	a := s.For("ABC-1")[1]
	if a.Outcome != NeedsYou || a.Reason != "asked 2 questions" || !a.Finished.Equal(t0.Add(3*time.Hour)) || a.Settled() {
		t.Errorf("settled %+v", a)
	}
	// A needs-you run can still end; a settled one can't change.
	_ = s.Settle("s-a", OK, "answered", t0.Add(4*time.Hour))
	_ = s.Settle("s-a", Failed, "again", t0.Add(5*time.Hour))
	if a := s.For("ABC-1")[1]; a.Outcome != OK || a.Reason != "answered" {
		t.Errorf("after settling twice %+v", a)
	}
	if r := s.Running(); len(r) != 2 {
		t.Errorf("running after settling %+v", r)
	}
	if len(s.For("nobody")) != 0 {
		t.Error("runs for an unknown subject")
	}
}

func TestBrokenRows(t *testing.T) {
	s := open(t)
	_ = s.Start(Run{ID: "ok", Subject: "X", Started: t0})
	if _, err := s.db.Exec(`INSERT INTO runs (id, kind, subject, step, started, outcome) VALUES ('bad', '', 'X', '', 'yesterday', 'running')`); err != nil {
		t.Fatal(err)
	}
	if got := s.For("X"); len(got) != 1 || got[0].ID != "ok" {
		t.Errorf("a broken row is skipped: %+v", got)
	}
	s.Close()
	if s.For("X") != nil || s.Running() != nil {
		t.Error("runs from a closed store")
	}
	if err := s.Settle("x", OK, "", t0); err == nil {
		t.Error("settled on a closed store")
	}
}

// The TUI and the watcher each hold a connection and write at once.
func TestTwoWriters(t *testing.T) {
	tui := open(t)
	watcher, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	var wg sync.WaitGroup
	errs := make(chan error, 200)
	for w, st := range []*Store{tui, watcher} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 50 {
				id := fmt.Sprintf("%d-%d", w, i)
				if err := st.Start(Run{ID: id, Kind: "ticket", Subject: "ABC-1", Step: "plan", Session: "s" + id, Started: t0.Add(time.Duration(i) * time.Second)}); err != nil {
					errs <- err
				}
				if err := st.Settle("s"+id, OK, "", t0); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if got := tui.For("ABC-1"); len(got) != 100 {
		t.Errorf("%d runs", len(got))
	}
}

func TestResult(t *testing.T) {
	for _, c := range []struct{ text, outcome, reason string }{
		{"All set.\nCLAUDIA_RESULT: ok — plan posted", OK, "plan posted"},
		{"CLAUDIA_RESULT: needs-you - asked 3 questions", NeedsYou, "asked 3 questions"},
		{"CLAUDIA_RESULT: failed: tests don't build\n", Failed, "tests don't build"},
		{"`CLAUDIA_RESULT: ok – done`", OK, "done"},
		{"CLAUDIA_RESULT:ok", OK, ""},
		{"first CLAUDIA_RESULT: failed — x\nthen CLAUDIA_RESULT: ok — y", OK, "y"},
	} {
		o, r, ok := Result(c.text)
		if !ok || o != c.outcome || r != c.reason {
			t.Errorf("%q: %q %q %v", c.text, o, r, ok)
		}
	}
	for _, text := range []string{"", "done", "CLAUDIA_RESULT: maybe", "CLAUDIA_RESULT:\nok"} {
		if _, _, ok := Result(text); ok {
			t.Errorf("%q read as a result", text)
		}
	}
}

func TestSeenOf(t *testing.T) {
	upd := t0.Add(time.Minute)
	tr := &claude.Transcript{Updated: upd, Tail: []claude.Turn{{Role: "user", Text: "go"}, {Role: "assistant", Text: "CLAUDIA_RESULT: ok"}}}
	s := claude.Session{ID: "s1", Transcript: tr, Live: &claude.Live{Status: claude.StatusBusy}}
	if sn := SeenOf(s, "Done: x", upd); sn != (Seen{Session: "s1", Live: true, Working: true, Reply: "CLAUDIA_RESULT: ok", Summary: "Done: x"}) {
		t.Errorf("busy %+v", sn)
	}
	s.Live.Status = claude.StatusIdle
	if sn := SeenOf(s, "Done: old", t0); sn.Working || sn.Summary != "" {
		t.Errorf("stale summary %+v", sn)
	}
	tr.Tail = append(tr.Tail, claude.Turn{Role: "user", Text: "more"})
	if sn := SeenOf(s, "", time.Time{}); sn.Reply != "" {
		t.Errorf("the conversation ends on a prompt: %+v", sn)
	}
	if sn := SeenOf(claude.Session{ID: "s2"}, "Done: x", time.Time{}); sn != (Seen{Session: "s2"}) {
		t.Errorf("no transcript %+v", sn)
	}
}

func TestReconcile(t *testing.T) {
	s := open(t)
	now := t0.Add(time.Hour)
	start := func(id string, age time.Duration) {
		t.Helper()
		if err := s.Start(Run{ID: id, Kind: "ticket", Subject: "ABC-1", Step: "plan", Session: id, Started: now.Add(-age)}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"busy", "idle", "marked", "asked", "doneSum", "needsSum", "silent", "gone", "fresh", "same"} {
		start(id, 10*time.Minute)
	}
	start("young", 30*time.Second)
	_ = s.Settle("same", OK, "done", now.Add(-time.Minute))
	_ = s.Start(Run{ID: "wait", Subject: "ABC-1", Session: "wait", Started: now.Add(-time.Hour), Outcome: NeedsYou})
	seen := []Seen{
		{Session: "busy", Live: true, Working: true, Reply: "CLAUDIA_RESULT: ok"},
		{Session: "idle", Live: true, Reply: "Shall I go on?", Summary: "Needs you: go on?"},
		{Session: "marked", Live: true, Reply: "CLAUDIA_RESULT: ok — plan ready"},
		{Session: "asked", Live: true, Reply: "CLAUDIA_RESULT: needs-you — asked 2 questions"},
		{Session: "doneSum", Summary: "Done: MR !5 open"},
		{Session: "needsSum", Summary: "Needs you: pick a name"},
		{Session: "silent", Summary: "Waiting: Bob"},
		{Session: "young"},
		{Session: "wait"},
	}
	changed, err := s.Reconcile(seen, now)
	if err != nil || !changed {
		t.Fatalf("changed %v %v", changed, err)
	}
	want := map[string][2]string{
		"busy":     {Running, ""},
		"idle":     {Running, ""},
		"marked":   {OK, "plan ready"},
		"asked":    {NeedsYou, "asked 2 questions"},
		"doneSum":  {OK, "MR !5 open"},
		"needsSum": {NeedsYou, "pick a name"},
		"silent":   {Waiting, "Bob"},
		"gone":     {Failed, "ended without a result"},
		"fresh":    {Failed, "ended without a result"},
		"young":    {Running, ""},
		"wait":     {NeedsYou, ""},
		"same":     {OK, "done"},
	}
	for _, r := range s.For("ABC-1") {
		if w := want[r.ID]; r.Outcome != w[0] || r.Reason != w[1] {
			t.Errorf("%s: %s %q, want %v", r.ID, r.Outcome, r.Reason, w)
		}
	}
	// Answered, the asking session carries on and ends well.
	seen[3].Reply = "CLAUDIA_RESULT: ok — all answered"
	if changed, _ := s.Reconcile(seen, now); !changed {
		t.Error("the answered run didn't change")
	}
	if changed, _ := s.Reconcile(seen, now); changed {
		t.Error("nothing new, yet changed")
	}
	s.Close()
	if _, err := s.Reconcile(nil, now); err != nil {
		t.Errorf("a closed store has no open runs: %v", err)
	}
}

func TestReconcileError(t *testing.T) {
	s := open(t)
	_ = s.Start(Run{ID: "r", Subject: "X", Session: "r", Started: t0})
	if _, err := s.db.Exec(`CREATE TRIGGER no BEFORE UPDATE ON runs BEGIN SELECT RAISE(ABORT, 'read only'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reconcile(nil, t0.Add(time.Hour)); err == nil || !strings.Contains(err.Error(), "read only") {
		t.Errorf("settle error: %v", err)
	}
}

func TestStuckAtStartup(t *testing.T) {
	s := open(t)
	now := t0.Add(time.Hour)
	for id, age := range map[string]time.Duration{"stuck": 5 * time.Minute, "young": 30 * time.Second, "dead": 5 * time.Minute} {
		if err := s.Start(Run{ID: id, Subject: "ABC-1", Step: "review", Session: id, Started: now.Add(-age)}); err != nil {
			t.Fatal(err)
		}
	}
	procs := map[string]claude.Proc{"stuck": {PID: 1}, "young": {PID: 2}, "known": {PID: 3}}
	seen := WithStarting([]Seen{{Session: "known", Live: true}}, procs)
	if len(seen) != 3 {
		t.Fatalf("seen %+v", seen)
	}
	if _, err := s.Reconcile(seen, now); err != nil {
		t.Fatal(err)
	}
	reason := func(id string) (string, string) {
		r := s.For("ABC-1")
		for _, x := range r {
			if x.ID == id {
				return x.Outcome, x.Reason
			}
		}
		return "", ""
	}
	// Past the grace period a starting process keeps its run running, holding
	// its slot, and says why; a young one is left alone; one with no process
	// left fails as before.
	if o, r := reason("stuck"); o != Running || r != StuckAtStartup {
		t.Errorf("stuck: %s %q", o, r)
	}
	if o, r := reason("young"); o != Running || r != "" {
		t.Errorf("young: %s %q", o, r)
	}
	if o, r := reason("dead"); o != Failed {
		t.Errorf("dead: %s %q", o, r)
	}
	if n := len(s.Running()); n != 2 {
		t.Errorf("running %d: a stuck run still holds its slot", n)
	}
	// Once it registers the reason clears and it carries on.
	if _, err := s.Reconcile([]Seen{{Session: "stuck", Live: true, Working: true}}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if o, r := reason("stuck"); o != Running || r != "" {
		t.Errorf("registered: %s %q", o, r)
	}
}

func TestStuckAtStartupErrors(t *testing.T) {
	s := open(t)
	_ = s.Start(Run{ID: "a", Subject: "X", Session: "a", Started: t0})
	_ = s.Start(Run{ID: "b", Subject: "X", Session: "b", Started: t0, Reason: StuckAtStartup})
	if _, err := s.db.Exec(`CREATE TRIGGER no BEFORE UPDATE ON runs BEGIN SELECT RAISE(ABORT, 'read only'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reconcile([]Seen{{Session: "a", Starting: true}}, t0.Add(time.Hour)); err == nil {
		t.Error("noting stuck: no error")
	}
	if _, err := s.Reconcile([]Seen{{Session: "a", Live: true, Working: true}, {Session: "b", Live: true}}, t0.Add(time.Hour)); err == nil {
		t.Error("clearing stuck: no error")
	}
}

func TestStuckWithTranscript(t *testing.T) {
	s := open(t)
	now := t0.Add(time.Hour)
	if err := s.Start(Run{ID: "r", Subject: "ABC-1", Step: "review", Session: "old", Started: now.Add(-5 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	// A session with a transcript but no registration: listed, not live.
	seen := WithStarting([]Seen{{Session: "old", Reply: "CLAUDIA_RESULT: ok"}, {Session: "gone"}}, map[string]claude.Proc{"old": {PID: 1}})
	if len(seen) != 2 || !seen[0].Starting || seen[1].Starting {
		t.Fatalf("seen %+v", seen)
	}
	if _, err := s.Reconcile(seen, now); err != nil {
		t.Fatal(err)
	}
	if r := s.For("ABC-1")[0]; r.Outcome != Running || r.Reason != StuckAtStartup {
		t.Errorf("run %+v", r)
	}
}

func TestWaiting(t *testing.T) {
	s := open(t)
	if o, r, ok := Result("asked\nCLAUDIA_RESULT: waiting — asked 3 questions"); !ok || o != Waiting || r != "asked 3 questions" {
		t.Errorf("result %q %q %v", o, r, ok)
	}
	if (Run{Outcome: Waiting}).Settled() || !(Run{Outcome: OK}).Settled() {
		t.Error("waiting counts as open")
	}
	_ = s.Start(Run{ID: "w", Subject: "ABC-1", Step: "readiness", Session: "a", Started: t0, StartedBy: ByAutopilot})
	_ = s.Start(Run{ID: "h", Subject: "ABC-2", Step: "plan", Session: "b", Started: t0})
	_ = s.Start(Run{ID: "m", Subject: "ABC-2", Step: "merge", Started: t0, Outcome: OK, StartedBy: ByAutopilot})
	now := t0.Add(time.Hour)
	if _, err := s.Reconcile([]Seen{{Session: "a", Live: true, Reply: "CLAUDIA_RESULT: waiting — asked 3 questions"}}, now); err != nil {
		t.Fatal(err)
	}
	// A dead session doesn't end a waiting run: it carries on once answered.
	if _, err := s.Reconcile(nil, now); err != nil {
		t.Fatal(err)
	}
	if r := s.For("ABC-1")[0]; r.Outcome != Waiting || r.Reason != "asked 3 questions" {
		t.Errorf("run %+v", r)
	}
	if by := s.SessionsStartedBy(ByAutopilot); len(by) != 1 || !by["a"] {
		t.Errorf("autopilot sessions %v", by)
	}
	// Resumed with a new run, only the newest one settles.
	_ = s.Start(Run{ID: "w2", Subject: "ABC-1", Step: "readiness", Session: "a", Started: now})
	if _, err := s.Reconcile([]Seen{{Session: "a", Live: true, Reply: "CLAUDIA_RESULT: ok — ready"}}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	rs := s.For("ABC-1")
	if rs[0].ID != "w2" || rs[0].Outcome != OK || rs[1].Outcome != Waiting {
		t.Errorf("runs %+v", rs)
	}
	if l := s.Latest(); len(l) != 2 || l["a"].ID != "w2" || l["b"].ID != "h" {
		t.Errorf("latest %+v", l)
	}
	if o, r := fallback("Waiting: Michal"); o != Waiting || r != "Michal" {
		t.Errorf("fallback %q %q", o, r)
	}
}

func TestSeenOfStarting(t *testing.T) {
	sn := SeenOf(claude.Session{ID: "a", Live: &claude.Live{Starting: true}}, "", time.Time{})
	if sn.Live || !sn.Starting {
		t.Errorf("seen %+v", sn)
	}
}

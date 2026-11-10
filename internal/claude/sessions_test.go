package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/state"
)

func writeLive(t *testing.T, dir string, l Live) {
	t.Helper()
	b, _ := json.Marshal(l)
	p := filepath.Join(dir, "sessions", strconv.Itoa(l.PID)+"-"+l.SessionID+".json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadRegistry(t *testing.T) {
	dir := t.TempDir()
	me := os.Getpid()
	writeLive(t, dir, Live{PID: me, SessionID: "live", Status: StatusBusy})
	writeLive(t, dir, Live{PID: me, SessionID: "bg", Kind: "daemon-worker"})
	writeLive(t, dir, Live{PID: me})
	writeLive(t, dir, Live{PID: 0, SessionID: "nopid"})
	writeLive(t, dir, Live{PID: 1 << 22, SessionID: "dead"})
	os.WriteFile(filepath.Join(dir, "sessions", "junk.json"), []byte("{"), 0o600)
	got := ReadRegistry(dir)
	if len(got) != 1 || got[0].SessionID != "live" || !got[0].Status.Working() {
		t.Errorf("got %+v", got)
	}
	if StatusShell.Working() != true || StatusWaiting.Working() || StatusIdle.Working() {
		t.Error("Working")
	}
}

func TestSessionAccessors(t *testing.T) {
	started := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	updated := started.Add(time.Hour)
	tr := &Transcript{Cwd: "/t", Started: started, Updated: updated, FirstPrompt: "hello\nworld"}
	bare := Session{ID: "0123456789abcdef"}
	if bare.Name() != "01234567" || bare.Dir() != "" || !bare.LastActive().IsZero() || !bare.Started().IsZero() || bare.Ticket() != "" {
		t.Errorf("bare session: %q %q", bare.Name(), bare.Dir())
	}

	s := Session{ID: "0123456789abcdef", Transcript: tr}
	if s.Name() != "hello" || s.Dir() != "/t" || !s.LastActive().Equal(updated) || !s.Started().Equal(started) {
		t.Errorf("transcript session: %q %q %v %v", s.Name(), s.Dir(), s.LastActive(), s.Started())
	}

	later := updated.Add(time.Minute)
	s.Live = &Live{Name: "named", Cwd: "/live", StartedAt: started.Add(time.Minute).UnixMilli(), UpdatedAt: later.UnixMilli()}
	if s.Name() != "hello" || s.Dir() != "/live" || !s.LastActive().Equal(later) || !s.Started().Equal(started.Add(time.Minute)) {
		t.Errorf("live session: %q %q %v %v", s.Name(), s.Dir(), s.LastActive(), s.Started())
	}
	s.Transcript = &Transcript{}
	if s.Name() != "named" {
		t.Errorf("live name %q", s.Name())
	}
}

func TestSessionTicketOrder(t *testing.T) {
	for _, c := range []struct {
		tr   Transcript
		want string
	}{
		{Transcript{Branch: "feat/ABC-1/x", AITitle: "ABC-2 thing", TicketBranch: "ABC-3"}, "ABC-1"},
		{Transcript{Branch: "devel", AITitle: "ABC-2 thing", TicketBranch: "ABC-3"}, "ABC-2"},
		{Transcript{Branch: "devel", AITitle: "thing", TicketBranch: "feat/ABC-3"}, "ABC-3"},
		{Transcript{Branch: "devel"}, ""},
	} {
		tr := c.tr
		if got := (Session{ID: "x", Transcript: &tr}).Ticket(); got != c.want {
			t.Errorf("%+v: got %q want %q", c.tr, got, c.want)
		}
	}
}

func TestStoreSessions(t *testing.T) {
	dir := t.TempDir()
	p := Profile{Name: "work", Dir: dir}
	now := time.Now()
	line := func(ts time.Time, prompt string) string {
		return `{"type":"user","cwd":"/repo","timestamp":"` + ts.UTC().Format(time.RFC3339) + `","message":{"content":"` + prompt + `"}}` + "\n"
	}
	writeTranscript(t, dir, "repo/aaaaaaaa-old.jsonl", line(now.Add(-2*time.Hour), "older"))
	writeTranscript(t, dir, "repo/bbbbbbbb-new.jsonl", line(now.Add(-time.Minute), "newer"))
	stale := writeTranscript(t, dir, "repo/cccccccc-stale.jsonl", line(now.Add(-48*time.Hour), "stale"))
	os.Chtimes(stale, now.Add(-48*time.Hour), now.Add(-48*time.Hour))
	os.Chtimes(filepath.Join(dir, "projects", "repo", "bbbbbbbb-new.jsonl"), now.Add(-time.Minute), now.Add(-time.Minute))
	os.Chtimes(filepath.Join(dir, "projects", "repo", "aaaaaaaa-old.jsonl"), now.Add(-2*time.Hour), now.Add(-2*time.Hour))

	me := os.Getpid()
	// A live session with an old transcript is listed regardless of the window,
	// and one that hasn't written a transcript yet is listed too.
	writeLive(t, dir, Live{PID: me, SessionID: "cccccccc-stale", StartedAt: now.Add(-time.Hour).UnixMilli()})
	writeLive(t, dir, Live{PID: me, SessionID: "dddddddd-fresh", Name: "fresh", StartedAt: now.Add(-time.Second).UnixMilli()})

	st := NewStore([]Profile{p}, 24*time.Hour)
	got := st.Sessions()
	var ids []string
	for _, s := range got {
		ids = append(ids, s.ID)
		if s.Profile != p {
			t.Errorf("%s profile %+v", s.ID, s.Profile)
		}
	}
	want := []string{"dddddddd-fresh", "cccccccc-stale", "bbbbbbbb-new", "aaaaaaaa-old"}
	if len(ids) != len(want) {
		t.Fatalf("got %v want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("got %v want %v", ids, want)
		}
	}
	if got[0].Transcript != nil || got[0].Name() != "fresh" {
		t.Errorf("fresh %+v", got[0])
	}
	if got[1].Name() != "stale" || got[2].Name() != "newer" {
		t.Errorf("names %q %q", got[1].Name(), got[2].Name())
	}
	again := st.Sessions()
	if again[2].Transcript != got[2].Transcript {
		t.Error("transcripts aren't cached between calls")
	}
}

func TestDefaultProfilesNamesDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.MkdirAll(filepath.Join(home, ".claude", "projects"), 0o755)
	os.WriteFile(filepath.Join(home, ".claude-file"), nil, 0o644)
	got := DefaultProfiles()
	if len(got) != 1 || got[0].Name != "default" || got[0].Dir != filepath.Join(home, ".claude") {
		t.Errorf("got %+v", got)
	}
}

func TestReadRegistryMarksWorkers(t *testing.T) {
	dir := t.TempDir()
	me := os.Getpid()
	os.MkdirAll(state.WorkerDir("brief"), 0o755)
	resolved, _ := filepath.EvalSymlinks(state.WorkerDir("brief"))
	writeLive(t, dir, Live{PID: me, SessionID: "sum", Cwd: state.WorkerDir("summary")})
	writeLive(t, dir, Live{PID: me, SessionID: "brief", Cwd: resolved})
	writeLive(t, dir, Live{PID: me, SessionID: "root", Cwd: state.WorkerDir("")})
	writeLive(t, dir, Live{PID: me, SessionID: "user", Cwd: "/src/app"})
	got := map[string]string{}
	for _, l := range ReadRegistry(dir) {
		got[l.SessionID] = l.Worker
	}
	if got["sum"] != "summary" || got["brief"] != "brief" || got["root"] != "" || got["user"] != "" || len(got) != 4 {
		t.Errorf("workers %v", got)
	}
}

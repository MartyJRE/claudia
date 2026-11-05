package summary

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/claude"
)

func session(at time.Time, tail ...claude.Turn) claude.Session {
	return claude.Session{
		ID: "s1", Profile: claude.Profile{Name: "work", Dir: "/profiles/work"},
		Transcript: &claude.Transcript{CustomTitle: "outlook mail", Updated: at, Tail: tail},
	}
}

func TestGenerate(t *testing.T) {
	at := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	var gotIn, gotDir string
	var gotExtra []string
	headless = func(system, input, dir string, extra ...string) (string, error) {
		gotIn, gotDir, gotExtra = input, dir, extra
		return "  \"Needs you: OK to send the test message?\"\nmore", nil
	}
	t.Cleanup(func() { headless = nil })
	s := Generate(session(at, claude.Turn{Role: "user", Text: "do it"}, claude.Turn{Role: "assistant", Text: "Want a test message?"}))
	if s.Text != "Needs you: OK to send the test message?" || !s.For.Equal(at) || s.Err != "" {
		t.Errorf("summary %+v", s)
	}
	if gotIn != "Title: outlook mail\n\nuser:\ndo it\n\nassistant:\nWant a test message?\n\n" || gotDir != "/profiles/work" || !slices.Equal(gotExtra, []string{"--model", "haiku"}) {
		t.Errorf("runs as the session's profile on Haiku: %q %q %v", gotIn, gotDir, gotExtra)
	}
	headless = func(string, string, string, ...string) (string, error) { return "", errors.New("not logged in") }
	if s := Generate(session(at)); s.Err != "not logged in" || !s.For.Equal(at) {
		t.Errorf("error kept with the turn it was for: %+v", s)
	}
}

func TestStaleAndStore(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	at := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	turn := claude.Turn{Role: "user", Text: "hi"}
	if Stale(session(at), nil) || Stale(claude.Session{ID: "x"}, nil) {
		t.Error("nothing to summarize without a tail")
	}
	if !Stale(session(at, turn), Load()) {
		t.Error("never summarized")
	}
	if err := Put("s1", Summary{Text: "Done: shipped", For: at}); err != nil {
		t.Fatal(err)
	}
	Put("s2", Summary{Text: "other"})
	have := Load()
	if Stale(session(at, turn), have) || have["s2"].Text != "other" || !strings.HasPrefix(have["s1"].Text, "Done") {
		t.Errorf("stored: %+v", have)
	}
	if !Stale(session(at.Add(time.Minute), turn), have) {
		t.Error("the session moved on")
	}
	if Stale(session(at.In(time.FixedZone("CET", 3600)), turn), have) {
		t.Error("the same instant in another zone is not news")
	}
	Put("s1", Summary{Text: "Needs you: merge?", For: at, Err: "x"})
	if got := Load(); len(got) != 2 || got["s1"] != (Summary{Text: "Needs you: merge?", For: at, Err: "x"}) {
		t.Errorf("Put replaces one entry: %+v", got)
	}
}

func TestLoadAndPutFailures(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	os.MkdirAll(filepath.Join(dir, "claudia"), 0o755)
	os.WriteFile(filepath.Join(dir, "claudia", file), []byte("{not json"), 0o600)
	if got := Load(); got == nil || len(got) != 0 {
		t.Errorf("a broken file loads as empty: %+v", got)
	}
	if err := Put("s1", Summary{Text: "a"}); err != nil {
		t.Fatal(err)
	}
	if got := Load(); len(got) != 1 || got["s1"].Text != "a" {
		t.Errorf("Put over a broken file: %+v", got)
	}
	blocked := filepath.Join(t.TempDir(), "file")
	os.WriteFile(blocked, nil, 0o600)
	t.Setenv("XDG_STATE_HOME", blocked)
	if err := Put("s1", Summary{}); err == nil {
		t.Error("an unwritable state dir fails")
	}
}

func TestPutConcurrently(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() { defer wg.Done(); Put(strconv.Itoa(i), Summary{Text: strconv.Itoa(i)}) }()
	}
	wg.Wait()
	if got := Load(); len(got) != 20 || got["7"].Text != "7" {
		t.Errorf("no Put lost another's entry: %d", len(got))
	}
}

func TestGenerateText(t *testing.T) {
	t.Cleanup(func() { headless = nil })
	for _, tc := range []struct{ out, want string }{
		{"Done: shipped", "Done: shipped"},
		{"\n\n  \"Waiting: Michal\"  \nsecond line", "Waiting: Michal"},
		{`"quoted "inner" text"`, `quoted "inner" text`},
		{"", ""},
		{"   \n  ", ""},
	} {
		headless = func(string, string, string, ...string) (string, error) { return tc.out, nil }
		if got := Generate(session(time.Time{})).Text; got != tc.want {
			t.Errorf("%q gave %q, want %q", tc.out, got, tc.want)
		}
	}
	var in string
	headless = func(_, input, _ string, _ ...string) (string, error) { in = input; return "x", nil }
	Generate(claude.Session{ID: "abc", Transcript: &claude.Transcript{FirstPrompt: "fix it\nplease"}})
	if in != "Title: fix it\n\n" {
		t.Errorf("an untitled session is named by its first prompt line: %q", in)
	}
	headless = func(string, string, string, ...string) (string, error) { return "Done: half", errors.New("exit 1") }
	if s := Generate(session(time.Time{})); s.Text != "Done: half" || s.Err != "exit 1" {
		t.Errorf("text and error both kept: %+v", s)
	}
}

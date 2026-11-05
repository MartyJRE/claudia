package summary

import (
	"errors"
	"slices"
	"strings"
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
}

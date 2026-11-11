package standup

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/teams"
)

func TestMain(m *testing.M) {
	dir, _ := os.MkdirTemp("", "claudia-test")
	os.Setenv("XDG_STATE_HOME", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// Wednesday 10:00.
var now = time.Date(2026, 11, 11, 10, 0, 0, 0, time.Local)

func TestSince(t *testing.T) {
	ev := func(subject string, start time.Time, resp string) teams.Event {
		return teams.Event{Subject: subject, Start: start, Response: resp}
	}
	events := []teams.Event{
		ev("Daily Standup", now.Add(-25*time.Hour), "accepted"),
		ev("Daily Standup", now.Add(-49*time.Hour), "accepted"),
		ev("Standup", now.Add(-time.Hour), "declined"),
		ev("Grooming", now.Add(-2*time.Hour), "accepted"),
		ev("Standup", now.Add(time.Hour), "accepted"),
	}
	if got := Since(events, now, config.Standup{}); !got.Equal(now.Add(-25 * time.Hour)) {
		t.Errorf("the last one I went to: %v", got)
	}
	if got := Since(events, now, config.Standup{Meeting: "Grooming"}); !got.Equal(now.Add(-2 * time.Hour)) {
		t.Errorf("my own word: %v", got)
	}
	if got := Since(nil, now, config.Standup{}); !got.Equal(time.Date(2026, 11, 10, 9, 30, 0, 0, time.Local)) {
		t.Errorf("no calendar: yesterday at 9:30, got %v", got)
	}
	monday := time.Date(2026, 11, 9, 10, 0, 0, 0, time.Local)
	if got := Since(nil, monday, config.Standup{Time: "10:15"}); !got.Equal(time.Date(2026, 11, 6, 10, 15, 0, 0, time.Local)) {
		t.Errorf("on Monday: Friday, got %v", got)
	}
}

func TestWrite(t *testing.T) {
	var gotSystem, gotInput string
	var gotExtra []string
	old := headless
	t.Cleanup(func() { headless = old })
	headless = func(kind, system, input, dir string, extra ...string) (string, error) {
		gotSystem, gotInput, gotExtra = system, input, extra
		return "Yesterday\n- x", nil
	}
	text, err := Write(map[string]int{"n": 1}, "Czech", "/p")
	if err != nil || text != "Yesterday\n- x" || !strings.Contains(gotSystem, "Write in Czech.") || !strings.Contains(gotInput, `"n": 1`) || gotExtra[1] != "haiku" {
		t.Errorf("%q %v %q %q", text, err, gotSystem, gotInput)
	}
	if _, err := Write(func() {}, "x", "/p"); err == nil {
		t.Error("facts that aren't JSON")
	}
	headless = func(string, string, string, string, ...string) (string, error) { return "", errors.New("down") }
	if _, err := Write(1, "x", "/p"); err == nil {
		t.Error("a failed run")
	}
}

func TestPoints(t *testing.T) {
	if _, ok := Load(); ok {
		t.Error("none yet")
	}
	p := Points{At: now, Since: now.Add(-time.Hour), Text: "Yesterday"}
	if err := Save(p); err != nil {
		t.Fatal(err)
	}
	if got, ok := Load(); !ok || got.Text != "Yesterday" || !got.Since.Equal(p.Since) {
		t.Errorf("loaded %+v", got)
	}
}

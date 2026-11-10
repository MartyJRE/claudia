package claude

import (
	"errors"
	"testing"
	"time"
)

func TestProcessSessions(t *testing.T) {
	old := psCommands
	t.Cleanup(func() { psCommands = old })
	psCommands = func() ([]byte, error) {
		return []byte(`  101    01:02:03 /bin/claude --session-id aaa -n review: fix the thing --append-system-prompt x
  102       00:45 /bin/claude --resume bbb
  103 2-00:00:10 claude --dangerously --model opus
  104       00:05 zsh
garbage
  abc       00:05 claude --resume ccc
`), nil
	}
	got := ProcessSessions()
	want := map[string]Proc{
		"aaa": {PID: 101, Name: "review: fix the thing", Age: time.Hour + 2*time.Minute + 3*time.Second},
		"bbb": {PID: 102, Age: 45 * time.Second},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for id, p := range want {
		if got[id] != p {
			t.Errorf("%s: %+v, want %+v", id, got[id], p)
		}
	}
	if d := elapsed("2-00:00:10"); d != 48*time.Hour+10*time.Second {
		t.Errorf("days: %v", d)
	}
	psCommands = func() ([]byte, error) { return nil, errors.New("no ps") }
	if ProcessSessions() != nil {
		t.Error("ps failing gives nothing")
	}
}

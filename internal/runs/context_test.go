package runs

import (
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/claude"
)

// Without autocompact a run whose context filled up waits for /compact: it
// needs you, which isn't a failure the back-off counts.
func TestReconcileContextFull(t *testing.T) {
	s := open(t)
	now := t0.Add(time.Hour)
	for _, id := range []string{"idle", "gone", "said"} {
		_ = s.Start(Run{ID: id, Subject: "ABC-1", Step: "implement", Session: id, Started: t0})
	}
	seen := []Seen{
		{Session: "idle", Live: true, ContextFull: true},
		{Session: "gone", ContextFull: true},
		{Session: "said", Live: true, ContextFull: true, Reply: "CLAUDIA_RESULT: ok — done"},
	}
	if _, err := s.Reconcile(seen, now); err != nil {
		t.Fatal(err)
	}
	want := map[string][2]string{"idle": {NeedsYou, ContextFull}, "gone": {NeedsYou, ContextFull}, "said": {OK, "done"}}
	for _, r := range s.For("ABC-1") {
		if w := want[r.ID]; r.Outcome != w[0] || r.Reason != w[1] {
			t.Errorf("%s: %s %q", r.ID, r.Outcome, r.Reason)
		}
	}
	tr := &claude.Transcript{Model: "claude-opus-5", Context: 192_000}
	if sn := SeenOf(claude.Session{ID: "x", Transcript: tr}, "", time.Time{}); !sn.ContextFull {
		t.Error("96% is full")
	}
	tr.Context = 100_000
	if sn := SeenOf(claude.Session{ID: "x", Transcript: tr}, "", time.Time{}); sn.ContextFull {
		t.Error("half isn't")
	}
	tr.ContextFull = true
	if sn := SeenOf(claude.Session{ID: "x", Transcript: tr}, "", time.Time{}); !sn.ContextFull {
		t.Error("the too-long error is")
	}
}

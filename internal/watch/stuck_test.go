package watch

import (
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/runs"
	"github.com/MartyJRE/claudia/internal/tmux"
)

func TestStuckNotes(t *testing.T) {
	got := catchNotes(t)
	w := newWatcher()
	all := []claude.Session{{ID: "known"}}
	procs := map[string]claude.Proc{
		"stuck": {PID: 7, Name: "review: fix it", Age: runs.Grace + time.Second},
		"young": {PID: 8, Age: time.Second},
		"mine":  {PID: 9, Age: time.Hour},
		"known": {PID: 10, Age: time.Hour},
	}
	kinds := map[string]string{"stuck": "review", "young": "review", "known": "fix"}
	w.stuckNotes(all, procs, kinds, &tmux.Locator{})
	w.stuckNotes(all, procs, kinds, &tmux.Locator{})
	n := got.take()
	if len(n) != 1 || n[0].title != "Claude is stuck at startup" || n[0].subtitle != "review: fix it" || n[0].id != "stuck:stuck" {
		t.Errorf("notes %+v", n)
	}
}

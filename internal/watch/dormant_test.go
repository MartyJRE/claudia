package watch

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/runs"
	"github.com/MartyJRE/claudia/internal/tmux"
)

func TestCloseDormant(t *testing.T) {
	privateServer(t)
	w := pilotWatcher(t)
	names := []string{"done", "waits", "mine", "split", "failed", "young", "fresh", "busy", "asks", "norun"}
	panes := startSessions(t, names...)
	tm(t, "split-window", "-t", panes["split"].ID, "sleep 60")
	now := time.Now()
	old := now.Add(-10 * time.Minute)
	start := func(id, outcome, by string, finished time.Time) {
		_ = w.runs.Start(runs.Run{ID: "r-" + id, Subject: "ABC-1", Step: "plan", Session: id, Started: old.Add(-time.Hour), Finished: finished, Outcome: outcome, StartedBy: by})
	}
	start("done", runs.OK, runs.ByAutopilot, old)
	start("waits", runs.Waiting, runs.ByAutopilot, old)
	start("mine", runs.OK, "", old)
	start("split", runs.OK, runs.ByAutopilot, old)
	start("failed", runs.Failed, runs.ByAutopilot, old)
	start("young", runs.OK, runs.ByAutopilot, old)
	start("fresh", runs.OK, runs.ByAutopilot, now.Add(-time.Minute))
	start("busy", runs.OK, runs.ByAutopilot, old)
	start("asks", runs.Waiting, runs.ByAutopilot, old)
	var all []claude.Session
	for _, id := range names {
		st, active := claude.StatusIdle, old
		switch id {
		case "young":
			active = now.Add(-30 * time.Second)
		case "busy":
			st = claude.StatusBusy
		case "asks":
			st = claude.StatusWaiting
		}
		all = append(all, claude.Session{ID: id, Transcript: &claude.Transcript{Updated: active}, Live: &claude.Live{PID: panes[id].PID, Status: st}})
	}
	all = append(all, claude.Session{ID: "closed"}, claude.Session{ID: "nopane", Live: &claude.Live{PID: 1, Status: claude.StatusIdle}})
	start("nopane", runs.OK, runs.ByAutopilot, old)
	var killed []string
	fakeSeam(t, &killWindow, func(target string) error { killed = append(killed, target); return errors.New("gone") })
	loc := tmux.NewLocator()
	w.closeDormant(all, loc, now)
	w.closeDormant(all, loc, now)
	slices.Sort(killed)
	want := []string{panes["done"].WindowID, panes["waits"].WindowID}
	slices.Sort(want)
	if !slices.Equal(killed, want) {
		t.Errorf("closed %v, want done and waits %v", killed, want)
	}
	(&watcher{}).closeDormant(all, loc, now)
}

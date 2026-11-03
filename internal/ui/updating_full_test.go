package ui

import (
	"errors"
	"slices"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MartyJRE/claudia/internal/update"
)

// runUpdate feeds the update's messages back into the model until it stops asking.
func runUpdate(t *testing.T, m *Model, cmd tea.Cmd) tea.Cmd {
	t.Helper()
	for i := 0; cmd != nil && i < 20; i++ {
		msg := cmd()
		if _, ok := msg.(tea.QuitMsg); ok {
			return cmd
		}
		_, cmd = m.Update(msg)
	}
	return cmd
}

func TestUpdateInstallsAndRestartsFull(t *testing.T) {
	m := newModelShell(t)
	ticks := instantTicks(t)
	fake(t, &fetchUpdate, func() (update.Plan, error) { return update.Plan{Latest: "abc1234"}, nil })
	fake(t, &installUpdate, func(line func(string)) error {
		line("get github.com/x: found meta tag")
		line("go: downloading github.com/x v1")
		return nil
	})
	stopped := 0
	fake(t, &stopWatcher, func() { stopped++ })

	cmd := pressShell(m, "ctrl+u")
	if m.startUpdate() != nil {
		t.Error("one update at a time")
	}
	last := runUpdate(t, m, cmd)
	if !isQuitShell(last) || !m.Result.Restart {
		t.Fatal("a finished update restarts claudia")
	}
	if m.upd.plan.Latest != "abc1234" || m.upd.stage != upDone || stopped != 1 {
		t.Errorf("plan %+v stage %d watcher stopped %d", m.upd.plan, m.upd.stage, stopped)
	}
	if !slices.Equal(m.upd.log, []string{"go: downloading github.com/x v1"}) {
		t.Errorf("meta tag lines are dropped: %q", m.upd.log)
	}
	if len(*ticks) != 1 || (*ticks)[0] != 3*time.Second {
		t.Errorf("restart after 3s: %v", *ticks)
	}
}

func TestUpdateFailsFull(t *testing.T) {
	m := newModelShell(t)
	fake(t, &fetchUpdate, func() (update.Plan, error) { return update.Plan{}, errors.New("rate limited") })
	fake(t, &installUpdate, func(func(string)) error { return errors.New("go install failed") })
	fake(t, &stopWatcher, func() { t.Error("a failed build leaves the watcher alone") })

	if last := runUpdate(t, m, m.startUpdate()); last != nil {
		t.Error("a failed update waits for esc")
	}
	if m.upd.planErr == nil || m.upd.err == nil || m.upd.err.Error() != "go install failed" || m.Result.Restart {
		t.Errorf("errors are kept for the screen: %+v", m.upd)
	}
}

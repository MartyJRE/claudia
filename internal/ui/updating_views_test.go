package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MartyJRE/claudia/internal/update"
)

// The plan message starts a real install, so these tests put the screen in
// the building stage by hand and drive it from there.
func buildingViews(t *testing.T, plan update.Plan) *Model {
	m := newModelViews(t)
	m.upd = &updateView{stage: upBuilding, plan: plan, stream: make(chan tea.Msg, 4)}
	return m
}

func TestUpdateStartsOnce(t *testing.T) {
	m := newModelViews(t)
	if cmd := m.startUpdate(); cmd == nil || m.upd == nil || m.upd.stage != upChecking {
		t.Fatal("ctrl+u opens the update screen and asks GitHub")
	}
	if m.startUpdate() != nil {
		t.Error("a second start while updating does nothing")
	}
	s := screenViews(m)
	for _, want := range []string{"updating claudia", "checking GitHub for the latest version", "what's new", "…"} {
		if !strings.Contains(s, want) {
			t.Errorf("checking screen lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "build output") {
		t.Error("no build output before building")
	}
	pressViews(m, "esc")
	if m.upd == nil {
		t.Error("esc can't leave a running update")
	}
}

func TestUpdateMessagesWithoutScreen(t *testing.T) {
	m := newModelViews(t)
	for _, msg := range []tea.Msg{upLogMsg("x"), upDoneMsg{}, upRestartMsg{}} {
		if _, cmd := m.Update(msg); cmd != nil || m.Result.Restart {
			t.Errorf("%T without the update screen is ignored", msg)
		}
	}
}

func TestUpdateBuildLog(t *testing.T) {
	m := buildingViews(t, update.Plan{Current: "0123456789abcdef", Latest: "fedcba9", Commits: []update.Commit{{SHA: "fedcba9", Subject: "ui: rounded pills"}}})
	for i := range 10 {
		_, cmd := m.Update(upLogMsg(fmt.Sprintf("go: downloading mod%d", i)))
		if cmd == nil {
			t.Fatal("each log line waits for the next")
		}
	}
	m.Update(upLogMsg("get github.com/x/y: found meta tag"))
	if len(m.upd.log) != 10 {
		t.Errorf("get lines are dropped, log has %d", len(m.upd.log))
	}
	s := screenViews(m)
	for _, want := range []string{"✓ checking GitHub for the latest version 0123456 → fedcba9", "downloading and building", "fedcba9  ui: rounded pills", "build output", "mod9"} {
		if !strings.Contains(s, want) {
			t.Errorf("building screen lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "mod1\n") || strings.Contains(s, "mod0") {
		t.Errorf("only the last 8 lines show:\n%s", s)
	}

	m.upd.stream <- upLogMsg("from the stream")
	if msg := waitStream(m.upd.stream)(); msg != upLogMsg("from the stream") {
		t.Errorf("waitStream reads the next message, got %v", msg)
	}
}

func TestUpdateFails(t *testing.T) {
	m := buildingViews(t, update.Plan{})
	m.upd.planErr = errors.New("rate limited")
	if _, cmd := m.Update(upDoneMsg{errors.New("exit status 1")}); cmd != nil {
		t.Error("a failed build schedules nothing")
	}
	s := screenViews(m)
	for _, want := range []string{"✗ downloading and building", "couldn't ask GitHub (rate limited); updating anyway", "update failed: exit status 1", "esc back to claudia"} {
		if !strings.Contains(s, want) {
			t.Errorf("failure screen lacks %q:\n%s", want, s)
		}
	}
	pressViews(m, "enter")
	if m.Result.Restart {
		t.Error("enter doesn't restart a failed update")
	}
	pressViews(m, "q")
	if m.upd != nil {
		t.Error("q leaves after a failure")
	}
}

func TestUpdateDoneRestarts(t *testing.T) {
	var commits []update.Commit
	for i := range 15 {
		commits = append(commits, update.Commit{SHA: fmt.Sprintf("c%06d", i), Subject: fmt.Sprintf("change %d", i)})
	}
	m := buildingViews(t, update.Plan{Latest: "c000000", Commits: commits})
	if _, cmd := m.Update(upDoneMsg{}); cmd == nil || m.upd.stage != upDone {
		t.Fatal("a finished build schedules the restart")
	}
	s := screenViews(m)
	for _, want := range []string{" → c000000", "✓ downloading and building", "✓ restarting the background watcher", "restarting claudia in 3…  enter now", "change 11", "… and 3 more"} {
		if !strings.Contains(s, want) {
			t.Errorf("done screen lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "change 12") {
		t.Error("only 12 commits are listed")
	}

	_, cmd := m.Update(upRestartMsg{})
	if !m.Result.Restart || cmd == nil {
		t.Fatal("the timer restarts")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("restarting quits the program")
	}

	m.Result.Restart = false
	cmd = pressViews(m, "enter")
	if !m.Result.Restart || cmd == nil {
		t.Error("enter restarts right away")
	}
	pressViews(m, "esc")
	if m.upd != nil {
		t.Error("esc goes back once it's over")
	}
}

func TestUpdateNothingNew(t *testing.T) {
	m := buildingViews(t, update.Plan{Current: "abc", Latest: "abc"})
	s := screenViews(m)
	if !strings.Contains(s, "nothing new — you already have the latest, reinstalling it") {
		t.Errorf("screen:\n%s", s)
	}
	m.width = 30
	for _, l := range strings.Split(screenViews(m), "\n") {
		if w := len([]rune(l)); w > 30 {
			t.Errorf("line wider than the terminal (%d): %q", w, l)
		}
	}
}

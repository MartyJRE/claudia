package tmux

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakePlan runs the stand-in plan prompt in a new window of the private
// server and returns its pane and the file it logs its input to.
func fakePlan(t *testing.T) (pane, log string) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3")
	}
	script, _ := filepath.Abs("testdata/fakeplan.py")
	log = filepath.Join(t.TempDir(), "input")
	pane, err := tm("new-session", "-d", "-P", "-F", "#{pane_id}", "-x", "120", "-y", "30", "python3 "+script+" "+log)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _, err := PlanPrompt(pane); return err == nil }, "the prompt to draw")
	return pane, log
}

func waitFor(t *testing.T, ok func() bool, what string) {
	t.Helper()
	for range 100 {
		if ok() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestSendPlanFeedback(t *testing.T) {
	privateServer(t)
	pane, log := fakePlan(t)
	if opt, err := PlanPrompt(pane); err != nil || opt != "3" {
		t.Fatalf("option %q %v", opt, err)
	}
	if err := SendPlanFeedback(pane, "Comments on the plan:\n\n> - retry twice\nno retries"); err != nil {
		t.Fatal(err)
	}
	var got string
	waitFor(t, func() bool { b, _ := os.ReadFile(log); got = string(b); return strings.HasSuffix(got, "\r") }, "enter")
	want := "3\x1b[200~Comments on the plan:\r\r> - retry twice\rno retries\x1b[201~\r"
	if got != want {
		t.Errorf("the prompt got\n%q\nwant\n%q", got, want)
	}
}

func TestSendPlanFeedbackWithoutPrompt(t *testing.T) {
	privateServer(t)
	pane, err := tm("new-session", "-d", "-P", "-F", "#{pane_id}", "sleep 30")
	if err != nil {
		t.Fatal(err)
	}
	if err := SendPlanFeedback(pane, "x"); !errors.Is(err, ErrNoPlanPrompt) {
		t.Errorf("no prompt: %v", err)
	}
	if err := SendPlanFeedback("%999", "x"); err == nil || errors.Is(err, ErrNoPlanPrompt) {
		t.Errorf("no such pane: %v", err)
	}
}

func TestSendPlanFeedbackWhenThePaneGoes(t *testing.T) {
	privateServer(t)
	for _, c := range []struct {
		name   string
		at     int // which pause pulls the rug
		server bool
	}{
		{"before choosing the option", 1, false},
		{"before pasting, server gone", 2, true},
		{"before pasting", 2, false},
		{"before enter", 3, false},
	} {
		pane, _ := fakePlan(t)
		n := 0
		old := pause
		pause = func() {
			if n++; n == c.at {
				if c.server {
					exec.Command("tmux", "kill-server").Run()
				} else {
					tm("kill-pane", "-t", pane)
				}
			}
		}
		if err := SendPlanFeedback(pane, "x"); err == nil {
			t.Errorf("%s: no error", c.name)
		}
		pause = old
		if c.server {
			privateServer(t)
		}
	}
}

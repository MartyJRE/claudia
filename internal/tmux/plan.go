package tmux

import (
	"errors"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// ErrNoPlanPrompt: the pane isn't showing Claude Code's plan approval prompt.
var ErrNoPlanPrompt = errors.New("the plan prompt isn't open in that pane")

var feedbackOption = regexp.MustCompile(`(\d+)\.\s+Tell Claude what to change`)

// PlanPrompt finds the plan prompt's "Tell Claude what to change" option on
// screen in the pane and returns its number.
func PlanPrompt(pane string) (string, error) {
	out, err := tm("capture-pane", "-p", "-t", pane)
	if err != nil {
		return "", err
	}
	m := feedbackOption.FindStringSubmatch(out)
	if m == nil {
		return "", ErrNoPlanPrompt
	}
	return m[1], nil
}

var pause = func() { time.Sleep(150 * time.Millisecond) }

// SendPlanFeedback answers the plan prompt in pane with feedback: it picks
// "Tell Claude what to change", pastes the text (bracketed, so its newlines
// don't submit) and presses enter, letting the prompt redraw between steps.
func SendPlanFeedback(pane, feedback string) error {
	opt, err := PlanPrompt(pane)
	if err != nil {
		return err
	}
	steps := []func() error{
		func() error { _, err := tm("send-keys", "-t", pane, opt); return err },
		func() error {
			load := exec.Command("tmux", "load-buffer", "-b", "claudia-plan", "-")
			load.Stdin = strings.NewReader(feedback)
			if out, err := load.CombinedOutput(); err != nil {
				return &cmdError{string(out), err}
			}
			_, err := tm("paste-buffer", "-p", "-d", "-b", "claudia-plan", "-t", pane)
			return err
		},
		func() error { _, err := tm("send-keys", "-t", pane, "Enter"); return err },
	}
	for _, step := range steps {
		pause()
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

package autopilot

import (
	"regexp"
	"strconv"

	"github.com/MartyJRE/claudia/internal/board"
	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/runs"
)

// Mode is a step's mode on a card: the card's own override for the step, else
// its override for every step, else the config. Merge is never auto without
// allow_auto_merge, and only implement waits for a plan OK.
func Mode(a config.Autopilot, over map[string]string, step string) (string, bool) {
	mode, own := a.Mode(step), false
	for _, k := range []string{runs.AnyStep, step} {
		if v, ok := over[k]; ok && config.ValidMode(step, v) == nil && v != "" {
			mode, own = v, true
		}
	}
	if step == "merge" && mode == config.ModeAuto && !a.AllowAutoMerge {
		mode = config.ModeAsk
	}
	return mode, own
}

// Gate is how a card's next step gets started.
type Gate int

const (
	Off     Gate = iota // never offered
	Queued              // the autopilot starts it when the limits allow
	Ask                 // it waits for s
	PlanOK              // implement waits for p on the plan
	Backoff             // it failed twice on its own; s starts it again
	Blocked             // the ticket is blocked by others that aren't done
)

// GateOf reads the gate of the card's next step. planOK: p was pressed on the card.
func GateOf(a config.Autopilot, over map[string]string, c board.Card, step string, planOK bool) Gate {
	mode, _ := Mode(a, over, step)
	switch {
	case mode == config.ModeOff:
		return Off
	case len(c.WaitsOn(step)) > 0:
		return Blocked
	case mode == config.ModeAsk:
		return Ask
	case BackedOff(c, step):
		return Backoff
	case mode == config.ModeAfterPlanOK && !planOK && !SimplePlan(c):
		return PlanOK
	}
	return Queued
}

// PlanOKed reads the stored OK of the card's plan: it counts only when given
// after the latest plan run finished, so an OK pressed before there was a plan
// doesn't wave through the one written later.
func PlanOKed(c board.Card, v string) bool {
	at, err := strconv.ParseInt(v, 10, 64)
	if v == "" || err != nil {
		return v != ""
	}
	r := c.Latest("plan")
	return r == nil || r.Finished.IsZero() || at >= r.Finished.UnixMilli()
}

// BackedOff: the step's last two runs both failed and both were the
// autopilot's; one you started resets it.
func BackedOff(c board.Card, step string) bool {
	n := 0
	for _, r := range c.Runs {
		if r.Step != step {
			continue
		}
		if r.Outcome != runs.Failed || r.StartedBy != runs.ByAutopilot {
			break
		}
		if n++; n == 2 {
			return true
		}
	}
	return false
}

var simple = regexp.MustCompile(`(?i)\bsize:\s*simple\b`)

// SimplePlan: the latest plan run said the change is simple.
func SimplePlan(c board.Card) bool {
	r := c.Latest("plan")
	return r != nil && r.Outcome == runs.OK && simple.MatchString(r.Reason)
}

// order is which step goes first when several wait: the ones closest to done.
var order = []string{"merge", "feedback", "review", "qa", "readiness", "plan", "mr", "implement"}

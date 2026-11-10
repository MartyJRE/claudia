package board

import (
	"slices"

	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/runs"
)

// A Step is what a card runs next. Each runs as a session, but merge, which
// is a GitLab call.
type Step struct{ ID, Label string }

var (
	TicketSteps = []Step{{"readiness", "readiness"}, {"plan", "plan"}, {"implement", "implement"}, {"mr", "open MR"},
		{"feedback", "feedback"}, {"merge", "merge"}, {"qa", "QA notes"}}
	// findings isn't run: it's me reading what the review found.
	ReviewSteps = []Step{{"review", "review"}, {"findings", "findings"}}
)

func StepLabel(id string) string {
	for _, s := range append(slices.Clip(TicketSteps), ReviewSteps...) {
		if s.ID == id {
			return s.Label
		}
	}
	return id
}

// Latest is the card's newest run of a step.
func (c Card) Latest(id string) *runs.Run {
	if i := slices.IndexFunc(c.Runs, func(r runs.Run) bool { return r.Step == id }); i >= 0 {
		return &c.Runs[i]
	}
	return nil
}

// Stale: a review or feedback run looked at a head the MR has moved on from.
func (c Card) Stale(r *runs.Run) bool {
	switch r.Step {
	case "review":
		return c.IsReview() && r.SHA != c.MR.SHA
	case "feedback":
		mr, ok := ThreadsMR(c)
		return ok && r.SHA != mr.SHA
	}
	return false
}

// ThreadsMR is my open MR with review threads to address.
func ThreadsMR(c Card) (gitlab.MR, bool) {
	if i := slices.IndexFunc(c.MRs, func(mr gitlab.MR) bool { return OpenMR(mr) && mr.OpenThreads() > 0 }); i >= 0 {
		return c.MRs[i], true
	}
	return gitlab.MR{}, false
}

// NextStep walks the card's pipeline from its stage: it skips a step whose
// latest run is ok and holds on one that's running or needs you, returning
// that run. Anything running holds the card: one thing at a time.
func NextStep(c Card) (string, *runs.Run) {
	if i := slices.IndexFunc(c.Runs, func(r runs.Run) bool { return r.Outcome == runs.Running }); i >= 0 {
		return c.Runs[i].Step, &c.Runs[i]
	}
	var walk []string
	switch {
	case c.IsReview():
		if c.RStage == Requested {
			walk = []string{"review"}
		}
	case c.Stage == UpNext:
		walk = []string{"readiness", "plan"}
	case c.Stage == Plan:
		walk = []string{"implement"}
	case c.Stage == Building:
		if !slices.ContainsFunc(c.MRs, OpenMR) {
			walk = []string{"mr"}
		}
	case c.Stage == Review:
		if _, ok := ThreadsMR(c); ok {
			walk = []string{"feedback"}
		}
	case c.Stage == Merge:
		walk = []string{"merge"}
	case c.Stage == QA:
		walk = []string{"qa"}
	}
	for _, id := range walk {
		r := c.Latest(id)
		switch {
		case r == nil || r.Outcome == runs.Failed || c.Stale(r):
			return id, nil
		case r.Outcome == runs.NeedsYou:
			return id, r
		}
	}
	return "", nil
}

// WaitingForAnswers: the readiness run asked questions on the ticket.
func WaitingForAnswers(c Card) bool {
	r := c.Latest("readiness")
	return c.Stage == UpNext && r != nil && r.Outcome == runs.NeedsYou
}

// StageStep is the first ticket step a stage still has ahead of it.
var StageStep = [NStages]int{UpNext: 0, Plan: 2, Building: 3, Review: 4, Merge: 5, QA: 6, Done: len(TicketSteps)}

// Passed: the card is beyond this step whether or not it ran here.
func (c Card) Passed(id string) bool {
	if c.IsReview() {
		return id == "review" && c.RStage != Requested && c.RStage != Reviewing
	}
	i := slices.IndexFunc(TicketSteps, func(s Step) bool { return s.ID == id })
	return i < StageStep[c.Stage]
}

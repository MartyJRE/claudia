package board

import (
	"slices"
	"strings"

	"github.com/MartyJRE/claudia/internal/atlassian"
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
	case c.Stage == Plan && c.Latest("implement") != nil:
		walk = []string{"implement"}
	case c.Stage == Plan:
		walk = []string{"plan", "implement"}
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
		case id == "plan" && r == nil && c.Planned():
			continue
		case r == nil || r.Outcome == runs.Failed || c.Stale(r):
			return id, nil
		case r.Outcome == runs.NeedsYou || r.Outcome == runs.Waiting:
			return id, r
		}
	}
	return "", nil
}

// Planned: there's a plan to OK, from a plan run that ended ok or from one of
// the card's sessions in plan mode. A ticket can sit in Plan by its Jira
// status alone, with no plan anywhere.
func (c Card) Planned() bool {
	if r := c.Latest("plan"); r != nil && r.Outcome == runs.OK {
		return true
	}
	return slices.ContainsFunc(c.Sessions, func(s Session) bool {
		return s.Transcript != nil && (s.Transcript.PlanPath != "" || s.Transcript.Plan != "")
	})
}

// WaitsOn is the unfinished tickets this one is blocked by, when step is one
// they hold back: readiness and plan go ahead regardless.
func (c Card) WaitsOn(step string) []string {
	if c.IsReview() || step == "" || step == "readiness" || step == "plan" {
		return nil
	}
	return c.Issue.BlockedBy
}

// WaitingForAnswers: the readiness run asked questions on the ticket. Runs
// from before the waiting outcome said so as needs-you, "asked …".
func WaitingForAnswers(c Card) bool {
	r := c.Latest("readiness")
	if c.Stage != UpNext || r == nil {
		return false
	}
	return r.Outcome == runs.Waiting || r.Outcome == runs.NeedsYou && strings.HasPrefix(r.Reason, "asked")
}

// StageStep is the first ticket step a stage still has ahead of it.
var StageStep = [NStages]int{UpNext: 0, Plan: 2, Building: 3, Review: 4, Merge: 5, QA: 6, Done: len(TicketSteps)}

// Passed: the card is beyond this step whether or not it ran here.
func (c Card) Passed(id string) bool {
	if c.IsReview() {
		return id == "review" && c.RStage != Requested && c.RStage != Reviewing
	}
	if id == "plan" && c.Stage == Plan {
		return c.Planned()
	}
	i := slices.IndexFunc(TicketSteps, func(s Step) bool { return s.ID == id })
	return i < StageStep[c.Stage]
}

// ActiveSprint is the active sprint most of issues are in, by id; 0 when none is.
func ActiveSprint(issues []atlassian.Issue) int {
	n := map[int]int{}
	best := 0
	for _, is := range issues {
		if s := is.Sprint; s != nil && s.State == "active" {
			n[s.ID]++
			if n[s.ID] > n[best] || n[s.ID] == n[best] && s.ID < best {
				best = s.ID
			}
		}
	}
	return best
}

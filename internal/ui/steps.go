package ui

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/launch"
	"github.com/MartyJRE/claudia/internal/runs"
)

// A step is what x starts on a card. Each runs as a session, but merge, which
// is a GitLab call.
type step struct{ id, label string }

var (
	ticketSteps = []step{{"readiness", "readiness"}, {"plan", "plan"}, {"implement", "implement"}, {"mr", "open MR"},
		{"feedback", "feedback"}, {"merge", "merge"}, {"qa", "QA notes"}}
	// findings isn't run: it's me reading what the review found.
	reviewSteps = []step{{"review", "review"}, {"findings", "findings"}}
)

func stepLabel(id string) string {
	for _, s := range append(slices.Clip(ticketSteps), reviewSteps...) {
		if s.id == id {
			return s.label
		}
	}
	return id
}

// sessionStep is how a ticket step starts its session: the intent, the verb
// in its title and the configured prompt.
type sessionStep struct {
	intent, verb string
	prompt       func(config.Config) string
}

var ticketSessions = map[string]sessionStep{
	"readiness": {"readiness", "check", func(c config.Config) string { return c.ReadinessPrompt }},
	"plan":      {"analysis", "analyse", func(c config.Config) string { return c.AnalysisPrompt }},
	"implement": {"implementation", "implement", func(c config.Config) string { return c.ImplementationPrompt }},
	"mr":        {"mr", "open MR", func(c config.Config) string { return c.MRPrompt }},
	"qa":        {"qa", "QA notes", func(c config.Config) string { return c.QAPrompt }},
}

// runsFor is the subject's runs, newest first, read once per refresh.
func (m *Model) runsFor(subject string) []runs.Run {
	if m.db == nil {
		return nil
	}
	if rs, ok := m.runCache[subject]; ok {
		return rs
	}
	if m.runCache == nil {
		m.runCache = map[string][]runs.Run{}
	}
	m.runCache[subject] = m.db.For(subject)
	return m.runCache[subject]
}

// latest is the card's newest run of a step.
func (c boardCard) latest(id string) *runs.Run {
	if i := slices.IndexFunc(c.runs, func(r runs.Run) bool { return r.Step == id }); i >= 0 {
		return &c.runs[i]
	}
	return nil
}

// stale: a review or feedback run looked at a head the MR has moved on from.
func (c boardCard) stale(r *runs.Run) bool {
	switch r.Step {
	case "review":
		return c.review() && r.SHA != c.mr.SHA
	case "feedback":
		mr, ok := threadsMR(c)
		return ok && r.SHA != mr.SHA
	}
	return false
}

// threadsMR is my open MR with review threads to address.
func threadsMR(c boardCard) (gitlab.MR, bool) {
	if i := slices.IndexFunc(c.mrs, func(mr gitlab.MR) bool { return openMR(mr) && mr.OpenThreads() > 0 }); i >= 0 {
		return c.mrs[i], true
	}
	return gitlab.MR{}, false
}

// nextStep walks the card's pipeline from its stage: it skips a step whose
// latest run is ok and holds on one that's running or needs you, returning
// that run. Anything running holds the card: one thing at a time.
func nextStep(c boardCard) (string, *runs.Run) {
	if i := slices.IndexFunc(c.runs, func(r runs.Run) bool { return r.Outcome == runs.Running }); i >= 0 {
		return c.runs[i].Step, &c.runs[i]
	}
	var walk []string
	switch {
	case c.review():
		if c.rstage == rsRequested {
			walk = []string{"review"}
		}
	case c.stage == stUpNext:
		walk = []string{"readiness", "plan"}
	case c.stage == stPlan:
		walk = []string{"implement"}
	case c.stage == stBuilding:
		if !slices.ContainsFunc(c.mrs, openMR) {
			walk = []string{"mr"}
		}
	case c.stage == stReview:
		if _, ok := threadsMR(c); ok {
			walk = []string{"feedback"}
		}
	case c.stage == stMerge:
		walk = []string{"merge"}
	case c.stage == stQA:
		walk = []string{"qa"}
	}
	for _, id := range walk {
		r := c.latest(id)
		switch {
		case r == nil || r.Outcome == runs.Failed || c.stale(r):
			return id, nil
		case r.Outcome == runs.NeedsYou:
			return id, r
		}
	}
	return "", nil
}

// waitingForAnswers: the readiness run asked questions on the ticket.
func waitingForAnswers(c boardCard) bool {
	r := c.latest("readiness")
	return c.stage == stUpNext && r != nil && r.Outcome == runs.NeedsYou
}

var runFrames = frames{[]string{"━", "╸", " ", "╺"}, 2}

// sPassed is a step the card's stage shows it got past, though claudia didn't run it.
var sPassed = lipgloss.NewStyle().Foreground(lipgloss.Color("#5a7348"))

// stageStep is the first ticket step a stage still has ahead of it.
var stageStep = [nStages]int{stUpNext: 0, stPlan: 2, stBuilding: 3, stReview: 4, stMerge: 5, stQA: 6, stDone: len(ticketSteps)}

// passed: the card is beyond this step whether or not it ran here.
func (c boardCard) passed(id string) bool {
	if c.review() {
		return id == "review" && c.rstage != rsRequested && c.rstage != rsReviewing
	}
	i := slices.IndexFunc(ticketSteps, func(s step) bool { return s.id == id })
	return i < stageStep[c.stage]
}

// turnStyle colours the step a card is on by whose move it is: yellow when it
// waits on me, blue while Claude carries on, grey (⧗) on someone else, and the
// accent when it's mine to start.
func turnStyle(t turn) (lipgloss.Style, string) {
	switch t {
	case turnNeedsYou:
		return lipgloss.NewStyle().Foreground(yellow), ""
	case turnAgent:
		return lipgloss.NewStyle().Foreground(blue), ""
	case turnColleague:
		return sDim, "⧗ "
	}
	return sAccent, ""
}

// currentStep is the step the strip marks: x's next step, else the first one
// the stage has ahead, so a card waiting on others still shows where it is.
func currentStep(c boardCard, next string) string {
	if next != "" || c.review() || stageStep[c.stage] >= len(ticketSteps) {
		return next
	}
	return ticketSteps[stageStep[c.stage]].id
}

// segment is one step on the strip, coloured by how its latest run went.
func (m *Model) segment(c boardCard, id, cur string) string {
	if id == "findings" {
		switch c.rstage {
		case rsFindings:
			return lipgloss.NewStyle().Foreground(yellow).Render("━")
		case rsWaiting, rsDone:
			return sOK.Render("━")
		}
		return sRule.Render("─")
	}
	r := c.latest(id)
	if r != nil && c.stale(r) {
		r = nil
	}
	switch {
	case r != nil && r.Outcome == runs.Running:
		return lipgloss.NewStyle().Foreground(blue).Render(m.glyph(runFrames))
	case id == cur:
		st, _ := turnStyle(c.turn)
		return st.Render("━")
	case r == nil && c.passed(id):
		return sPassed.Render("━")
	case r == nil:
		return sRule.Render("─")
	}
	return outcomeStyle(r.Outcome).Render("━")
}

func outcomeStyle(outcome string) lipgloss.Style {
	switch outcome {
	case runs.OK:
		return sOK
	case runs.NeedsYou:
		return lipgloss.NewStyle().Foreground(yellow)
	case runs.Failed:
		return sErr
	}
	return lipgloss.NewStyle().Foreground(blue)
}

// stepStrip is a card's last line: a segment per step, then the step x is on.
func (m *Model) stepStrip(c boardCard) string {
	steps := ticketSteps
	if c.review() {
		steps = reviewSteps
	}
	next, hold := nextStep(c)
	cur := currentStep(c, next)
	segs := make([]string, len(steps))
	for i, s := range steps {
		segs[i] = m.segment(c, s.id, cur)
	}
	strip := strings.Join(segs, " ")
	switch {
	case hold != nil:
		strip += "  " + outcomeStyle(hold.Outcome).Render(stepLabel(hold.Step))
	case cur != "":
		st, prefix := turnStyle(c.turn)
		strip += "  " + st.Render(prefix+stepLabel(cur))
	}
	return strip
}

// runLine is the detail panel's now, before and next.
func (m *Model) runLine(c boardCard) string {
	next, hold := nextStep(c)
	var parts []string
	if hold != nil {
		now := outcomeStyle(hold.Outcome).Render(stepLabel(hold.Step) + " " + hold.Outcome)
		if r, ok := m.sessionRow(hold.Session); ok && r.Transcript != nil && r.Model != "" {
			f, v := modelName(r.Model)
			now += " " + lipgloss.NewStyle().Foreground(modelColor(f)).Render(f+" "+v)
		}
		parts = append(parts, sDim.Render("now ")+now+sDim.Render(" · "+age(hold.Started)))
	}
	if i := slices.IndexFunc(c.runs, runs.Run.Settled); i >= 0 {
		r := c.runs[i]
		before := stepLabel(r.Step) + " " + outcomeStyle(r.Outcome).Render(r.Outcome)
		if r.Reason != "" {
			before += sDim.Render(" · " + r.Reason)
		}
		parts = append(parts, sDim.Render("before ")+before+sDim.Render(" · "+age(r.Finished)))
	}
	x := sKey.Render(m.key("board.run"))
	switch {
	case hold != nil:
		parts = append(parts, sDim.Render("next ")+x+sDim.Render(" jumps to it"))
	case next == "merge":
		parts = append(parts, sDim.Render("next merge · ")+x+sDim.Render(" twice merges it"))
	case next != "":
		parts = append(parts, sDim.Render("next "+stepLabel(next)+" · ")+x+sDim.Render(" starts it"))
	default:
		parts = append(parts, sDim.Render("next —"))
	}
	return strings.Join(parts, sDim.Render("   "))
}

// runHint is the footer's word for x on the card.
func runHint(c boardCard) string {
	next, hold := nextStep(c)
	switch {
	case hold != nil:
		return "jump to " + stepLabel(hold.Step)
	case next != "":
		return "run " + stepLabel(next)
	}
	return "nothing to run"
}

func (m *Model) sessionRow(id string) (row, bool) {
	if i := slices.IndexFunc(m.sessions, func(r row) bool { return r.ID == id }); i >= 0 {
		return m.sessions[i], true
	}
	return row{}, false
}

// runNext is x: it starts the card's next step, or jumps to the run it holds on.
func (m *Model) runNext(c boardCard) (tea.Model, tea.Cmd) {
	if m.db == nil {
		m.setFlash(fmt.Sprintf("the run log isn't open: %v", m.dbErr), true)
		return m, nil
	}
	next, hold := nextStep(c)
	if hold != nil {
		r, ok := m.sessionRow(hold.Session)
		switch {
		case ok && !r.live() && r.Dir() != "":
			return m.start(launch.Resume(r.Session).AsRun(hold.ID, hold.Step, hold.Subject))
		case ok:
			return m.jumpTo(r)
		case hold.Outcome == runs.Running:
			m.setFlash(stepLabel(hold.Step)+" is running, but its session isn't around", true)
			return m, nil
		}
	}
	if next == "" {
		m.setFlash("nothing for "+m.key("board.run")+" to run on "+c.ref(), false)
		return m, nil
	}
	run := runs.Run{Kind: "ticket", Subject: c.id(), Step: next}
	if c.review() {
		run.Kind = "review"
	}
	switch next {
	case "merge":
		return m.mergeStep(c, run)
	case "review":
		run.SHA = c.mr.SHA
		m.step = &run
		return m.reviewWithClaude(*c.mr)
	case "feedback":
		mr, _ := threadsMR(c)
		run.SHA = mr.SHA
		m.step = &run
		return m.addressThreads(mr)
	}
	m.step = &run
	return m.stepSession(next, c.issue.Key, c.issue.Summary)
}

// stepSession starts a ticket step's session with its configured prompt.
func (m *Model) stepSession(id, key, summary string) (tea.Model, tea.Cmd) {
	s := ticketSessions[id]
	return m.ticketSession(key, summary, s.intent, s.verb, s.verb+" "+key+" in", func(path string, err error) string {
		return fillBrief(s.prompt(getConfig()), key, path, err)
	})
}

// takeStep hands the run x is starting to the session launch that records it.
func (m *Model) takeStep() *runs.Run {
	r := m.step
	m.step = nil
	return r
}

// startRun records the session as the run x started, if it did, and tells the
// session how to report back. A failed write still starts the session.
func startRun(db *runs.Store, sp launch.Spec, id string, pending *runs.Run) (launch.Spec, error) {
	if pending == nil {
		return sp, nil
	}
	r := *pending
	r.ID, r.Session, r.Started = launch.NewID(), id, clockNow()
	if db == nil {
		return sp, errors.New("the run log isn't open")
	}
	return sp.AsRun(r.ID, r.Step, r.Subject), db.Start(r)
}

type mergedMsg struct {
	ref      string
	err, log error
}

// mergeStep merges the card's ready MR on the second press; the run is
// recorded settled.
func (m *Model) mergeStep(c boardCard, run runs.Run) (tea.Model, tea.Cmd) {
	i := slices.IndexFunc(c.mrs, mergeable)
	if i < 0 {
		m.setFlash("no MR of "+c.ref()+" is ready to merge", true)
		return m, nil
	}
	mr := c.mrs[i]
	ref := fmt.Sprintf("!%d %s", mr.IID, mr.Repo())
	if !m.confirm("board.merge:"+mr.WebURL, "merge "+ref) {
		return m, nil
	}
	db := m.db
	run.SHA = mr.SHA
	return m, func() tea.Msg {
		err := mergeMR(mr)
		run.ID, run.Started = launch.NewID(), clockNow()
		run.Finished, run.Outcome, run.Reason = run.Started, runs.OK, "merged"
		if err != nil {
			run.Outcome, run.Reason = runs.Failed, err.Error()
		}
		return mergedMsg{ref, err, db.Start(run)}
	}
}

func (m *Model) merged(msg mergedMsg) tea.Cmd {
	m.runCache = nil
	switch {
	case msg.err != nil:
		m.setFlash("merge "+msg.ref+": "+msg.err.Error(), true)
	case msg.log != nil:
		m.setFlash("merged "+msg.ref+", but the run log: "+msg.log.Error(), true)
	default:
		m.setFlash("merged "+msg.ref, false)
	}
	m.refilter()
	m.fetchedAt[srcGitlab] = time.Time{}
	return m.refreshStale()
}

// ref names the card in a flash.
func (c boardCard) ref() string {
	if c.review() {
		return fmt.Sprintf("!%d", c.mr.IID)
	}
	return c.issue.Key
}

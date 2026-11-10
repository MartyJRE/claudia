// Package board reads where each ticket and review stands off Jira, GitLab,
// the sessions and the run log, and which step comes next. The TUI draws it
// and the autopilot acts on it, so it knows nothing about either.
package board

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/runs"
)

// A ticket's stage is never stored: it's read off Jira, GitLab and the
// sessions on every refresh, so it can't drift from them.
type Stage int

const (
	UpNext Stage = iota
	Plan
	Building
	Review
	Merge
	QA
	Done
	NStages
)

var StageNames = [NStages]string{"Up next", "Plan", "Building", "Review", "Merge", "QA", "Done"}

// A review card is an MR waiting on my review; like a ticket's, its stage is
// read off GitLab and the review sessions on every refresh.
type RStage int

const (
	Requested RStage = iota
	Reviewing
	Findings
	Waiting
	Reviewed
	NRStages
)

var RStageNames = [NRStages]string{"Requested", "Reviewing", "Check findings", "Waiting on author", "Done"}

// AllStage is the ticket column a review card sits in on the All view.
var AllStage = [NRStages]Stage{UpNext, Building, Review, Merge, Done}

// Turn is whose move a card waits on, most urgent last.
type Turn int

const (
	You Turn = iota
	Colleague
	Agent
	NeedsYou
)

// The prefixes the watcher's summaries start with.
const (
	NeedsYouPrefix = "Needs you:"
	DonePrefix     = "Done:"
	WaitingPrefix  = "Waiting:"
)

// Session is a Claude session with what claudia knows of it besides.
type Session struct {
	claude.Session
	Kind    string // what claudia started it for, e.g. review
	Summary string // where it stands, written by the watcher
	// SummaryFor is the transcript update the summary was written from.
	SummaryFor time.Time
}

func (s Session) IsLive() bool { return s.Live != nil }

func (s Session) Working() bool { return s.Live != nil && s.Live.Status.Working() }

// Summarized: the summary was written from the session's latest turn.
func (s Session) Summarized(prefix string) bool {
	return strings.HasPrefix(s.Summary, prefix) && s.Transcript != nil && s.SummaryFor.Equal(s.Updated)
}

// NeedsYou: Claude is waiting on a prompt, or it stopped and its summary of
// that very turn says it's asking you something.
func (s Session) NeedsYou() bool {
	if !s.IsLive() || s.Working() {
		return false
	}
	return s.Live.Status == claude.StatusWaiting || s.Summarized(NeedsYouPrefix)
}

// Done: stopped, and its summary of that very turn says nothing is left, so
// it's likely safe to close.
func (s Session) Done() bool {
	return s.IsLive() && !s.Working() && !s.NeedsYou() && s.Summarized(DonePrefix)
}

// Activity is what a working session does and on what.
func Activity(s Session) (string, string) {
	if s.Live != nil && s.Live.Status == claude.StatusShell {
		return "running", ""
	}
	var a claude.Activity
	if s.Transcript != nil {
		a = s.Transcript.Activity
	}
	verb := cmp.Or(a.Verb, "thinking")
	if s.Kind == "review" && (verb == "reading" || verb == "thinking") {
		verb = "reviewing"
	}
	return verb, a.Detail
}

// Card is a ticket (Issue set) or a review request (MR set) on the board.
type Card struct {
	Issue    atlassian.Issue
	Stage    Stage
	Why      string // how the stage was chosen
	Sessions []Session
	MRs      []gitlab.MR
	MR       *gitlab.MR // set on a review card, which has no issue
	RStage   RStage
	Runs     []runs.Run // newest first
	// A review card's turn is read with its stage; a ticket's is the caller's.
	Turn       Turn
	Next, Verb string
}

func (c Card) IsReview() bool { return c.MR != nil }

// ID is what the card's runs and overrides are filed under.
func (c Card) ID() string {
	if c.IsReview() {
		return c.MR.WebURL
	}
	return c.Issue.Key
}

// Cards builds a card per ticket in src, epics left out: they're worked
// through their children.
func Cards(src []atlassian.Issue, ix Index, sessions []Session, runsFor func(string) []runs.Run, statuses map[string]string) []Card {
	byID := byID(sessions)
	var cards []Card
	for _, is := range src {
		if is.Level >= 1 {
			continue
		}
		c := Card{Issue: is, MRs: ix.TicketMRs[is.Key], Runs: runsFor(is.Key)}
		for _, id := range ix.TicketSess[is.Key] {
			if s, ok := byID[id]; ok {
				c.Sessions = append(c.Sessions, s)
			}
		}
		c.Stage, c.Why = CardStage(c, statuses)
		cards = append(cards, c)
	}
	return cards
}

// ReviewCards builds a card per MR waiting on my review; drafts aren't asked yet.
func ReviewCards(gl gitlab.Snapshot, ix Index, sessions []Session, runsFor func(string) []runs.Run) []Card {
	byID := byID(sessions)
	var cards []Card
	for _, mr := range gl.Review {
		if mr.Draft {
			continue
		}
		c := Card{MR: &mr, Runs: runsFor(mr.WebURL)}
		for _, id := range ix.MRSessions[mr.WebURL] {
			if s, ok := byID[id]; ok {
				c.Sessions = append(c.Sessions, s)
			}
		}
		c.RStage, c.Turn, c.Next, c.Verb, c.Why = ReviewStage(mr, c.Sessions, c.Runs, gl.Me.Username)
		c.Stage = AllStage[c.RStage]
		cards = append(cards, c)
	}
	return cards
}

func byID(sessions []Session) map[string]Session {
	m := map[string]Session{}
	for _, s := range sessions {
		m[s.ID] = s
	}
	return m
}

func OpenMR(mr gitlab.MR) bool { return mr.State == "opened" }

func Mergeable(mr gitlab.MR) bool {
	return OpenMR(mr) && !mr.Draft && mr.ApprovalsLeft == 0 && mr.PipelineStatus() == "success" && mr.OpenThreads() == 0
}

// CardStage takes the further of what the MRs and sessions show and what the
// Jira status says: merged MRs drop out of GitLab's list after a few days, so
// a ticket in testing would otherwise fall back to Up next. statuses is
// board.statuses from the config.
func CardStage(c Card, statuses map[string]string) (Stage, string) {
	is := c.Issue
	if col, ok := statuses[is.Status]; ok {
		if st, ok := StageNamed(col); ok {
			return st, fmt.Sprintf("status %q is set to %s in board.statuses", is.Status, StageNames[st])
		}
	}
	if is.Category == "done" {
		return Done, "done in Jira"
	}
	st, why := EvidenceStage(c)
	if s, ok := StatusStage(is.Status); ok && s > st {
		return s, fmt.Sprintf("Jira status %q", is.Status)
	}
	return st, why
}

// StatusStage guesses a stage from a Jira status name, for workflows that
// board.statuses doesn't spell out.
func StatusStage(status string) (Stage, bool) {
	s := strings.ToLower(status)
	switch {
	case strings.Contains(s, "ready for analysis"):
		return UpNext, true
	case strings.Contains(s, "test"), strings.Contains(s, "qa"), strings.Contains(s, "preprod"):
		return QA, true
	case strings.Contains(s, "review"):
		return Review, true
	case strings.Contains(s, "analysis"), strings.Contains(s, "preparation"), strings.Contains(s, "ready for dev"):
		return Plan, true
	case strings.Contains(s, "progress"):
		return Building, true
	}
	return 0, false
}

// OnHold is a parked status: the ticket waits on something outside your control.
func OnHold(status string) bool {
	s := strings.ToLower(status)
	return strings.Contains(s, "hold") || strings.Contains(s, "waiting") || strings.Contains(s, "blocked")
}

// EvidenceStage walks the ladder from the furthest stage down; the first that
// matches wins.
func EvidenceStage(c Card) (Stage, string) {
	is := c.Issue
	var open []gitlab.MR
	var merged *gitlab.MR
	for i, mr := range c.MRs {
		switch {
		case OpenMR(mr):
			open = append(open, mr)
		case mr.State == "merged" && merged == nil:
			merged = &c.MRs[i]
		}
	}
	if merged != nil && len(open) == 0 {
		return QA, fmt.Sprintf("!%d is merged and the ticket isn't done", merged.IID)
	}
	if i := slices.IndexFunc(open, Mergeable); i >= 0 {
		return Merge, fmt.Sprintf("!%d is approved, its pipeline passed and no threads are open", open[i].IID)
	}
	if i := slices.IndexFunc(open, func(mr gitlab.MR) bool { return !mr.Draft }); i >= 0 {
		return Review, fmt.Sprintf("!%d is open for review", open[i].IID)
	}
	if len(open) > 0 {
		return Building, fmt.Sprintf("!%d is a draft", open[0].IID)
	}
	for _, s := range c.Sessions {
		if s.Kind == "implementation" || s.Kind == "fix" || s.Kind == "test" {
			return Building, fmt.Sprintf("%s session %q", s.Kind, s.Name())
		}
		if s.Working() && s.Transcript != nil && claude.TicketFrom(s.Branch) == is.Key {
			return Building, fmt.Sprintf("%q is working on %s", s.Name(), s.Branch)
		}
	}
	for _, s := range c.Sessions {
		if s.Kind == "analysis" {
			return Plan, fmt.Sprintf("analysis session %q", s.Name())
		}
		if s.Transcript != nil && (s.Plan != "" || s.PlanPath != "") {
			return Plan, fmt.Sprintf("%q has a plan", s.Name())
		}
	}
	return UpNext, "no plan, session or MR yet"
}

// StageNamed reads a board.statuses column, e.g. "Up next", "up-next" or "qa".
func StageNamed(s string) (Stage, bool) {
	norm := func(s string) string {
		return strings.NewReplacer(" ", "", "-", "", "_", "").Replace(strings.ToLower(s))
	}
	for st, name := range StageNames {
		if norm(name) == norm(s) {
			return Stage(st), true
		}
	}
	return 0, false
}

// ReviewStage reads where my review of an MR stands, whose turn it is, what
// comes next and, on Claude's turn, what it's doing.
func ReviewStage(mr gitlab.MR, sessions []Session, rs []runs.Run, me string) (RStage, Turn, string, string, string) {
	var reviews []Session
	for _, s := range sessions {
		if s.Kind == "review" {
			reviews = append(reviews, s)
		}
	}
	if mr.ApprovedByMe(me) {
		return Reviewed, You, "", "", "you approved it"
	}
	for _, s := range reviews {
		if s.Working() {
			verb, detail := Activity(s)
			return Reviewing, Agent, oneLine(detail), verb, fmt.Sprintf("%q is reviewing it", s.Name())
		}
	}
	if mr.ReviewedByMe() {
		return Waiting, Colleague, "waits on " + mr.Author.Username, "", "you reviewed it without approving"
	}
	if len(reviews) == 0 {
		return Requested, You, "review it", "", "you're a reviewer and haven't reviewed it yet"
	}
	last := slices.MaxFunc(reviews, func(a, b Session) int { return a.LastActive().Compare(b.LastActive()) })
	// A review run knows the head it read: only a push makes its findings stale.
	if i := slices.IndexFunc(rs, func(r runs.Run) bool { return r.Session == last.ID }); i >= 0 {
		if rs[i].SHA != mr.SHA {
			return Requested, You, "review it again", "", fmt.Sprintf("new commits since %q reviewed it", last.Name())
		}
		return Findings, NeedsYou, FindingHint(last), "", fmt.Sprintf("%q finished its review", last.Name())
	}
	// GitLab clears my review when the author asks for another, and a push or
	// reply after the review means its findings may no longer hold.
	if mr.UpdatedAt.After(last.LastActive()) {
		return Requested, You, "review it again", "", fmt.Sprintf("the MR changed after %q reviewed it", last.Name())
	}
	return Findings, NeedsYou, FindingHint(last), "", fmt.Sprintf("%q finished its review", last.Name())
}

// FindingHint is the first line of what the review session left, without the
// watcher's prefix.
func FindingHint(s Session) string {
	sum := s.Summary
	for _, p := range []string{NeedsYouPrefix, DonePrefix, WaitingPrefix} {
		sum = strings.TrimPrefix(sum, p)
	}
	sum, _, _ = strings.Cut(strings.TrimSpace(sum), "\n")
	if sum == "" {
		return "read the findings"
	}
	return sum
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

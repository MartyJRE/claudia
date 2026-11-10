package ui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/state"
)

// A ticket's stage is never stored: it's read off Jira, GitLab and the
// sessions on every refresh, so it can't drift from them.
type stage int

const (
	stUpNext stage = iota
	stPlan
	stBuilding
	stReview
	stMerge
	stQA
	stDone
	nStages
)

var stageNames = [nStages]string{"Up next", "Plan", "Building", "Review", "Merge", "QA", "Done"}

// turn is whose move a ticket waits on, most urgent last.
type turn int

const (
	turnYou turn = iota
	turnColleague
	turnAgent
	turnNeedsYou
)

type boardCard struct {
	issue    atlassian.Issue
	stage    stage
	turn     turn
	why      string // how the stage was chosen
	next     string // what the turn's owner does next
	verb     string // what the working session does, on an agent's turn
	sessions []row
	mrs      []gitlab.MR
	cost     claude.TicketCost
}

type boardPrefs struct {
	Team  bool `json:"team"`
	Done  bool `json:"done"`
	Later bool `json:"later"`
}

const boardPrefsFile = "board-prefs.json"

func loadBoardPrefs() boardPrefs {
	var p boardPrefs
	state.Load(boardPrefsFile, &p)
	return p
}

func (m *Model) saveBoardPrefs() { _ = state.Save(boardPrefsFile, m.boardPrefs) }

// boardCards builds a card per ticket in scope, epics left out: they're worked
// through their children.
func (m *Model) boardCards() []boardCard {
	src := m.jira.Sprint
	if m.boardPrefs.Team {
		src = m.jira.Team
	}
	byID := map[string]row{}
	for _, r := range m.sessions {
		byID[r.ID] = r
	}
	waiting := map[string][]inboxItem{}
	for _, it := range m.inbox() {
		if _, snoozed := m.dismissed[it.id]; it.ticket != "" && !snoozed {
			waiting[it.ticket] = append(waiting[it.ticket], it)
		}
	}
	var cards []boardCard
	for _, is := range src {
		if is.Level >= 1 {
			continue
		}
		c := boardCard{issue: is, mrs: m.ix.ticketMRs[is.Key], cost: m.costs[is.Key]}
		for _, id := range m.ix.ticketSess[is.Key] {
			if r, ok := byID[id]; ok {
				c.sessions = append(c.sessions, r)
			}
		}
		c.stage, c.why = cardStage(c)
		c.turn, c.next, c.verb = cardTurn(c, waiting[is.Key])
		cards = append(cards, c)
	}
	return cards
}

func openMR(mr gitlab.MR) bool { return mr.State == "opened" }

func mergeable(mr gitlab.MR) bool {
	return openMR(mr) && !mr.Draft && mr.ApprovalsLeft == 0 && mr.PipelineStatus() == "success" && mr.OpenThreads() == 0
}

// cardStage takes the further of what the MRs and sessions show and what the
// Jira status says: merged MRs drop out of GitLab's list after a few days, so
// a ticket in testing would otherwise fall back to Up next.
func cardStage(c boardCard) (stage, string) {
	is := c.issue
	if col, ok := getConfig().Board.Statuses[is.Status]; ok {
		if st, ok := stageNamed(col); ok {
			return st, fmt.Sprintf("status %q is set to %s in board.statuses", is.Status, stageNames[st])
		}
	}
	if is.Category == "done" {
		return stDone, "done in Jira"
	}
	st, why := evidenceStage(c)
	if s, ok := statusStage(is.Status); ok && s > st {
		return s, fmt.Sprintf("Jira status %q", is.Status)
	}
	return st, why
}

// statusStage guesses a stage from a Jira status name, for workflows that
// board.statuses doesn't spell out.
func statusStage(status string) (stage, bool) {
	s := strings.ToLower(status)
	switch {
	case strings.Contains(s, "ready for analysis"):
		return stUpNext, true
	case strings.Contains(s, "test"), strings.Contains(s, "qa"), strings.Contains(s, "preprod"):
		return stQA, true
	case strings.Contains(s, "review"):
		return stReview, true
	case strings.Contains(s, "analysis"), strings.Contains(s, "preparation"), strings.Contains(s, "ready for dev"):
		return stPlan, true
	case strings.Contains(s, "progress"):
		return stBuilding, true
	}
	return 0, false
}

// onHold is a parked status: the ticket waits on something outside your control.
func onHold(status string) bool {
	s := strings.ToLower(status)
	return strings.Contains(s, "hold") || strings.Contains(s, "waiting") || strings.Contains(s, "blocked")
}

// evidenceStage walks the ladder from the furthest stage down; the first that
// matches wins.
func evidenceStage(c boardCard) (stage, string) {
	is := c.issue
	var open []gitlab.MR
	var merged *gitlab.MR
	for i, mr := range c.mrs {
		switch {
		case openMR(mr):
			open = append(open, mr)
		case mr.State == "merged" && merged == nil:
			merged = &c.mrs[i]
		}
	}
	if merged != nil && len(open) == 0 {
		return stQA, fmt.Sprintf("!%d is merged and the ticket isn't done", merged.IID)
	}
	if i := slices.IndexFunc(open, mergeable); i >= 0 {
		return stMerge, fmt.Sprintf("!%d is approved, its pipeline passed and no threads are open", open[i].IID)
	}
	if i := slices.IndexFunc(open, func(mr gitlab.MR) bool { return !mr.Draft }); i >= 0 {
		return stReview, fmt.Sprintf("!%d is open for review", open[i].IID)
	}
	if len(open) > 0 {
		return stBuilding, fmt.Sprintf("!%d is a draft", open[0].IID)
	}
	for _, r := range c.sessions {
		if r.kind == "implementation" || r.kind == "fix" || r.kind == "test" {
			return stBuilding, fmt.Sprintf("%s session %q", r.kind, r.Name())
		}
		if r.live() && r.Live.Status.Working() && r.Transcript != nil && claude.TicketFrom(r.Branch) == is.Key {
			return stBuilding, fmt.Sprintf("%q is working on %s", r.Name(), r.Branch)
		}
	}
	for _, r := range c.sessions {
		if r.kind == "analysis" {
			return stPlan, fmt.Sprintf("analysis session %q", r.Name())
		}
		if r.Transcript != nil && (r.Plan != "" || r.PlanPath != "") {
			return stPlan, fmt.Sprintf("%q has a plan", r.Name())
		}
	}
	return stUpNext, "no plan, session or MR yet"
}

// stageNamed reads a board.statuses column, e.g. "Up next", "up-next" or "qa".
func stageNamed(s string) (stage, bool) {
	norm := func(s string) string {
		return strings.NewReplacer(" ", "", "-", "", "_", "").Replace(strings.ToLower(s))
	}
	for st, name := range stageNames {
		if norm(name) == norm(s) {
			return stage(st), true
		}
	}
	return 0, false
}

var nextStep = [nStages]string{"plan it", "implement", "open MR", "get it reviewed", "merge", "QA notes", ""}

func cardTurn(c boardCard, waiting []inboxItem) (turn, string, string) {
	for _, r := range c.sessions {
		if r.needsYou() {
			return turnNeedsYou, "answer " + r.Name(), ""
		}
	}
	if len(waiting) > 0 {
		switch it := waiting[0]; it.kind {
		case kThreads:
			return turnNeedsYou, fmt.Sprintf("address %d threads", it.mr.OpenThreads()), ""
		case kReview:
			return turnNeedsYou, fmt.Sprintf("review !%d", it.mr.IID), ""
		default:
			return turnNeedsYou, "answer " + it.who, ""
		}
	}
	for _, r := range c.sessions {
		if r.live() && r.Live.Status.Working() {
			verb, detail := activity(r)
			return turnAgent, oneLine(detail), verb
		}
	}
	for _, r := range c.sessions {
		if r.live() && r.summarized(waitingPrefix) {
			return turnColleague, strings.TrimSpace(strings.TrimPrefix(r.summary, waitingPrefix)), ""
		}
	}
	for _, mr := range c.mrs {
		if openMR(mr) && !mr.Draft && mr.ApprovalsLeft > 0 && mr.OpenThreads() == 0 && mr.PipelineStatus() != "failed" {
			return turnColleague, fmt.Sprintf("!%d waits on %d approval(s)", mr.IID, mr.ApprovalsLeft), ""
		}
	}
	if onHold(c.issue.Status) {
		return turnColleague, "on hold: " + c.issue.Status, ""
	}
	next := nextStep[c.stage]
	for _, mr := range c.mrs {
		if !openMR(mr) {
			continue
		}
		switch {
		case mr.OpenThreads() > 0:
			next = fmt.Sprintf("address %d threads", mr.OpenThreads())
		case mr.PipelineStatus() == "failed":
			next = fmt.Sprintf("fix the pipeline of !%d", mr.IID)
		}
	}
	return turnYou, next, ""
}

// boardColumns is the stages the board shows: Done only when asked for.
func (m *Model) boardColumns() []stage {
	cols := []stage{stUpNext, stPlan, stBuilding, stReview, stMerge, stQA}
	if m.boardPrefs.Done {
		cols = append(cols, stDone)
	}
	return cols
}

// layoutBoard lays the cards into the shown columns, needs-you cards first and
// Jira's rank after that.
func (m *Model) layoutBoard(cards []boardCard, match func(...string) bool) [][]boardCard {
	cols := m.boardColumns()
	out := make([][]boardCard, len(cols))
	m.boardLater = 0
	for _, c := range cards {
		i := slices.Index(cols, c.stage)
		is := c.issue
		if i < 0 || !match(is.Key, is.Summary, is.Status, is.Assignee, stageNames[c.stage], c.next) {
			continue
		}
		if later(c) && !m.boardPrefs.Later {
			m.boardLater++
			continue
		}
		out[i] = append(out[i], c)
	}
	for _, col := range out {
		slices.SortStableFunc(col, func(a, b boardCard) int {
			return boolRank(b.turn == turnNeedsYou) - boolRank(a.turn == turnNeedsYou)
		})
	}
	return out
}

// later is an untouched ticket planned for a future sprint; Up next folds these
// so the current sprint's tickets aren't buried.
func later(c boardCard) bool {
	is := c.issue
	return c.stage == stUpNext && is.Category != "indeterminate" && is.Sprint != nil && is.Sprint.State == "future"
}

// currentSprint names the active sprint most of the board's cards are in.
func (m *Model) currentSprint() string {
	n := map[string]int{}
	best := ""
	for _, col := range m.vis.board {
		for _, c := range col {
			if s := c.issue.Sprint; s != nil && s.State == "active" {
				n[s.Name]++
				if n[s.Name] > n[best] || (n[s.Name] == n[best] && s.Name < best) {
					best = s.Name
				}
			}
		}
	}
	return best
}

func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

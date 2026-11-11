package ui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/autopilot"
	"github.com/MartyJRE/claudia/internal/board"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/runs"
	"github.com/MartyJRE/claudia/internal/state"
)

type (
	stage = board.Stage
	turn  = board.Turn
)

const (
	stUpNext   = board.UpNext
	stPlan     = board.Plan
	stBuilding = board.Building
	stReview   = board.Review
	stMerge    = board.Merge
	stQA       = board.QA
	stDone     = board.Done
	nStages    = board.NStages

	turnYou       = board.You
	turnColleague = board.Colleague
	turnAgent     = board.Agent
	turnNeedsYou  = board.NeedsYou
)

var stageNames = board.StageNames

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
	mr       *gitlab.MR // set on a review card, which has no issue
	rstage   rstage
	runs     []runs.Run // newest first
	// What the autopilot makes of the card's next step.
	auto   bool
	gate   autopilot.Gate
	over   map[string]string
	block  string
	planOK bool
	ctx    float64 // how full the running run's context is
	hinted bool    // the strip says what to do, so the chips needn't
}

type boardPrefs struct {
	Team bool   `json:"team"`
	Done bool   `json:"done"`
	View string `json:"view,omitempty"` // tickets, reviews or all
}

// boardView is what r cycles through: my tickets, the reviews waiting on me,
// or both on the ticket columns.
type boardView int

const (
	bvTickets boardView = iota
	bvReviews
	bvAll
	nBoardViews
)

var boardViewNames = [nBoardViews]string{"Tickets", "Reviews", "All"}

func (p boardPrefs) view() boardView {
	for v, name := range boardViewNames {
		if strings.EqualFold(p.View, name) {
			return boardView(v)
		}
	}
	return bvTickets
}

const boardPrefsFile = "board-prefs.json"

func loadBoardPrefs() boardPrefs {
	var p boardPrefs
	state.Load(boardPrefsFile, &p)
	return p
}

func (m *Model) saveBoardPrefs() { _ = state.Save(boardPrefsFile, m.boardPrefs) }

// card is the boardCard as the board package reads it.
func (c boardCard) card() board.Card {
	return board.Card{Issue: c.issue, Stage: c.stage, Why: c.why, Sessions: sessionsOf(c.sessions), MRs: c.mrs, MR: c.mr, RStage: c.rstage, Runs: c.runs}
}

// fromBoard takes a board card in with its sessions' rows.
func (m *Model) fromBoard(bc board.Card, byID map[string]row) boardCard {
	c := boardCard{issue: bc.Issue, stage: bc.Stage, why: bc.Why, mrs: bc.MRs, mr: bc.MR, rstage: bc.RStage, runs: bc.Runs,
		turn: bc.Turn, next: bc.Next, verb: bc.Verb}
	for _, s := range bc.Sessions {
		c.sessions = append(c.sessions, byID[s.ID])
	}
	return c
}

func (m *Model) rowsByID() map[string]row {
	byID := map[string]row{}
	for _, r := range m.sessions {
		byID[r.ID] = r
	}
	return byID
}

// boardCards builds a card per ticket in scope.
func (m *Model) boardCards() []boardCard {
	src := m.jira.Sprint
	if m.boardPrefs.Team {
		src = m.jira.Team
	}
	// Where review cards show, a review request isn't the ticket's turn too.
	reviews := m.boardPrefs.view() != bvTickets
	waiting := map[string][]inboxItem{}
	for _, it := range m.inbox() {
		if _, snoozed := m.dismissed[it.id]; it.ticket != "" && !snoozed && (it.kind != kReview || !reviews) {
			waiting[it.ticket] = append(waiting[it.ticket], it)
		}
	}
	byID := m.rowsByID()
	var cards []boardCard
	for _, bc := range board.Cards(src, m.ix, sessionsOf(m.sessions), m.runsFor, getConfig().Board.Statuses) {
		c := m.fromBoard(bc, byID)
		c.cost = m.costs[c.issue.Key]
		m.applyAutopilot(&c)
		c.turn, c.next, c.verb = cardTurn(c, waiting[c.issue.Key])
		next, hold := nextStep(c)
		_, c.hinted = m.stripHint(c, next, hold)
		cards = append(cards, c)
	}
	return cards
}

var (
	openMR     = board.OpenMR
	mergeable  = board.Mergeable
	onHold     = board.OnHold
	stageNamed = board.StageNamed
)

func cardStage(c boardCard) (stage, string) {
	return board.CardStage(c.card(), getConfig().Board.Statuses)
}

var stageNext = [nStages]string{"plan it", "implement", "open MR", "get it reviewed", "merge", "QA notes", ""}

func cardTurn(c boardCard, waiting []inboxItem) (turn, string, string) {
	_, hold := nextStep(c)
	if hold != nil && hold.Outcome == runs.Waiting && len(waiting) == 0 {
		return turnColleague, cmp.Or(hold.Reason, "waiting"), ""
	}
	if waitingForAnswers(c) && len(waiting) == 0 {
		return turnColleague, "waiting for answers", ""
	}
	if hold != nil && hold.Outcome == runs.NeedsYou && hold.Reason == runs.ContextFull {
		return turnNeedsYou, "context full", ""
	}
	for _, r := range c.sessions {
		if r.Live != nil && r.Live.Starting {
			return turnNeedsYou, "stuck at startup", ""
		}
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
	id, _ := nextStep(c)
	if w := c.card().WaitsOn(id); len(w) > 0 {
		return turnColleague, waitsOnText(w), ""
	}
	if t, next, verb, ok := autoTurn(c, id); ok && c.gate != autopilot.Off {
		return t, next, verb
	}
	next := stageNext[c.stage]
	switch {
	case c.auto && c.gate == autopilot.Off:
		next = ""
	case id != "":
		next = stepLabel(id)
	}
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

// boardCol is one column the board shows: a ticket stage, or a review stage on
// the Reviews view.
type boardCol struct {
	name   string
	st     stage
	rs     rstage
	review bool
}

// boardColumns is the columns the board shows: Done only when asked for.
func (m *Model) boardColumns() []boardCol {
	var cols []boardCol
	if m.boardPrefs.view() == bvReviews {
		for rs := range nRStages {
			if rs != rsDone || m.boardPrefs.Done {
				cols = append(cols, boardCol{name: rstageNames[rs], rs: rs, review: true})
			}
		}
		return cols
	}
	for _, st := range []stage{stUpNext, stPlan, stBuilding, stReview, stMerge, stQA, stDone} {
		if st != stDone || m.boardPrefs.Done {
			cols = append(cols, boardCol{name: stageNames[st], st: st})
		}
	}
	return cols
}

// layoutBoard lays the cards into the shown columns, needs-you cards first and
// Jira's rank after that.
func (m *Model) layoutBoard(cards, reviews []boardCard, match func(...string) bool) [][]boardCard {
	cols := m.boardColumns()
	m.vis.boardCols = cols
	out := make([][]boardCard, len(cols))
	m.boardLater = 0
	m.boardFutures = futureSprints(cards)
	if m.boardSprint > len(m.boardFutures) {
		m.boardSprint = 0
	}
	shown := ""
	if m.boardSprint > 0 {
		shown = m.boardFutures[m.boardSprint-1]
	}
	view := m.boardPrefs.view()
	for _, c := range cards {
		i := slices.IndexFunc(cols, func(col boardCol) bool { return !col.review && col.st == c.stage })
		is := c.issue
		if i < 0 || !match(is.Key, is.Summary, is.Status, is.Assignee, stageNames[c.stage], c.next) {
			continue
		}
		switch {
		case shown != "":
			if is.Sprint == nil || is.Sprint.Name != shown {
				continue
			}
		case later(c):
			m.boardLater++
			continue
		}
		out[i] = append(out[i], c)
	}
	for _, c := range reviews {
		i := slices.IndexFunc(cols, func(col boardCol) bool {
			if view == bvAll {
				return !col.review && col.st == c.stage
			}
			return col.review && col.rs == c.rstage
		})
		// A future sprint shown on All is about tickets only.
		if i < 0 || (view == bvAll && shown != "") || !match(fmt.Sprintf("!%d", c.mr.IID), c.mr.Title, c.mr.Repo(), c.mr.Author.Username, rstageNames[c.rstage], c.next) {
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

// futureSprints are the planned sprints the cards are in, soonest first.
func futureSprints(cards []boardCard) []string {
	var sprints []*atlassian.Sprint
	for _, c := range cards {
		if s := c.issue.Sprint; s != nil && s.State == "future" && !slices.ContainsFunc(sprints, func(o *atlassian.Sprint) bool { return o.Name == s.Name }) {
			sprints = append(sprints, s)
		}
	}
	slices.SortFunc(sprints, func(a, b *atlassian.Sprint) int {
		if c := a.Start.Compare(b.Start); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	names := make([]string, len(sprints))
	for i, s := range sprints {
		names[i] = s.Name
	}
	return names
}

// currentSprint names the sprint the board shows: the one f picked, else the
// active sprint most of its cards are in.
func (m *Model) currentSprint() string {
	if m.boardSprint > 0 {
		return m.boardFutures[m.boardSprint-1]
	}
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

package ui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/gitlab"
)

const (
	boardMinCol = 24
	cardHeight  = 4
)

// refilterBoard rebuilds the cards, keeping the cursor on its card while the
// card stays in the column.
func (m *Model) refilterBoard(match func(...string) bool) {
	sel := ""
	if c, ok := m.cardUnderCursor(); ok {
		sel = c.id()
	}
	m.loadAutopilot()
	cards, reviews := m.boardCards(), m.reviewCards()
	m.boardNeeds, m.boardRevNeeds = 0, 0
	for _, c := range append(slices.Clip(cards), reviews...) {
		if c.turn == turnNeedsYou && c.stage != stDone {
			m.boardNeeds++
			if c.review() {
				m.boardRevNeeds++
			}
		}
	}
	m.vis.board = m.layoutBoard(cards, reviews, match)
	m.boardCol = max(0, min(m.boardCol, len(m.vis.board)-1))
	if len(m.vis.board[m.boardCol]) == 0 {
		if i := slices.IndexFunc(m.vis.board, func(col []boardCard) bool { return len(col) > 0 }); i >= 0 {
			m.boardCol, m.cur[vBoard] = i, m.boardCur[i]
		}
	}
	if i := slices.IndexFunc(m.vis.board[m.boardCol], func(c boardCard) bool { return c.id() == sel }); sel != "" && i >= 0 {
		m.cur[vBoard].pos = i
	}
}

// currentCard is the card under the cursor once you've stepped into the Board.
func (m *Model) currentCard() (boardCard, bool) {
	if !m.boardFocus {
		return boardCard{}, false
	}
	return m.cardUnderCursor()
}

func (m *Model) cardUnderCursor() (boardCard, bool) {
	if m.boardCol >= len(m.vis.board) {
		return boardCard{}, false
	}
	col, c := m.vis.board[m.boardCol], m.cur[vBoard].pos
	if c < 0 || c >= len(col) {
		return boardCard{}, false
	}
	return col[c], true
}

// moveColumn takes the cursor to the next column with cards; each column keeps
// where its own cursor was.
func (m *Model) moveColumn(d int) {
	to := m.boardCol + d
	for to >= 0 && to < len(m.vis.board) && len(m.vis.board[to]) == 0 {
		to += d
	}
	if to < 0 || to >= len(m.vis.board) {
		return
	}
	m.boardCur[m.boardCol] = m.cur[vBoard]
	m.boardCol = to
	m.cur[vBoard] = m.boardCur[to]
	m.cur[vBoard].clamp(len(m.vis.board[to]))
}

func (m *Model) listBoard(h int) string {
	if h <= 0 {
		return ""
	}
	if !slices.ContainsFunc(m.vis.board, func(col []boardCard) bool { return len(col) > 0 }) {
		empty := "nothing on the board — " + m.key("board.team") + " shows the team, " + m.key("board.done") + " what's done"
		switch {
		case m.boardPrefs.view() == bvReviews:
			empty = "no reviews waiting on you — " + m.key("board.done") + " shows the approved ones"
		case m.atl == nil && m.atlErr != nil:
			empty = m.atlErr.Error()
		case m.jira.FetchedAt.IsZero():
			empty = m.spin() + " asking Jira…"
		case m.boardPrefs.Team && len(m.jira.Team) == 0:
			empty = "no team sprint: set jira_project in settings"
		}
		return lipgloss.Place(m.width, h, lipgloss.Center, lipgloss.Center, sDim.Render(empty))
	}
	n := len(m.vis.board)
	shown := n
	if n*(boardMinCol+1)-1 > m.width {
		shown = max(1, (m.width+1)/(boardMinCol+1))
	}
	m.boardOff = max(0, min(m.boardOff, n-shown))
	if m.boardCol < m.boardOff {
		m.boardOff = m.boardCol
	}
	if m.boardCol >= m.boardOff+shown {
		m.boardOff = m.boardCol - shown + 1
	}
	free := m.width - (shown - 1)
	var blocks []string
	for k := range shown {
		if k > 0 {
			blocks = append(blocks, " ")
		}
		w := free / shown
		if k < free%shown {
			w++
		}
		i := m.boardOff + k
		blocks = append(blocks, m.boardColumn(i, w, h, i == m.boardOff && i > 0, i == m.boardOff+shown-1 && i < n-1))
	}
	out := lipgloss.JoinHorizontal(lipgloss.Top, blocks...)
	return lipgloss.NewStyle().Height(h).MaxHeight(h).Render(out)
}

func (m *Model) boardColumn(i, w, h int, moreLeft, moreRight bool) string {
	cards := m.vis.board[i]
	col := m.vis.boardCols[i]
	active := i == m.boardCol
	name := sBold
	if active {
		name = sAccent
	}
	left, right := "", ""
	if moreLeft {
		left = sDim.Render("‹ ")
	}
	if moreRight {
		right = sDim.Render(" ›")
	}
	lines := []string{fit(left+name.Render(col.name)+sDim.Render(fmt.Sprintf(" %d", len(cards)))+right, w), sRule.Render(strings.Repeat("─", w))}
	if len(cards) == 0 {
		lines = append(lines, fit(sDim.Render(" —"), w))
	}
	cur := &m.boardCur[i]
	if active {
		cur = &m.cur[vBoard]
	}
	room := max(1, (h-2+1)/(cardHeight+1))
	cur.clamp(len(cards))
	if cur.pos < cur.offset {
		cur.offset = cur.pos
	}
	if cur.pos >= cur.offset+room {
		cur.offset = cur.pos - room + 1
	}
	cur.offset = max(0, min(cur.offset, len(cards)-room))
	for j := cur.offset; j < min(len(cards), cur.offset+room); j++ {
		if j > cur.offset {
			lines = append(lines, "")
		}
		lines = append(lines, m.cardLines(cards[j], w, m.boardFocus && active && j == cur.pos)...)
	}
	if below := len(cards) - cur.offset - room; below > 0 && len(lines) < h {
		lines = append(lines, sDim.Render(fmt.Sprintf("  ↓ %d more", below)))
	}
	if !col.review && col.st == stUpNext && m.boardLater > 0 && len(lines) < h {
		lines = append(lines, sDim.Render(fmt.Sprintf("  + %d in future sprints  ", m.boardLater))+sKey.Render(m.key("board.later")))
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	for k := range lines {
		lines[k] = fit(lines[k], w)
	}
	return strings.Join(lines[:h], "\n")
}

func (m *Model) cardLines(c boardCard, w int, sel bool) []string {
	if c.review() {
		return m.reviewCardLines(c, w, sel)
	}
	inner := w - 2
	lines := []string{
		fit(sTicket.Render(c.issue.Key)+m.cardBadge(c), inner),
		fit(sText.Render(c.issue.Summary), inner),
		fit(cardChips(c), inner),
		fit(m.stepStrip(c), inner),
	}
	bar := "  "
	if sel {
		bar = sBar.Render("▌ ")
	}
	for i, l := range lines {
		if sel {
			l = highlight(l)
		}
		lines[i] = bar + l
	}
	return lines
}

// cardBadge leaves out the plain "you" turn: on most cards it's the default,
// and the → next hint already says what to do.
func (m *Model) cardBadge(c boardCard) string {
	if c.turn == turnYou {
		return ""
	}
	return "  " + m.turnBadge(c)
}

func (m *Model) turnBadge(c boardCard) string {
	switch c.turn {
	case turnNeedsYou:
		return lipgloss.NewStyle().Foreground(yellow).Bold(true).Render("◆ needs you")
	case turnAgent:
		if c.verb == queuedVerb {
			return lipgloss.NewStyle().Foreground(blue).Render("◷ queued")
		}
		return sOK.Render(m.activityGlyph(c.verb))
	case turnColleague:
		return sDim.Render("⧗ waiting")
	}
	return lipgloss.NewStyle().Foreground(blue).Render("● you")
}

func costText(usd float64, unknown bool) string {
	s := lipgloss.NewStyle().Foreground(yellow).Render(money(usd))
	if unknown {
		s += sDim.Render("+?")
	}
	return s
}

// cardChips is the card's dim last line: cost, sessions, MRs and what's next.
func cardChips(c boardCard) string {
	var chips []string
	if c.cost.Sessions > 0 {
		chips = append(chips, costText(c.cost.USD, c.cost.Unknown))
	}
	if n := len(c.sessions); n > 0 {
		chips = append(chips, sDim.Render(fmt.Sprintf("✻%d", n)))
	}
	for _, mr := range c.mrs {
		chips = append(chips, mrChip(mr))
	}
	for _, ch := range []string{ctxChip(c.ctx), overrideChip(c.over)} {
		if ch != "" {
			chips = append(chips, ch)
		}
	}
	if c.next != "" && !c.hinted {
		label := "→ "
		if c.turn == turnAgent || c.turn == turnColleague {
			label = ""
		}
		chips = append(chips, sDim.Render(label+c.next))
	}
	return strings.Join(chips, sDim.Render(" · "))
}

func mrChip(mr gitlab.MR) string {
	ref := fmt.Sprintf("!%d", mr.IID)
	switch {
	case mr.State == "merged":
		return lipgloss.NewStyle().Foreground(purple).Render(ref + " merged")
	case mr.Draft:
		return sDim.Render(ref + " draft")
	}
	switch mr.PipelineStatus() {
	case "success":
		return sKey.Render(ref) + sOK.Render(" ✓")
	case "failed":
		return sKey.Render(ref) + sErr.Render(" ✗")
	case "":
		return sKey.Render(ref)
	}
	return sKey.Render(ref) + sDim.Render(" …")
}

func (m *Model) detailBoard() string {
	c, ok := m.currentCard()
	if !ok {
		if m.boardFocus {
			return m.panel(nil)
		}
		return m.panel([]string{sDim.Render("↓ steps into the board, ↑ on a column's top card steps back out; outside it ←/→ switch tabs")})
	}
	if c.review() {
		return m.detailReview(c)
	}
	is := c.issue
	lines := []string{sTicket.Render(is.Key) + " " + sBold.Render(is.Summary)}
	meta := []string{sAccent.Render(stageNames[c.stage]), statusText(is)}
	sprint := ""
	if is.Sprint != nil {
		sprint = is.Sprint.Name
	}
	for _, s := range []string{typeName(is.Type), is.Assignee, sprint} {
		if s != "" {
			meta = append(meta, sDim.Render(s))
		}
	}
	if is.Parent != "" {
		meta = append(meta, sTicket.Render(is.Parent)+sDim.Render(" "+is.ParentSummary))
	}
	lines = append(lines, strings.Join(meta, sDim.Render("  ·  ")))
	turn := m.turnBadge(c)
	if c.next != "" {
		turn += sDim.Render(" · " + c.next)
	}
	lines = append(lines, turn+sDim.Render("  ·  "+c.why), m.runLine(c))
	if c.auto {
		lines = append(lines, modesLine(c))
	}
	// Sessions and MRs share a line, so the cost below still fits the panel.
	var parts []string
	for _, r := range c.sessions {
		parts = append(parts, m.status(r)+" "+sText.Render(r.Name()))
	}
	for _, mr := range c.mrs {
		parts = append(parts, m.mrRef(mr.WebURL))
	}
	if len(parts) > 0 {
		lines = append(lines, strings.Join(parts, "   "))
	}
	if c.cost.Sessions > 0 {
		lines = append(lines, m.costLine(is.Key, c))
	}
	return m.panel(lines)
}

// costLine breaks a ticket's cost down by model and by session, costliest first.
func (m *Model) costLine(key string, c boardCard) string {
	parts := []string{sDim.Render("cost ") + costText(c.cost.USD, c.cost.Unknown)}
	for _, dm := range modelRows(c.cost.Models) {
		f, v := modelName(dm.Model)
		usd, _ := dm.Cost(dm.Model)
		parts = append(parts, lipgloss.NewStyle().Foreground(modelColor(f)).Render(f+" "+v)+" "+sDim.Render(money(usd)))
	}
	names := map[string]string{}
	for _, r := range m.sessions {
		names[r.ID] = r.Name()
	}
	sess := m.ledger.Sessions(key)
	slices.SortStableFunc(sess, func(a, b claude.SessionCost) int { return cmp.Compare(b.USD, a.USD) })
	for _, s := range sess {
		name := names[s.ID]
		if name == "" {
			name = s.ID[:min(8, len(s.ID))]
		}
		parts = append(parts, sText.Render(name)+" "+sDim.Render(money(s.USD)))
	}
	return strings.Join(parts, sDim.Render("  ·  "))
}

// boardNav steps in and out of the Board. Outside it the side keys switch tabs
// as everywhere else; inside, they move between columns.
func (m *Model) boardNav(k string) (tea.Model, tea.Cmd, bool) {
	act := m.action(k)
	if !m.boardFocus {
		switch act {
		case "board.left":
			m.switchView((m.view + nViews - 1) % nViews)
		case "board.right":
			m.switchView((m.view + 1) % nViews)
		case "global.down", "global.top", "global.bottom":
			if m.count() == 0 {
				return m, nil, true
			}
			m.boardFocus = true
			return m, nil, act == "global.down"
		case "global.up":
		default:
			return m, nil, false
		}
		return m, nil, true
	}
	switch {
	case act == "global.up" && m.cur[vBoard].pos == 0, act == "global.back" && m.filter.Value() == "":
		m.boardFocus = false
		return m, nil, true
	}
	return m, nil, false
}

func (m *Model) keyBoard(k string) (tea.Model, tea.Cmd) {
	act := m.action(k)
	switch act {
	case "board.left":
		m.moveColumn(-1)
		return m, nil
	case "board.right":
		m.moveColumn(1)
		return m, nil
	case "board.team":
		m.boardPrefs.Team = !m.boardPrefs.Team
		m.saveBoardPrefs()
		m.cur[vBoard], m.boardCur = cursor{}, [nStages]cursor{}
		m.refilter()
		return m, nil
	case "board.reviews":
		m.boardPrefs.View = strings.ToLower(boardViewNames[(m.boardPrefs.view()+1)%nBoardViews])
		m.saveBoardPrefs()
		m.cur[vBoard], m.boardCur, m.boardCol, m.boardOff = cursor{}, [nStages]cursor{}, 0, 0
		m.refilter()
		m.boardFocus = m.boardFocus && m.count() > 0
		return m, nil
	case "board.done":
		m.boardPrefs.Done = !m.boardPrefs.Done
		m.saveBoardPrefs()
		m.refilter()
		return m, nil
	case "board.pause":
		m.togglePause()
		return m, nil
	case "board.later":
		if m.boardPrefs.view() == bvReviews {
			m.setFlash("sprints are for tickets: "+m.key("board.reviews")+" shows them", false)
			return m, nil
		}
		if len(m.boardFutures) == 0 {
			m.setFlash("no future sprint has your tickets", false)
			return m, nil
		}
		m.boardSprint = (m.boardSprint + 1) % (len(m.boardFutures) + 1)
		m.cur[vBoard], m.boardCur, m.boardFocus = cursor{}, [nStages]cursor{}, false
		m.refilter()
		m.setFlash("showing "+m.currentSprint(), false)
		return m, nil
	}
	c, ok := m.currentCard()
	if !ok {
		if !m.boardFocus && m.count() > 0 {
			m.setFlash("↓ steps into the board to pick a card", false)
		}
		return m, nil
	}
	switch act {
	case "board.run", "board.run_bg":
		return m.runNext(c)
	case "board.plan_ok":
		m.planOK(c)
		return m, nil
	case "board.override":
		m.overrideChooser(c)
		return m, nil
	case "board.sessions":
		if _, hold := nextStep(c); hold != nil {
			if r, ok := m.sessionRow(hold.Session); ok && !r.live() && r.Dir() != "" {
				return m.runNext(c) // its window was closed while it waited: resume it
			}
		}
	}
	if c.review() {
		return m.keyReviewCard(act, c)
	}
	key := c.issue.Key
	switch act {
	case "board.sessions":
		if r, ok := m.askingSession(key); ok {
			return m.jumpTo(r)
		}
		if ids := m.ix.TicketSess[key]; len(ids) > 0 {
			m.chooseSessions(ids, "sessions for "+key)
			return m, nil
		}
		return m.claudeForTicket(key, c.issue.Summary)
	case "board.claude", "board.claude_bg":
		return m.claudeForTicket(key, c.issue.Summary)
	case "board.copy":
		return m, copyText(key, key)
	case "board.mr":
		if mr, ok := cardMR(c, false); ok {
			return m, openURL(mr.WebURL)
		}
		m.setFlash("no MR for "+key+" yet", true)
		return m, nil
	case "board.threads":
		if mr, ok := cardMR(c, true); ok {
			m.chooseThreads(mr)
			return m, nil
		}
		m.setFlash("no open MR of yours for "+key, true)
		return m, nil
	case "board.open", "board.move":
		if m.atl == nil {
			m.setFlash("Jira isn't set up: "+fmt.Sprint(m.atlErr), true)
			return m, nil
		}
		if act == "board.move" {
			return m, m.loadTransitions(key)
		}
		return m, openURL(m.atl.IssueURL(key))
	}
	return m, nil
}

// cardMR is the MR the card's keys act on: an open one first, and for threads
// one of mine, since only those carry their threads.
func cardMR(c boardCard, threads bool) (gitlab.MR, bool) {
	ok := func(mr gitlab.MR) bool { return openMR(mr) && (!threads || len(mr.Threads) > 0) }
	if i := slices.IndexFunc(c.mrs, ok); i >= 0 {
		return c.mrs[i], true
	}
	if !threads && len(c.mrs) > 0 {
		return c.mrs[0], true
	}
	return gitlab.MR{}, false
}

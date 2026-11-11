package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/MartyJRE/claudia/internal/autopilot"
	"github.com/MartyJRE/claudia/internal/board"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/runs"
)

type rstage = board.RStage

const (
	rsRequested = board.Requested
	rsReviewing = board.Reviewing
	rsFindings  = board.Findings
	rsWaiting   = board.Waiting
	rsDone      = board.Reviewed
	nRStages    = board.NRStages
)

var rstageNames = board.RStageNames

// reviewTint sets review cards apart from ticket cards, and stays distinct
// from the cursor's visualGrey.
var reviewTint = lipgloss.Color("#342e42")

func (m *Model) reviewCards() []boardCard {
	byID := m.rowsByID()
	var cards []boardCard
	for _, bc := range board.ReviewCards(m.gl, m.ix, sessionsOf(m.sessions), m.runsFor) {
		c := m.fromBoard(bc, byID)
		m.applyAutopilot(&c)
		next, hold := nextStep(c)
		if t, n, verb, ok := autoTurn(c, next); ok && hold == nil && c.gate != autopilot.Off {
			c.turn, c.next, c.verb = t, n, verb
		}
		_, c.hinted = m.stripHint(c, next, hold)
		cards = append(cards, c)
	}
	return cards
}

func reviewStage(mr gitlab.MR, sessions []row, rs []runs.Run, me string) (rstage, turn, string, string, string) {
	return board.ReviewStage(mr, sessionsOf(sessions), rs, me)
}

func (c boardCard) review() bool { return c.mr != nil }

// id keeps the cursor on its card across refreshes.
func (c boardCard) id() string {
	if c.review() {
		return c.mr.WebURL
	}
	return c.issue.Key
}

// boardViews names the three views r cycles through, the current one in
// brackets, with the reviews that need you. The others get spaces where the
// brackets would be, so the labels stay put as r moves the brackets.
func (m *Model) boardViews() string {
	cur := m.boardPrefs.view()
	var parts []string
	for v, name := range boardViewNames {
		if boardView(v) == bvReviews && m.boardRevNeeds > 0 {
			name += fmt.Sprintf(" %d", m.boardRevNeeds)
		}
		if boardView(v) == cur {
			parts = append(parts, sAccent.Render("["+name+"]"))
			continue
		}
		parts = append(parts, sDim.Render(" "+name+" "))
	}
	return strings.Join(parts, sDim.Render("·"))
}

func reviewChips(c boardCard) string {
	mr := c.mr
	chips := []string{sDim.Render(mr.Author.Username)}
	if a := age(mr.CreatedAt); a != "" {
		chips = append(chips, sDim.Render(a))
	}
	if p := pipelineChip(*mr); p != "" {
		chips = append(chips, p)
	}
	for _, ch := range []string{ctxChip(c.ctx), overrideChip(c.over)} {
		if ch != "" {
			chips = append(chips, ch)
		}
	}
	if c.next != "" {
		label := "→ "
		if c.turn != turnYou {
			label = ""
		}
		chips = append(chips, sDim.Render(label+c.next))
	}
	return strings.Join(chips, sDim.Render(" · "))
}

// pipelineChip is mrChip without the ref, for cards whose first line has it.
func pipelineChip(mr gitlab.MR) string {
	switch st := mr.PipelineStatus(); st {
	case "success":
		return sOK.Render("✓ passed")
	case "failed":
		return sErr.Render("✗ failed")
	case "":
		return ""
	default:
		return sDim.Render("… " + st)
	}
}

// reviewBadge is the turn badge, shortened when the column is too narrow for
// the full one next to the ref.
func (m *Model) reviewBadge(c boardCard, room int) string {
	full := m.cardBadge(c)
	if full == "" || lipgloss.Width(full) <= room {
		return full
	}
	switch c.turn {
	case turnNeedsYou:
		return "  " + lipgloss.NewStyle().Foreground(yellow).Bold(true).Render("◆ check")
	case turnAgent:
		return "  " + sOK.Render(m.glyph(activityFrames[c.verb])+" review…")
	default:
		return "  " + sDim.Render("⧗ author")
	}
}

// tint paints a background behind already-styled text, as highlight does.
func tint(s string, bg lipgloss.Color) string {
	open, _, _ := strings.Cut(lipgloss.NewStyle().Background(bg).Render(" "), " ")
	if open == "" {
		return s
	}
	s = strings.ReplaceAll(s, "\x1b[0m", "\x1b[0m"+open)
	s = strings.ReplaceAll(s, "\x1b[m", "\x1b[m"+open)
	return open + s + "\x1b[0m"
}

func (m *Model) reviewCardLines(c boardCard, w int, sel bool) []string {
	inner := w - 2
	head := sKey.Render(fmt.Sprintf("!%d", c.mr.IID)) + " " + sBranch.Render(c.mr.Repo())
	// The badge says where the review stands, so it keeps its room and the
	// repo name gives way.
	badge := m.reviewBadge(c, inner-lipgloss.Width(head))
	head = ansi.Truncate(head, max(0, inner-lipgloss.Width(badge)), "…")
	lines := []string{
		fit(head+badge, inner),
		fit(sText.Render(c.mr.Title), inner),
		fit(reviewChips(c), inner),
		fit(m.stepStrip(c), inner),
	}
	for i, l := range lines {
		bar := "  "
		if sel {
			bar, l = sBar.Render("▌ "), highlight(l)
		}
		lines[i] = tint(bar, reviewTint)
		if !sel {
			l = tint(l, reviewTint)
		}
		lines[i] += l
	}
	return lines
}

func (m *Model) detailReview(c boardCard) string {
	mr := *c.mr
	lines := []string{sKey.Render(fmt.Sprintf("!%d", mr.IID)) + " " + sBold.Render(mr.Title)}
	meta := []string{sAccent.Render(rstageNames[c.rstage]), sBranch.Render(mr.Project()), sDim.Render("by " + mr.Author.Username)}
	if p := pipelineChip(mr); p != "" {
		meta = append(meta, p)
	}
	if !mr.CreatedAt.IsZero() {
		meta = append(meta, sDim.Render("opened "+age(mr.CreatedAt)+" ago"))
	}
	switch {
	case len(mr.ApprovedBy) > 0:
		meta = append(meta, sOK.Render("approved by "+strings.Join(mr.ApprovedBy, ", ")))
	case mr.ApprovalsLeft > 0:
		meta = append(meta, sDim.Render(fmt.Sprintf("%d approval(s) left", mr.ApprovalsLeft)))
	}
	if t := mrTicket(mr); t != "" {
		meta = append(meta, sTicket.Render(t))
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
	for _, r := range c.sessions {
		l := m.status(r) + " " + sText.Render(r.Name())
		if r.summary != "" {
			l += sDim.Render("  " + oneLine(r.summary))
		}
		lines = append(lines, l)
	}
	return m.panel(lines)
}

func (m *Model) keyReviewCard(act string, c boardCard) (tea.Model, tea.Cmd) {
	mr := *c.mr
	ref := fmt.Sprintf("!%d", mr.IID)
	switch act {
	case "board.sessions", "board.claude":
		ids := m.ix.MRSessions[mr.WebURL]
		if r, ok := m.askingAmong(ids); ok {
			return m.jumpTo(r)
		}
		if act == "board.sessions" && len(ids) > 0 {
			m.chooseSessions(ids, "sessions for "+ref)
			return m, nil
		}
		return m.reviewWithClaude(mr)
	case "board.claude_bg":
		return m.reviewWithClaude(mr)
	case "board.open", "board.mr":
		return m, openURL(mr.WebURL)
	case "board.threads":
		m.chooseThreads(mr)
		return m, nil
	case "board.copy":
		return m, copyText(mr.WebURL, ref+" link")
	case "board.move":
		m.setFlash(ref+" is a review: only tickets move", true)
	}
	return m, nil
}

func (m *Model) reviewHints(c boardCard) [][2]string {
	return [][2]string{
		{m.key("board.left") + "/" + m.key("board.right"), "columns"},
		m.hint("board.run", runHint(c)),
		m.hint("board.run_bg", "in the background"),
		m.hint("board.override", "autopilot"),
		{m.key("board.sessions"), "sessions"},
		{m.key("board.claude"), "review with claude"},
		{m.key("board.claude_bg"), "in the background"},
		{m.key("board.open"), "open MR"},
		{m.key("board.threads"), "threads"},
		{m.key("board.copy"), "copy link"},
	}
}

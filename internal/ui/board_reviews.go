package ui

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/runs"
)

// A review card is an MR waiting on my review; like a ticket's, its stage is
// read off GitLab and the review sessions on every refresh.
type rstage int

const (
	rsRequested rstage = iota
	rsReviewing
	rsFindings
	rsWaiting
	rsDone
	nRStages
)

var rstageNames = [nRStages]string{"Requested", "Reviewing", "Check findings", "Waiting on author", "Done"}

// allStage is the ticket column a review card sits in on the All view.
var allStage = [nRStages]stage{stUpNext, stBuilding, stReview, stMerge, stDone}

// reviewTint sets review cards apart from ticket cards, and stays distinct
// from the cursor's visualGrey.
var reviewTint = lipgloss.Color("#342e42")

func (m *Model) reviewCards() []boardCard {
	byID := map[string]row{}
	for _, r := range m.sessions {
		byID[r.ID] = r
	}
	var cards []boardCard
	for _, mr := range m.gl.Review {
		if mr.Draft {
			continue
		}
		c := boardCard{mr: &mr, runs: m.runsFor(mr.WebURL)}
		for _, id := range m.ix.mrSessions[mr.WebURL] {
			if r, ok := byID[id]; ok {
				c.sessions = append(c.sessions, r)
			}
		}
		c.rstage, c.turn, c.next, c.verb, c.why = reviewStage(mr, c.sessions, c.runs, m.gl.Me.Username)
		c.stage = allStage[c.rstage]
		cards = append(cards, c)
	}
	return cards
}

func reviewStage(mr gitlab.MR, sessions []row, rs []runs.Run, me string) (rstage, turn, string, string, string) {
	var reviews []row
	for _, r := range sessions {
		if r.kind == "review" {
			reviews = append(reviews, r)
		}
	}
	if mr.ApprovedByMe(me) {
		return rsDone, turnYou, "", "", "you approved it"
	}
	for _, r := range reviews {
		if r.live() && r.Live.Status.Working() {
			verb, detail := activity(r)
			return rsReviewing, turnAgent, oneLine(detail), verb, fmt.Sprintf("%q is reviewing it", r.Name())
		}
	}
	if mr.ReviewedByMe() {
		return rsWaiting, turnColleague, "waits on " + mr.Author.Username, "", "you reviewed it without approving"
	}
	if len(reviews) == 0 {
		return rsRequested, turnYou, "review it", "", "you're a reviewer and haven't reviewed it yet"
	}
	last := slices.MaxFunc(reviews, func(a, b row) int { return a.LastActive().Compare(b.LastActive()) })
	// A review run knows the head it read: only a push makes its findings stale.
	if i := slices.IndexFunc(rs, func(r runs.Run) bool { return r.Session == last.ID }); i >= 0 {
		if rs[i].SHA != mr.SHA {
			return rsRequested, turnYou, "review it again", "", fmt.Sprintf("new commits since %q reviewed it", last.Name())
		}
		return rsFindings, turnNeedsYou, findingHint(last), "", fmt.Sprintf("%q finished its review", last.Name())
	}
	// GitLab clears my review when the author asks for another, and a push or
	// reply after the review means its findings may no longer hold.
	if mr.UpdatedAt.After(last.LastActive()) {
		return rsRequested, turnYou, "review it again", "", fmt.Sprintf("the MR changed after %q reviewed it", last.Name())
	}
	return rsFindings, turnNeedsYou, findingHint(last), "", fmt.Sprintf("%q finished its review", last.Name())
}

// findingHint is the first line of what the review session left, without the
// watcher's prefix.
func findingHint(r row) string {
	s := r.summary
	for _, p := range []string{needsYouPrefix, donePrefix, waitingPrefix} {
		s = strings.TrimPrefix(s, p)
	}
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	if s == "" {
		return "read the findings"
	}
	return s
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
	case "board.sessions":
		if ids := m.ix.mrSessions[mr.WebURL]; len(ids) > 0 {
			m.chooseSessions(ids, "sessions for "+ref)
			return m, nil
		}
		return m.reviewWithClaude(mr)
	case "board.claude", "board.claude_bg":
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
		{m.key("board.sessions"), "sessions"},
		{m.key("board.claude"), "review with claude"},
		{m.key("board.claude_bg"), "in the background"},
		{m.key("board.open"), "open MR"},
		{m.key("board.threads"), "threads"},
		{m.key("board.copy"), "copy link"},
	}
}

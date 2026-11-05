package ui

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/MartyJRE/claudia/internal/plan"
	"github.com/MartyJRE/claudia/internal/tmux"
)

// planReview is a session's plan with your comments on its lines; s sends
// them to the session's plan prompt as feedback.
type planReview struct {
	sess     row
	key      string // where the comments are saved: the plan file, else the session
	lines    []string
	comments []plan.Comment
	cur      int
	anchor   int // -1, or where V started a selection
	offset   int // first visual row shown
	editing  int // -1 when not writing; else the comment being edited, len(comments) for a new one
	input    textarea.Model
}

var sendPlanFeedback = tmux.SendPlanFeedback

// openPlan shows the session's plan, with any comments left on it before.
func (m *Model) openPlan(r row) {
	if r.Transcript == nil {
		m.setFlash("no plan in this session", true)
		return
	}
	lines := plan.Lines(plan.Text(r.PlanPath, r.Plan))
	if len(lines) == 0 {
		m.setFlash("no plan in this session", true)
		return
	}
	k := r.PlanPath
	if k == "" {
		k = "session:" + r.ID
	}
	in := textarea.New()
	in.ShowLineNumbers = false
	in.Prompt = ""
	in.Placeholder = "your comment"
	in.SetHeight(3)
	in.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "ctrl+j"))
	m.plan = &planReview{sess: r, key: k, lines: lines, comments: plan.Load(k, lines), anchor: -1, editing: -1, input: in}
}

func (p *planReview) save() { _ = plan.Save(p.key, p.comments) }

// selection is the lines the next comment covers.
func (p *planReview) selection() (int, int) {
	if p.anchor < 0 {
		return p.cur, p.cur
	}
	return min(p.anchor, p.cur), max(p.anchor, p.cur)
}

// at is the comment the cursor is on: one whose lines include it.
func (p *planReview) at() int {
	for i := len(p.comments) - 1; i >= 0; i-- {
		if c := p.comments[i]; p.cur >= c.From && p.cur <= c.To {
			return i
		}
	}
	return -1
}

func (m *Model) closePlan() (tea.Model, tea.Cmd) {
	m.plan.save()
	m.plan = nil
	if m.opts.PlanOnly {
		return m, tea.Quit
	}
	return m, nil
}

func (m *Model) updatePlan(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.plan
	if p.editing >= 0 {
		return m.updatePlanInput(msg)
	}
	// Keys pressed in quick succession can arrive as one event; take them one by one.
	if msg.Type == tea.KeyRunes && len(msg.Runes) > 1 && !msg.Paste {
		var cmds []tea.Cmd
		for i, r := range msg.Runes {
			if m.plan == nil {
				break
			}
			if m.plan.editing >= 0 {
				_, cmd := m.updatePlanInput(tea.KeyMsg{Type: tea.KeyRunes, Runes: msg.Runes[i:]})
				cmds = append(cmds, cmd)
				break
			}
			_, cmd := m.updatePlan(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	}
	page := max(1, m.planHeight()/2)
	switch msg.String() {
	case "esc":
		if p.anchor >= 0 {
			p.anchor = -1
			return m, nil
		}
		return m.closePlan()
	case "q":
		return m.closePlan()
	case "j", "down":
		p.cur = min(p.cur+1, len(p.lines)-1)
	case "k", "up":
		p.cur = max(p.cur-1, 0)
	case "ctrl+d", "pgdown":
		p.cur = min(p.cur+page, len(p.lines)-1)
	case "ctrl+u", "pgup":
		p.cur = max(p.cur-page, 0)
	case "g", "home":
		p.cur = 0
	case "G", "end":
		p.cur = len(p.lines) - 1
	case "V":
		if p.anchor >= 0 {
			p.anchor = -1
		} else {
			p.anchor = p.cur
		}
	case "c", "enter":
		p.editing = len(p.comments)
		p.input.Reset()
		return m, p.input.Focus()
	case "e":
		if i := p.at(); i >= 0 {
			p.editing = i
			p.input.SetValue(p.comments[i].Text)
			return m, p.input.Focus()
		}
		m.setFlash("no comment on this line", false)
	case "d":
		if i := p.at(); i >= 0 {
			p.comments = slices.Delete(p.comments, i, i+1)
			p.save()
		}
	case "s":
		return m.sendPlan()
	}
	return m, nil
}

func (m *Model) updatePlanInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.plan
	switch msg.String() {
	case "esc":
		p.editing = -1
		p.input.Blur()
		return m, nil
	case "enter":
		text := strings.TrimSpace(p.input.Value())
		switch {
		case p.editing < len(p.comments) && text == "":
			p.comments = slices.Delete(p.comments, p.editing, p.editing+1)
		case p.editing < len(p.comments):
			p.comments[p.editing].Text = text
		case text != "":
			from, to := p.selection()
			p.comments = append(p.comments, plan.New(p.lines, from, to, text))
		}
		p.editing, p.anchor = -1, -1
		p.input.Blur()
		p.save()
		return m, nil
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	return m, cmd
}

type planSentMsg struct{ err error }

// sendPlan answers the session's plan prompt with the comments; when that
// can't be done in its pane, they're copied for pasting instead.
func (m *Model) sendPlan() (tea.Model, tea.Cmd) {
	p := m.plan
	if len(p.comments) == 0 {
		m.setFlash("no comments yet: c to write one", false)
		return m, nil
	}
	feedback := plan.Feedback(p.comments)
	if p.sess.pane == nil {
		m.setFlash("the session isn't in tmux, so the feedback was copied: paste it into the plan prompt", false)
		return m, copyText(feedback, "plan feedback")
	}
	pane := p.sess.pane.ID
	return m, func() tea.Msg { return planSentMsg{sendPlanFeedback(pane, feedback)} }
}

func (m *Model) planSent(msg planSentMsg) (tea.Model, tea.Cmd) {
	p := m.plan
	if p == nil {
		return m, nil
	}
	if msg.err != nil {
		why := msg.err.Error()
		if errors.Is(msg.err, tmux.ErrNoPlanPrompt) {
			why = "the plan prompt isn't open"
		}
		m.setFlash(why+": feedback copied, paste it into the plan prompt", true)
		return m, copyText(plan.Feedback(p.comments), "plan feedback")
	}
	p.comments = nil
	m.setFlash("feedback sent to "+p.sess.Name(), false)
	return m.closePlan()
}

func (m *Model) planHeight() int { return max(3, m.height-4) }

var (
	planRail   = lipgloss.NewStyle().Foreground(purple)
	planYou    = lipgloss.NewStyle().Foreground(purple).Bold(true)
	planSel    = lipgloss.NewStyle().Background(visualGrey)
	planHead1  = lipgloss.NewStyle().Foreground(blue).Bold(true)
	planHead   = lipgloss.NewStyle().Foreground(white).Bold(true)
	planCode   = lipgloss.NewStyle().Foreground(yellow)
	inlineCode = regexp.MustCompile("`[^`]+`")
)

// planLine styles one wrapped piece of a plan line; fenced code stays dim.
func planLine(s string, fenced bool) string {
	t := strings.TrimSpace(s)
	switch {
	case fenced || strings.HasPrefix(t, "```"):
		return sDim.Render(s)
	case strings.HasPrefix(t, "# "):
		return planHead1.Render(s)
	case strings.HasPrefix(t, "#"):
		return planHead.Render(s)
	}
	var b strings.Builder
	last := 0
	for _, loc := range inlineCode.FindAllStringIndex(s, -1) {
		b.WriteString(sText.Render(s[last:loc[0]]))
		b.WriteString(planCode.Render(s[loc[0]+1 : loc[1]-1]))
		last = loc[1]
	}
	b.WriteString(sText.Render(s[last:]))
	return b.String()
}

// planRows lays the plan out: each line wrapped to the width, the comments
// under the last line they cover, and the input under the selection while
// writing. line says which plan line each row belongs to.
func (m *Model) planRows(w int) (rows []string, line []int) {
	p := m.plan
	from, to := p.selection()
	textW := max(10, w-4)
	fenced := false
	add := func(s string, i int) { rows, line = append(rows, s), append(line, i) }
	comment := func(c plan.Comment, i int) {
		for j, part := range strings.Split(ansi.Wordwrap(c.Text, textW-8, ""), "\n") {
			who := "     "
			if j == 0 {
				who = planYou.Render("you") + "  "
			}
			add("   "+planRail.Render("┃")+" "+who+sText.Render(part), i)
		}
	}
	for i, l := range p.lines {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			fenced = !fenced
		}
		inSel := i >= from && i <= to && (p.anchor >= 0 || p.editing == len(p.comments))
		for _, part := range strings.Split(ansi.Wordwrap(l, textW, ""), "\n") {
			bar := "  "
			if i == p.cur || inSel {
				bar = planRail.Bold(true).Render("▌") + " "
			}
			text := planLine(part, fenced && !strings.HasPrefix(strings.TrimSpace(l), "```"))
			if inSel {
				text = planSel.Render(ansi.Strip(part) + strings.Repeat(" ", max(0, textW-ansi.StringWidth(part))))
			}
			add(" "+bar+text, i)
		}
		for ci, c := range p.comments {
			if c.To != i {
				continue
			}
			if ci == p.editing {
				for _, r := range strings.Split(m.planInputBox(textW), "\n") {
					add("   "+r, i)
				}
				continue
			}
			comment(c, i)
		}
		if p.editing == len(p.comments) && i == to {
			for _, r := range strings.Split(m.planInputBox(textW), "\n") {
				add("   "+r, i)
			}
		}
	}
	return rows, line
}

func (m *Model) planInputBox(w int) string {
	p := m.plan
	from, to := p.selection()
	title := "comment"
	if p.editing < len(p.comments) {
		title = "edit comment"
	} else if to > from {
		title = fmt.Sprintf("comment on %d lines", to-from+1)
	}
	p.input.SetWidth(max(10, w-6))
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(purple).Padding(0, 1).Width(max(12, w-4))
	hint := sDim.Render("enter save · alt+enter new line · esc cancel")
	return box.Render(planYou.Render(title) + "\n" + p.input.View() + "\n" + hint)
}

func (m *Model) viewPlan() string {
	p := m.plan
	w, h := m.width, m.planHeight()
	head := planYou.Render("plan") + sDim.Render(" · ") + sBold.Render(p.sess.Name())
	if t := m.ix.sessionTicket[p.sess.ID]; t != "" {
		head += sDim.Render(" · ") + sTicket.Render(t)
	}
	n := sDim.Render(fmt.Sprintf("%d comment(s)", len(p.comments)))
	head = " " + head + strings.Repeat(" ", max(1, w-lipgloss.Width(head)-lipgloss.Width(n)-2)) + n

	rows, line := m.planRows(w)
	first := slices.Index(line, p.cur)
	last := first
	for last+1 < len(line) && line[last+1] == p.cur {
		last++
	}
	if p.editing >= 0 {
		for last+1 < len(line) && line[last+1] <= max(p.cur, p.anchor) {
			last++
		}
	}
	if first < p.offset {
		p.offset = first
	}
	if last >= p.offset+h {
		p.offset = last - h + 1
	}
	p.offset = max(0, min(p.offset, len(rows)-h))
	body := rows[p.offset:min(len(rows), p.offset+h)]
	for len(body) < h {
		body = append(body, "")
	}
	for i := range body {
		body[i] = ansi.Truncate(body[i], w, "…")
	}

	keys := func(k, label string) string { return sKey.Render(k) + sDim.Render(" "+label) }
	foot := strings.Join([]string{keys("j/k", "move"), keys("V", "select"), keys("c", "comment"), keys("e", "edit"), keys("d", "delete"), keys("s", "send"), keys("esc", "close")}, "  ")
	if m.flash != "" {
		st := sOK
		if m.flashErr {
			st = sErr
		}
		foot = st.Render(m.flash)
	}
	return head + "\n" + sRule.Render(strings.Repeat("─", w)) + "\n" + strings.Join(body, "\n") + "\n" + " " + foot
}

// ErrNoSession: no Claude session runs in the pane `claudia plan` was given.
var ErrNoSession = errors.New("no Claude session in this pane")

// PlanFor builds a model showing only the plan of the session in pane, for
// `claudia plan` in a tmux popup over it.
func PlanFor(pane string) (*Model, error) {
	m := Headless()
	m.opts.PlanOnly = true
	i := slices.IndexFunc(m.sessions, func(r row) bool { return r.pane != nil && r.pane.ID == pane })
	if i < 0 {
		return nil, ErrNoSession
	}
	m.openPlan(m.sessions[i])
	if m.plan == nil {
		return nil, errors.New(m.flash)
	}
	return m, nil
}

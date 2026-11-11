package ui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MartyJRE/claudia/internal/launch"
	"github.com/MartyJRE/claudia/internal/runs"
	"github.com/MartyJRE/claudia/internal/steps"
)

// Todos are asks that never reached Jira, often made in person. T captures
// one; it sits in Up next with a tint of its own, s turns it into a ticket
// through a Claude session, and x ticks it off when it needs no ticket.

var todoTint = lipgloss.Color("#2b3a35")

func (c boardCard) isTodo() bool { return c.todo != nil }

// todoCards are the todos still open, and ticketed ones whose ticket the
// board doesn't show yet.
func (m *Model) todoCards(tickets []boardCard) []boardCard {
	if m.db == nil {
		return nil
	}
	var out []boardCard
	for _, t := range m.db.Todos(clockNow()) {
		if t.Ticket != "" && slices.ContainsFunc(tickets, func(c boardCard) bool { return c.issue.Key == t.Ticket }) {
			continue
		}
		c := boardCard{todo: &t, stage: stUpNext, runs: m.runsFor(t.Subject())}
		c.turn, c.next, c.verb = todoTurn(c)
		out = append(out, c)
	}
	return out
}

func todoTurn(c boardCard) (turn, string, string) {
	_, hold := nextStep(c)
	switch {
	case hold != nil && hold.Outcome == runs.Running:
		return turnAgent, "drafting the ticket", "writing"
	case hold != nil && hold.Outcome == runs.NeedsYou:
		return turnNeedsYou, "answer Claude", ""
	case hold != nil:
		return turnColleague, cmp.Or(hold.Reason, "waiting"), ""
	case c.todo.Ticket != "":
		return turnYou, "ticketed as " + c.todo.Ticket, ""
	}
	return turnYou, "ticket", ""
}

func (m *Model) todoCardLines(c boardCard, w int, sel bool) []string {
	inner := w - 2
	t := c.todo
	var chips []string
	if t.Asked != "" {
		chips = append(chips, "from "+t.Asked)
	}
	chips = append(chips, age(t.Created))
	if t.Ticket != "" {
		chips = append(chips, t.Ticket)
	} else if t.Project != "" {
		chips = append(chips, "for "+t.Project)
	}
	lines := []string{
		fit(m.agreedChip(c)+lipgloss.NewStyle().Foreground(green).Render("✎ todo")+m.cardBadge(c), inner),
		fit(sText.Render(t.Title), inner),
		fit(sDim.Render(strings.Join(chips, " · ")), inner),
		fit(m.stepStrip(c), inner),
	}
	for i, l := range lines {
		bar := "  "
		if sel {
			bar, l = sBar.Render("▌ "), highlight(l)
		}
		lines[i] = tint(bar, todoTint)
		if !sel {
			l = tint(l, todoTint)
		}
		lines[i] += l
	}
	return lines
}

func (m *Model) detailTodo(c boardCard) string {
	t := c.todo
	lines := []string{lipgloss.NewStyle().Foreground(green).Render("✎ todo") + " " + sBold.Render(t.Title)}
	meta := []string{sDim.Render("noted " + age(t.Created) + " ago")}
	if t.Asked != "" {
		meta = append(meta, sDim.Render("asked by "+t.Asked))
	}
	if t.Ticket != "" {
		meta = append(meta, sTicket.Render(t.Ticket)+sDim.Render(", not on the board yet"))
	} else if t.Project != "" {
		meta = append(meta, sDim.Render("for "+t.Project))
	}
	if a := m.agreedMeta(c); a != "" {
		meta = append(meta, a)
	}
	lines = append(lines, strings.Join(meta, sDim.Render("  ·  ")))
	if t.Note != "" {
		lines = append(lines, sText.Render(t.Note))
	}
	lines = append(lines, m.turnBadge(c)+sDim.Render(" · "+c.next), m.runLine(c))
	return m.panel(lines)
}

func (m *Model) todoHints() [][2]string {
	return [][2]string{
		{m.key("board.left") + "/" + m.key("board.right"), "columns"},
		m.hint("board.run", "ticket it"),
		m.hint("board.todo_done", "tick off"),
		m.hint("board.pin", "agreed order"),
		m.hint("board.todo", "new todo"),
		{m.key("board.copy"), "copy"},
	}
}

func (m *Model) keyTodoCard(act string, c boardCard) (tea.Model, tea.Cmd) {
	t := c.todo
	switch act {
	case "board.pin":
		m.pinCard(c)
	case "board.up", "board.down":
		m.moveCard(c, map[string]int{"board.up": -1, "board.down": 1}[act])
	case "board.run", "board.run_bg", "board.claude", "board.claude_bg", "board.sessions":
		return m.runNext(c)
	case "board.todo_done":
		if err := m.db.DoneTodo(t.ID, clockNow()); err != nil {
			m.setFlash(err.Error(), true)
			return m, nil
		}
		m.setFlash("ticked off “"+t.Title+"”", false)
		m.refilter()
	case "board.copy":
		return m, copyText(t.Title, "the todo")
	case "board.open":
		if t.Ticket == "" || m.atl == nil {
			m.setFlash("no ticket for this todo yet: "+m.key("board.run")+" drafts one", false)
			return m, nil
		}
		return m, openURL(m.atl.IssueURL(t.Ticket))
	default:
		m.setFlash("that's for tickets: a todo can be ticketed, ticked off or pinned", false)
	}
	return m, nil
}

// askTodo is T: a todo's title, then who asked for it.
func (m *Model) askTodo() tea.Cmd {
	if m.db == nil {
		m.setFlash(fmt.Sprintf("the run log isn't open: %v", m.dbErr), true)
		return nil
	}
	in := newInput("todo › ", "what was asked")
	in.Width = max(40, m.width-12)
	m.prompt = &promptBox{title: "a todo that isn't in Jira", verb: "next", in: in, submit: func(m *Model, title string) (tea.Model, tea.Cmd) {
		who := newInput("asked by › ", "who asked, or enter to skip")
		who.Width = max(40, m.width-12)
		m.prompt = &promptBox{title: "who asked for “" + title + "”?", verb: "add", in: who, optional: true, submit: func(m *Model, asked string) (tea.Model, tea.Cmd) {
			return m, m.addTodo(runs.Todo{Title: title, Asked: asked})
		}}
		return m, m.prompt.in.Focus()
	}}
	return m.prompt.in.Focus()
}

func (m *Model) addTodo(t runs.Todo) tea.Cmd {
	t.Created = clockNow()
	if _, err := m.db.AddTodo(t); err != nil {
		m.setFlash(err.Error(), true)
		return nil
	}
	m.setFlash("noted “"+t.Title+"” in Up next; "+m.key("board.run")+" tickets it", false)
	m.refilter()
	return nil
}

// ticketTodo starts the session that drafts the todo's ticket, in the
// project the todo names, the only one configured, or one you pick.
func (m *Model) ticketTodo(c boardCard, run runs.Run) (tea.Model, tea.Cmd) {
	t := *c.todo
	projects := getConfig().TodoProjects()
	start := func(m *Model, project string) (tea.Model, tea.Cmd) {
		sp, id := launch.New(steps.Anywhere(getConfig()), launch.WorkProfile(m.store.Profiles), "ticket: "+t.Title, todoPrompt(t, project))
		sp.Kind = "ticket"
		sp, err := startRun(m.db, sp, id, &run)
		bg := m.background
		return m, func() tea.Msg { return startMsg{sp, bg, err} }
	}
	switch {
	case len(projects) == 0:
		m.setFlash("no Jira project to ticket it in: set todos.projects or jira_project", true)
		return m, nil
	case len(projects) == 1:
		return start(m, projects[0])
	}
	var items []choice
	for _, p := range projects {
		items = append(items, choice{label: p, search: p, value: p})
	}
	m.chooser = newChooser("ticket “"+t.Title+"” in", items, func(m *Model, ch choice) (tea.Model, tea.Cmd) {
		return start(m, ch.value.(string))
	})
	m.chooser.cur.pos = max(0, slices.Index(projects, t.Project))
	return m, nil
}

func todoPrompt(t runs.Todo, project string) string {
	ask := fmt.Sprintf("Someone asked me for this outside Jira: %q.", t.Title)
	if t.Asked != "" {
		ask = fmt.Sprintf("%s asked me for this outside Jira: %q.", t.Asked, t.Title)
	}
	if t.Note != "" {
		ask += " My note: " + t.Note
	}
	return ask + fmt.Sprintf(" Draft a Jira ticket for it in project %s: a summary and a description with the context and acceptance criteria; look at the code only where it helps. "+
		"Show me the draft and wait for my OK. Once I OK it, create the issue with the Atlassian MCP, assigned to me, "+
		"then run `%s ctl todo ticket %d <KEY>` so claudia puts the ticket in the todo's place.", project, launch.Self(), t.ID)
}

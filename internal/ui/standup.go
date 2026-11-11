package ui

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/launch"
	"github.com/MartyJRE/claudia/internal/runs"
	"github.com/MartyJRE/claudia/internal/standup"
	"github.com/MartyJRE/claudia/internal/teams"
)

// The Standup tab: Yesterday, Today and Blocked as talking points written
// once a day by a cheap headless run, with the facts they came from below.

type sfRun struct{ Card, Title, Step, Outcome, Reason string }

type sfMR struct{ Ref, Title, Ticket string }

type sfIssue struct{ Key, Summary, Status string }

type sfSession struct{ Name, Ticket string }

type sfBlocked struct {
	Ref, Title, WaitsOn string
	Since               time.Time `json:",omitzero"`
}

type sfMeeting struct{ Subject, Start, End string }

// StandupFacts is what the talking points are written from.
type StandupFacts struct {
	Since     time.Time
	Yesterday struct {
		Runs      []sfRun     `json:",omitempty"`
		Opened    []sfMR      `json:",omitempty"`
		Merged    []sfMR      `json:",omitempty"`
		Reviewed  []sfMR      `json:",omitempty"`
		TodosDone []string    `json:",omitempty"`
		Worked    []sfSession `json:",omitempty"`
	}
	Today struct {
		Agreed     []CtlAgreed `json:",omitempty"`
		InProgress []sfIssue   `json:",omitempty"`
		Todos      []string    `json:",omitempty"`
		Meetings   []sfMeeting `json:",omitempty"`
	}
	Blocked []sfBlocked `json:",omitempty"`
}

func (m *Model) StandupData(since time.Time) StandupFacts {
	var f StandupFacts
	f.Since = since
	now := clockNow()
	if m.db != nil {
		for _, r := range m.db.Since(since) {
			if r.Outcome != runs.Running {
				f.Yesterday.Runs = append(f.Yesterday.Runs, sfRun{m.subjectRef(r.Subject), m.subjectTitle(r.Subject), r.Step, r.Outcome, r.Reason})
			}
		}
		for _, t := range m.db.Todos(since) {
			if t.Done.IsZero() {
				f.Today.Todos = append(f.Today.Todos, t.Title)
			} else {
				f.Yesterday.TodosDone = append(f.Yesterday.TodosDone, t.Title)
			}
		}
	}
	mr := func(g gitlab.MR) sfMR { return sfMR{fmt.Sprintf("%s!%d", g.Repo(), g.IID), g.Title, mrTicket(g)} }
	for _, g := range m.gl.Mine {
		if g.CreatedAt.After(since) {
			f.Yesterday.Opened = append(f.Yesterday.Opened, mr(g))
		}
	}
	for _, g := range m.gl.Merged {
		if g.MergedAt.After(since) {
			f.Yesterday.Merged = append(f.Yesterday.Merged, mr(g))
		}
	}
	for _, g := range m.gl.Review {
		if g.ApprovedByMe(m.gl.Me.Username) && g.UpdatedAt.After(since) {
			f.Yesterday.Reviewed = append(f.Yesterday.Reviewed, mr(g))
		}
	}
	for _, r := range m.sessions {
		if r.LastActive().After(since) && r.Transcript != nil {
			f.Yesterday.Worked = append(f.Yesterday.Worked, sfSession{r.Name(), m.ix.SessionTicket[r.ID]})
		}
	}
	f.Today.Agreed = m.CtlPrio()
	for _, is := range m.jira.Sprint {
		if is.Category == "indeterminate" && is.Level < 1 {
			f.Today.InProgress = append(f.Today.InProgress, sfIssue{is.Key, is.Summary, is.Status})
		}
	}
	y, mo, d := now.Date()
	for _, e := range m.teams.Events {
		if s := e.Start.Local(); s.Year() == y && s.Month() == mo && s.Day() == d && e.Response != "declined" {
			f.Today.Meetings = append(f.Today.Meetings, sfMeeting{e.Subject, s.Format("15:04"), e.End.Local().Format("15:04")})
		}
	}
	for _, c := range slices.Concat(m.boardCards(), m.reviewCards()) {
		if c.turn != turnColleague || c.stage == stDone {
			continue
		}
		b := sfBlocked{Ref: c.ref(), WaitsOn: c.next}
		switch {
		case c.review():
			b.Title, b.Since = c.mr.Title, c.mr.UpdatedAt
		default:
			b.Title, b.Since = c.issue.Summary, c.issue.Updated
		}
		if _, hold := nextStep(c); hold != nil && !hold.Finished.IsZero() {
			b.Since = hold.Finished
		}
		f.Blocked = append(f.Blocked, b)
	}
	return f
}

func (m *Model) subjectRef(subject string) string {
	if g, ok := m.mrByURL(subject); ok {
		return fmt.Sprintf("%s!%d", g.Repo(), g.IID)
	}
	return subject
}

func (m *Model) subjectTitle(subject string) string {
	if is, ok := m.ix.Issues[subject]; ok {
		return is.Summary
	}
	if g, ok := m.mrByURL(subject); ok {
		return g.Title
	}
	if id, ok := runs.TodoID(subject); ok && m.db != nil {
		if t, ok := m.db.Todo(id); ok {
			return t.Title
		}
	}
	return ""
}

type standupSinceMsg struct{ since time.Time }

type standupMsg struct{ p standup.Points }

var calendarPast = func(c *teams.Client, from, to time.Time) ([]teams.Event, error) { return c.Calendar(from, to) }

var writeStandup = standup.Write

// startStandup finds when the last standup was, then has the points written.
func (m *Model) startStandup() tea.Cmd {
	if m.standupBusy {
		return nil
	}
	m.standupBusy = true
	m.vis.standup = m.standupLines()
	tm, now, cfg := m.tm, clockNow(), getConfig().Standup
	return func() tea.Msg {
		var evs []teams.Event
		if tm != nil {
			evs, _ = calendarPast(tm, now.Add(-standup.Lookback), now)
		}
		return standupSinceMsg{standup.Since(evs, now, cfg)}
	}
}

func (m *Model) standupSince(msg standupSinceMsg) tea.Cmd {
	facts := m.StandupData(msg.since)
	dir, lang := launch.WorkProfile(m.store.Profiles).Dir, getConfig().Standup.Lang()
	return func() tea.Msg {
		p := standup.Points{At: clockNow(), Since: msg.since}
		text, err := writeStandup(facts, lang, dir)
		p.Text = text
		if err != nil {
			p.Err = err.Error()
		}
		return standupMsg{p}
	}
}

func (m *Model) standupWritten(msg standupMsg) {
	m.standupBusy = false
	if msg.p.Err != "" && m.standup.Text != "" {
		m.standup.Err = msg.p.Err // keep the last good points
	} else {
		m.standup = msg.p
	}
	_ = standup.Save(m.standup)
	m.refilter()
}

// standupDue: the points weren't written today yet.
func (m *Model) standupDue() bool {
	y1, m1, d1 := m.standup.At.Date()
	y2, m2, d2 := clockNow().Date()
	return !m.standupBusy && (m.standup.At.IsZero() || y1 != y2 || m1 != m2 || d1 != d2)
}

func (m *Model) keyStandup(k string) (tea.Model, tea.Cmd) {
	switch m.action(k) {
	case "standup.refresh":
		return m, m.startStandup()
	case "standup.copy":
		if m.standup.Text == "" {
			m.setFlash("no talking points yet", false)
			return m, nil
		}
		return m, copyText(m.standup.Text, "the talking points")
	}
	return m, nil
}

var sectionHead = lipgloss.NewStyle().Foreground(purple).Bold(true)

// standupLines is the tab's body: the talking points, then the facts.
func (m *Model) standupLines() []string {
	var out []string
	switch {
	case m.standup.Text != "":
		for _, l := range strings.Split(m.standup.Text, "\n") {
			switch t := strings.TrimSpace(l); {
			case t == "":
				out = append(out, "")
			case strings.HasPrefix(t, "- "), strings.HasPrefix(t, "• "):
				out = append(out, "   "+sText.Render(l))
			default:
				out = append(out, " "+sectionHead.Render(t))
			}
		}
	case m.standupBusy:
		out = append(out, " "+sDim.Render(m.spin()+" writing the talking points…"))
	default:
		out = append(out, " "+sDim.Render("no talking points yet: "+m.key("standup.refresh")+" writes them"))
	}
	if m.standup.Err != "" {
		out = append(out, "", " "+sErr.Render("last write failed: "+oneLine(m.standup.Err)))
	}
	if m.standup.Since.IsZero() {
		return out
	}
	f := m.StandupData(m.standup.Since)
	fact := func(s string) string { return "   " + sDim.Render(s) }
	out = append(out, "", " "+sDim.Render("─── what it was written from"), "", " "+sBold.Render("Yesterday"))
	for _, r := range f.Yesterday.Runs {
		out = append(out, fact(fmt.Sprintf("%s %s: %s %s", r.Card, r.Step, r.Outcome, r.Reason)))
	}
	for _, list := range []struct {
		verb string
		mrs  []sfMR
	}{{"opened", f.Yesterday.Opened}, {"merged", f.Yesterday.Merged}, {"approved", f.Yesterday.Reviewed}} {
		for _, g := range list.mrs {
			out = append(out, fact(list.verb+" "+g.Ref+" "+g.Title))
		}
	}
	for _, t := range f.Yesterday.TodosDone {
		out = append(out, fact("ticked off "+t))
	}
	for _, s := range f.Yesterday.Worked {
		out = append(out, fact(strings.TrimSpace("worked in "+s.Name+" "+s.Ticket)))
	}
	out = append(out, "", " "+sBold.Render("Today"))
	for _, a := range f.Today.Agreed {
		out = append(out, fact(fmt.Sprintf("%s %s %s", circled(a.Pos), a.Ref, a.Title)))
	}
	for _, is := range f.Today.InProgress {
		out = append(out, fact(is.Key+" "+is.Summary+" · "+is.Status))
	}
	for _, t := range f.Today.Todos {
		out = append(out, fact("todo: "+t))
	}
	for _, e := range f.Today.Meetings {
		out = append(out, fact(e.Start+"–"+e.End+" "+e.Subject))
	}
	out = append(out, "", " "+sBold.Render("Blocked"))
	for _, b := range f.Blocked {
		since := ""
		if !b.Since.IsZero() {
			since = ", " + age(b.Since)
		}
		out = append(out, fact(b.Ref+" "+b.Title+": "+b.WaitsOn+since))
	}
	return out
}

func (m *Model) standupSubtitle() string {
	s := sDim.Render("talking points for standup")
	if !m.standup.Since.IsZero() {
		s = sDim.Render("since " + m.standup.Since.Format("Mon 15:04") + " · written " + m.standup.At.Format("15:04") + "  ")
	}
	return s + sKey.Render(m.key("standup.refresh")) + sDim.Render(" rewrite  ") + sKey.Render(m.key("standup.copy")) + sDim.Render(" copy")
}

// listStandup scrolls the body; the cursor is the top line shown.
func (m *Model) listStandup(h int) string {
	lines := m.vis.standup
	top := min(m.cur[vStandup].pos, max(0, len(lines)-h))
	shown := lines[top:min(len(lines), top+h)]
	for len(shown) < h {
		shown = append(shown, "")
	}
	return strings.Join(shown, "\n")
}

// WriteStandup writes fresh talking points now, as opening the tab does, for
// `claudia standup`.
func (m *Model) WriteStandup() (standup.Points, error) {
	now := clockNow()
	var evs []teams.Event
	if m.tm != nil {
		evs, _ = calendarPast(m.tm, now.Add(-standup.Lookback), now)
	}
	msg := m.standupSince(standupSinceMsg{standup.Since(evs, now, getConfig().Standup)})().(standupMsg)
	m.standupWritten(msg)
	if msg.p.Err != "" {
		return msg.p, errors.New(msg.p.Err)
	}
	return msg.p, nil
}

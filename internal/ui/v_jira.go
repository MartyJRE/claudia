package ui

import (
	"fmt"
	"os"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/launch"
	"github.com/MartyJRE/claudia/internal/state"
)

var jiraCols = []col{
	{"KEY", 10, 0},
	{"SUMMARY", 0, 0},
	{"TYPE", 9, 110},
	{"STATUS", 22, 0},
	{"ASSIGNEE", 16, 0},
	{"MR", 12, 100},
	{"CLAUDE", 8, 0},
}

func statusText(is atlassian.Issue) string {
	c := commentGrey
	switch is.Category {
	case "indeterminate":
		c = blue
	case "done":
		c = green
	case "new":
		c = white
	}
	return lipgloss.NewStyle().Foreground(c).Render(is.Status)
}

func (m *Model) filterIssues(match func(...string) bool) []atlassian.Issue {
	src := m.jira.Sprint
	if m.teamJira {
		src = m.jira.Team
	}
	var out []atlassian.Issue
	for _, is := range src {
		if !m.showDone && is.Category == "done" {
			continue
		}
		if slices.Contains(m.jiraPrefs.Hidden, is.Status) {
			continue
		}
		if match(is.Key, is.Summary, is.Status, is.Assignee, typeName(is.Type)) {
			out = append(out, is)
		}
	}
	order := map[string]int{"indeterminate": 0, "new": 1, "done": 2}
	switch m.jiraPrefs.Sort {
	case sortRank:
		slices.SortStableFunc(out, func(a, b atlassian.Issue) int { return order[a.Category] - order[b.Category] })
	case sortStatus:
		// Workflow order is unknown, so group by category, then by status name.
		slices.SortStableFunc(out, func(a, b atlassian.Issue) int {
			if d := order[a.Category] - order[b.Category]; d != 0 {
				return d
			}
			return strings.Compare(a.Status, b.Status)
		})
	case sortUpdated:
		slices.SortStableFunc(out, func(a, b atlassian.Issue) int { return b.Updated.Compare(a.Updated) })
	case sortKey:
		slices.SortStableFunc(out, func(a, b atlassian.Issue) int { return keyNum(b.Key) - keyNum(a.Key) })
	}
	return out
}

const (
	sortRank = iota
	sortStatus
	sortUpdated
	sortKey
	nSorts
)

var sortNames = [nSorts]string{"rank", "status", "recently updated", "newest key"}

type jiraPrefs struct {
	Hidden []string `json:"hidden_statuses"`
	Sort   int      `json:"sort"`
}

func loadJiraPrefs() jiraPrefs {
	var p jiraPrefs
	state.Load("jira-prefs.json", &p)
	return p
}

func (m *Model) saveJiraPrefs() { _ = state.Save("jira-prefs.json", m.jiraPrefs) }

func keyNum(k string) int {
	n := 0
	fmt.Sscanf(k[strings.LastIndexByte(k, '-')+1:], "%d", &n)
	return n
}

// The site is set to Czech; show the standard English type names.
var typeNames = map[string]string{
	"Úkol":     "Task",
	"Podúloha": "Subtask",
	"Chyba":    "Bug",
	"Epos":     "Epic",
	"Příběh":   "Story",
}

func typeName(t string) string {
	if n, ok := typeNames[t]; ok {
		return n
	}
	return t
}

// chooseStatuses toggles which statuses the Jira view hides; it stays open so
// several can be flipped in a row.
func (m *Model) chooseStatuses() {
	src := m.jira.Sprint
	if m.teamJira {
		src = m.jira.Team
	}
	seen := map[string]bool{}
	var statuses []atlassian.Issue
	for _, is := range append(append([]atlassian.Issue{}, src...), m.jira.Sprint...) {
		if !seen[is.Status] {
			seen[is.Status] = true
			statuses = append(statuses, is)
		}
	}
	for _, h := range m.jiraPrefs.Hidden {
		if !seen[h] {
			statuses = append(statuses, atlassian.Issue{Status: h})
		}
	}
	var items []choice
	for _, is := range statuses {
		mark := sOK.Render("✓ ")
		if slices.Contains(m.jiraPrefs.Hidden, is.Status) {
			mark = sErr.Render("✗ ")
		}
		items = append(items, choice{label: mark + statusText(is), search: is.Status, value: is.Status})
	}
	m.chooser = newChooser("show statuses (enter toggles, esc done)", items, func(m *Model, ch choice) (tea.Model, tea.Cmd) {
		st := ch.value.(string)
		if i := slices.Index(m.jiraPrefs.Hidden, st); i >= 0 {
			m.jiraPrefs.Hidden = slices.Delete(m.jiraPrefs.Hidden, i, i+1)
		} else {
			m.jiraPrefs.Hidden = append(m.jiraPrefs.Hidden, st)
		}
		m.saveJiraPrefs()
		m.refilter()
		m.chooseStatuses()
		m.chooser.cur.pos = max(0, slices.IndexFunc(m.chooser.shown, func(c choice) bool { return c.value == st }))
		return m, nil
	})
}

// gotoTicket selects the issue in the Jira view, or opens it in the browser when
// it isn't on the sprint lists.
func (m *Model) gotoTicket(key string) (tea.Model, tea.Cmd) {
	if key == "" {
		m.setFlash("no ticket linked", true)
		return m, nil
	}
	for _, team := range []bool{false, true} {
		src := m.jira.Sprint
		if team {
			src = m.jira.Team
		}
		if !slices.ContainsFunc(src, func(is atlassian.Issue) bool { return is.Key == key }) {
			continue
		}
		m.view, m.teamJira = vJira, team
		m.filter.SetValue("")
		m.refilter()
		if i := slices.IndexFunc(m.vis.issues, func(is atlassian.Issue) bool { return is.Key == key }); i >= 0 {
			m.cur[vJira].pos = i
			return m, nil
		}
	}
	if m.atl == nil {
		return m, nil
	}
	m.setFlash(key+" isn't in the sprint lists (or is hidden) — opening it", false)
	return m, openURL(m.atl.IssueURL(key))
}

func (m *Model) listJira(h int) string {
	rows := m.vis.issues
	empty := "nothing in your sprint — d shows done, a the team"
	if m.atl == nil {
		empty = m.atlErr.Error()
	} else if m.jira.FetchedAt.IsZero() {
		empty = spinner[m.frame%len(spinner)] + " asking Jira…"
	}
	return m.table(jiraCols, len(rows), &m.cur[vJira], h, func(i int, sel bool) []string {
		is := rows[i]
		st := sText
		if sel {
			st = sBold
		}
		if is.Category == "done" {
			st = sDim
		}
		return []string{
			sTicket.Render(is.Key),
			st.Render(is.Summary),
			sDim.Render(typeName(is.Type)),
			statusText(is),
			sDim.Render(is.Assignee),
			m.issueMRs(is.Key),
			m.issueSessions(is.Key),
		}
	}, empty)
}

func (m *Model) issueMRs(key string) string {
	var parts []string
	for _, mr := range m.ix.ticketMRs[key] {
		st := sKey
		if mr.State == "merged" {
			st = lipgloss.NewStyle().Foreground(purple)
		}
		parts = append(parts, st.Render(fmt.Sprintf("!%d", mr.IID)))
	}
	return strings.Join(parts, " ")
}

// issueSessions shows a robot per paired session, lit when one is running.
func (m *Model) issueSessions(key string) string {
	ids := m.ix.ticketSess[key]
	if len(ids) == 0 {
		return ""
	}
	live, waiting := 0, false
	for _, id := range ids {
		for _, r := range m.sessions {
			if r.ID == id && r.live() {
				live++
				waiting = waiting || r.Live.Status == claude.StatusWaiting
			}
		}
	}
	s := fmt.Sprintf("✻ %d", len(ids))
	switch {
	case waiting:
		return lipgloss.NewStyle().Foreground(yellow).Bold(true).Render(s)
	case live > 0:
		return sOK.Render(s)
	}
	return sDim.Render(s)
}

func (m *Model) currentIssue() (atlassian.Issue, bool) {
	c := m.cur[vJira].pos
	if c < 0 || c >= len(m.vis.issues) {
		return atlassian.Issue{}, false
	}
	return m.vis.issues[c], true
}

func (m *Model) detailJira() string {
	is, ok := m.currentIssue()
	if !ok {
		return m.panel(nil)
	}
	lines := []string{sTicket.Render(is.Key) + " " + sBold.Render(is.Summary)}
	meta := []string{statusText(is), sDim.Render(typeName(is.Type)), sDim.Render(is.Assignee)}
	if is.Parent != "" {
		p := sTicket.Render(is.Parent)
		if pi, ok := m.ix.issues[is.Parent]; ok {
			p += sDim.Render(" " + pi.Summary)
		}
		meta = append(meta, p)
	}
	lines = append(lines, strings.Join(meta, sDim.Render("  ·  ")))
	if mrs := m.ix.ticketMRs[is.Key]; len(mrs) > 0 {
		var refs []string
		for _, mr := range mrs {
			refs = append(refs, m.mrRef(mr.WebURL))
		}
		lines = append(lines, strings.Join(refs, "   "))
	}
	if ids := m.ix.ticketSess[is.Key]; len(ids) > 0 {
		lines = append(lines, sDim.Render("sessions: ")+m.sessionRefs(ids))
	}
	if n := m.unansweredFor(is.Key); n > 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(purple).Render(fmt.Sprintf("%d unanswered mention(s) — see Mentions", n)))
	}
	return m.panel(lines)
}

func (m *Model) keyJira(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "a":
		m.teamJira = !m.teamJira
		m.cur[vJira] = cursor{}
		m.refilter()
		return m, nil
	case "d":
		m.showDone = !m.showDone
		m.refilter()
		return m, nil
	case "s":
		m.jiraPrefs.Sort = (m.jiraPrefs.Sort + 1) % nSorts
		m.saveJiraPrefs()
		m.refilter()
		m.setFlash("sorted by "+sortNames[m.jiraPrefs.Sort], false)
		return m, nil
	case "f":
		m.chooseStatuses()
		return m, nil
	}
	is, ok := m.currentIssue()
	if !ok || m.atl == nil {
		return m, nil
	}
	switch k {
	case "o":
		return m, openURL(m.atl.IssueURL(is.Key))
	case "y":
		return m, copyText(is.Key, is.Key)
	case "enter":
		if len(m.ix.ticketSess[is.Key]) > 0 {
			m.chooseSessions(m.ix.ticketSess[is.Key], "sessions for "+is.Key)
			return m, nil
		}
		return m.claudeForTicket(is.Key, is.Summary)
	case "c":
		return m.claudeForTicket(is.Key, is.Summary)
	case "b":
		return m.briefSession(is.Key, is.Summary)
	case "m":
		return m, m.loadTransitions(is.Key)
	case "e":
		return m, m.exportBrief(is.Key, func(path string) tea.Msg {
			b, err := os.ReadFile(path)
			if err != nil {
				return flashMsg{err.Error(), true}
			}
			return copyText(string(b), is.Key+" brief (with image paths)")()
		})
	case "i":
		return m, m.showImages(is.Key)
	}
	return m, nil
}

func briefDir() string { return state.Path("jira-export") }

func (m *Model) exportBrief(key string, then func(path string) tea.Msg) tea.Cmd {
	atl := m.atl
	m.setFlash("exporting "+key+"…", false)
	return func() tea.Msg {
		p, err := atl.ExportBrief(key, briefDir())
		if err != nil {
			return flashMsg{err.Error(), true}
		}
		return then(p)
	}
}

type startMsg struct{ sp launch.Spec }

type transitionsMsg struct {
	key string
	ts  []atlassian.Transition
	err error
}

func (m *Model) loadTransitions(key string) tea.Cmd {
	atl := m.atl
	return func() tea.Msg {
		ts, err := atl.Transitions(key)
		return transitionsMsg{key, ts, err}
	}
}

func (m *Model) showTransitions(msg transitionsMsg) {
	if msg.err != nil {
		m.setFlash(msg.err.Error(), true)
		return
	}
	var items []choice
	for _, t := range msg.ts {
		label := sText.Render(t.Name)
		if t.To != t.Name {
			label += sDim.Render(" → " + t.To)
		}
		items = append(items, choice{label: label, search: t.Name + " " + t.To, value: t})
	}
	key := msg.key
	m.chooser = newChooser("move "+key, items, func(m *Model, ch choice) (tea.Model, tea.Cmd) {
		t := ch.value.(atlassian.Transition)
		atl := m.atl
		return m, func() tea.Msg {
			if err := atl.Transition(key, t.ID); err != nil {
				return flashMsg{err.Error(), true}
			}
			return refreshMsg(srcJira)
		}
	})
}

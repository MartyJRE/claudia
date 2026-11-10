package ui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MartyJRE/claudia/internal/claude"
)

// updateLedger takes the scan into the cost ledger, with the ticket claudia
// knows each recent session by, and saves it when anything changed.
func (m *Model) updateLedger() {
	if m.ledger == nil {
		m.ledger = claude.OpenLedger()
	}
	changed := m.ledger.Merge(m.usageScanner.Sessions())
	for _, r := range m.sessions {
		t, ok := m.ix.sessionTicket[r.ID]
		if k, set := m.links.Ticket(r.ID); !ok && !(set && k == "-") {
			continue
		}
		if m.ledger.SetTicket(r.ID, t) {
			changed = true
		}
	}
	if changed {
		if err := m.ledger.Save(); err != nil {
			m.setFlash("cost ledger: "+err.Error(), true)
		}
	}
	m.costs = ticketCosts(m.ledger)
}

func ticketCosts(l *claude.Ledger) map[string]claude.TicketCost {
	out := map[string]claude.TicketCost{}
	for _, t := range l.Tickets() {
		out[t.Ticket] = t
	}
	return out
}

const usageLayoutKey = "usage"

func (m *Model) usageByTicket() bool { return m.listPrefs.Layout[usageLayoutKey] == 1 }

func (m *Model) keyUsage(k string) (tea.Model, tea.Cmd) {
	if m.action(k) != "usage.layout" {
		return m, nil
	}
	if m.listPrefs.Layout == nil {
		m.listPrefs.Layout = map[string]int{}
	}
	name := "by ticket"
	if m.usageByTicket() {
		m.listPrefs.Layout[usageLayoutKey], name = 0, "by day"
	} else {
		m.listPrefs.Layout[usageLayoutKey] = 1
	}
	m.saveListPrefs()
	m.cur[vUsage] = cursor{}
	m.refilter()
	m.setFlash("layout: "+name, false)
	return m, nil
}

// costRow is an epic's header or one of its tickets in the by-ticket layout.
type costRow struct {
	epic   bool
	cost   claude.TicketCost
	models []claude.DayModel
}

// modelRows lists a total's models, the costliest first, the way a day's are.
func modelRows(models map[string]claude.Tokens) []claude.DayModel {
	var out []claude.DayModel
	for model, t := range models {
		out = append(out, claude.DayModel{Model: model, Tokens: t})
	}
	usd := func(dm claude.DayModel) float64 { c, _ := dm.Cost(dm.Model); return c }
	slices.SortFunc(out, func(a, b claude.DayModel) int {
		return cmp.Or(cmp.Compare(usd(b), usd(a)), cmp.Compare(a.Model, b.Model))
	})
	return out
}

func (m *Model) costRows() []costRow {
	if !m.usageByTicket() {
		return nil
	}
	parent := map[string]string{}
	for k, is := range m.ix.issues {
		parent[k] = is.Parent
	}
	var out []costRow
	for _, e := range m.ledger.Epics(parent) {
		out = append(out, costRow{epic: true, cost: e.TicketCost, models: modelRows(e.Models)})
		for _, t := range e.Tickets {
			out = append(out, costRow{cost: t, models: modelRows(t.Models)})
		}
	}
	return out
}

var costCols = []col{
	{"TICKET", 22, 0},
	{"SUMMARY", 36, 110},
	{"SESSIONS", 8, 0},
	{"LAST", 10, 0},
	{"TOKENS", 8, 100},
	{"COST", 9, 0},
	{"", 0, 0},
}

func (m *Model) issueSummary(key string) string {
	if is, ok := m.ix.issues[key]; ok {
		return is.Summary
	}
	for _, is := range m.ix.issues {
		if is.Parent == key && is.ParentSummary != "" {
			return is.ParentSummary
		}
	}
	return ""
}

func (m *Model) listCosts(h int) string {
	ws := layout(costCols, m.width, m.vis.widths.costs)
	empty := m.spin() + " reading transcripts…"
	if m.usageLoaded {
		empty = "no sessions in the cost ledger yet"
	}
	return m.table(costCols, m.vis.widths.costs, len(m.vis.costRows), &m.cur[vUsage], h, m.costCells(max(0, ws[len(ws)-1])), empty)
}

// costCells draws the tickets, with a cost bar barW wide for the costliest
// epic and ticket.
func (m *Model) costCells(barW int) func(int, bool) []string {
	rows := m.vis.costRows
	peak := map[bool]float64{}
	for _, r := range rows {
		peak[r.epic] = max(peak[r.epic], r.cost.USD)
	}
	return func(i int, sel bool) []string {
		r := rows[i]
		key, st := r.cost.Ticket, sTicket
		switch {
		case r.epic && key == "":
			key, st = "no epic", sBold
		case r.epic:
			st = sTicket.Bold(true)
		case key == "":
			key, st = "  no ticket", sDim
		default:
			key = "  " + key
		}
		summary := sText.Render(m.issueSummary(r.cost.Ticket))
		if r.epic {
			summary = sBold.Render(m.issueSummary(r.cost.Ticket))
		}
		bar := ""
		if p := peak[r.epic]; p > 0 {
			bar = stackedBar(r.models, r.cost.USD, int(r.cost.USD/p*float64(barW)))
		}
		last := ""
		if !r.cost.Last.IsZero() {
			last = r.cost.Last.Local().Format("02 Jan")
		}
		return []string{
			st.Render(key),
			summary,
			sDim.Render(fmt.Sprint(r.cost.Sessions)),
			sDim.Render(last),
			sDim.Render(tokens(r.cost.Tokens)),
			costText(r.cost.USD, r.cost.Unknown),
			bar,
		}
	}
}

// detailCost breaks a row down: an epic by model, a ticket by session.
func (m *Model) detailCost() string {
	c := m.cur[vUsage].pos
	if c < 0 || c >= len(m.vis.costRows) {
		return m.panel(nil)
	}
	r := m.vis.costRows[c]
	key := r.cost.Ticket
	title := sTicket.Render(key) + " " + sBold.Render(m.issueSummary(key))
	switch {
	case r.epic && key == "":
		title = sBold.Render("no epic")
	case key == "":
		title = sBold.Render("sessions on no ticket")
	}
	lines := []string{title + sDim.Render(fmt.Sprintf("  ·  %d sessions  ·  ", r.cost.Sessions)) + costText(r.cost.USD, r.cost.Unknown)}
	if r.epic {
		for _, dm := range r.models {
			f, v := modelName(dm.Model)
			usd := "no price"
			if c, ok := dm.Cost(dm.Model); ok {
				usd = money(c)
			}
			lines = append(lines, lipgloss.NewStyle().Foreground(modelColor(f)).Width(11).Render(f+" "+v)+" "+
				sDim.Render(tokens(dm.Total())+" tokens  ")+lipgloss.NewStyle().Foreground(yellow).Render(usd))
		}
		return m.panel(lines)
	}
	names := map[string]string{}
	for _, s := range m.sessions {
		names[s.ID] = s.Name()
	}
	for _, s := range m.ledger.Sessions(key) {
		name := names[s.ID]
		if name == "" {
			name = s.ID[:min(8, len(s.ID))]
		}
		var fams []string
		for _, dm := range modelRows(s.Models) {
			f, v := modelName(dm.Model)
			fams = append(fams, lipgloss.NewStyle().Foreground(modelColor(f)).Render(f+" "+v))
		}
		lines = append(lines, fmt.Sprintf("%s  %s  %s  %s  %s",
			sDim.Render(s.Last.Local().Format("02 Jan 15:04")), costText(s.USD, s.Unknown),
			sText.Render(name), sDim.Render(s.Profile), strings.Join(fams, sDim.Render(", "))))
	}
	return m.panel(lines)
}

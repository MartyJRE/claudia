package ui

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/state"
)

type usageRow struct {
	day     string
	models  []claude.DayModel
	tokens  claude.Tokens
	cost    float64
	unknown bool // a model without a known price
}

type usageMsg []claude.DayModel

func (m *Model) loadUsage() tea.Cmd {
	sc := m.usageScanner
	return func() tea.Msg { return usageMsg(sc.Scan()) }
}

func (m *Model) usageRows() []usageRow {
	byDay := map[string]*usageRow{}
	var days []string
	for _, dm := range m.usage {
		r := byDay[dm.Day]
		if r == nil {
			r = &usageRow{day: dm.Day}
			byDay[dm.Day] = r
			days = append(days, dm.Day)
		}
		r.models = append(r.models, dm)
		r.tokens.Add(dm.Tokens)
		if c, ok := dm.Cost(dm.Model); ok {
			r.cost += c
		} else {
			r.unknown = true
		}
	}
	slices.Sort(days)
	slices.Reverse(days)
	var out []usageRow
	for _, d := range days {
		out = append(out, *byDay[d])
	}
	return out
}

var usageCols = []col{
	{"DAY", 11, 0},
	{"MODELS", 22, 110},
	{"INPUT", 7, 120},
	{"OUTPUT", 7, 0},
	{"CACHE WRITE", 11, 100},
	{"CACHE READ", 10, 0},
	{"TOTAL", 8, 0},
	{"COST", 9, 0},
	{"", 0, 0},
}

func (m *Model) listUsage(h int) string {
	top := m.viewLimits()
	h -= lipgloss.Height(top)
	rows := m.vis.usage
	peak := 0.0
	for _, r := range rows {
		peak = max(peak, r.cost)
	}
	empty := spinner[m.frame%len(spinner)] + " reading transcripts…"
	if m.usageLoaded {
		empty = "no usage in the last 31 days"
	}
	ws := layout(usageCols, m.width)
	barW := max(0, ws[len(ws)-1])
	table := m.table(usageCols, len(rows), &m.cur[vUsage], h, func(i int, sel bool) []string {
		r := rows[i]
		t, _ := time.ParseInLocation("2006-01-02", r.day, time.Local)
		dayStyle := sText
		if t.Weekday() == time.Saturday || t.Weekday() == time.Sunday {
			dayStyle = sDim
		}
		var fams []string
		for _, dm := range r.models {
			f, v := modelName(dm.Model)
			fams = append(fams, lipgloss.NewStyle().Foreground(modelColor(f)).Render(f+" "+v))
		}
		bar := ""
		if peak > 0 {
			bar = sBar.Render(strings.Repeat("━", int(r.cost/peak*float64(barW))))
		}
		cost := money(r.cost)
		if r.unknown {
			cost += sDim.Render("+?")
		}
		return []string{
			dayStyle.Render(t.Format("Mon 02 Jan")),
			strings.Join(fams, sDim.Render(", ")),
			sDim.Render(tokens(r.tokens.Input)),
			sText.Render(tokens(r.tokens.Output)),
			sDim.Render(tokens(r.tokens.Write5m + r.tokens.Write1h)),
			sDim.Render(tokens(r.tokens.Read)),
			sText.Render(tokens(r.tokens.Total())),
			lipgloss.NewStyle().Foreground(yellow).Render(cost),
			bar,
		}
	}, empty)
	return top + table
}

func money(v float64) string {
	if v >= 1000 {
		return fmt.Sprintf("$%.0f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}

// viewLimits shows the plan windows per profile plus period totals.
func (m *Model) viewLimits() string {
	var lines []string
	for _, p := range m.store.Profiles {
		l, ok := claude.ReadLimits(state.Path("limits-" + filepath.Base(p.Dir) + ".json"))
		label := sBold.Render(fmt.Sprintf(" %-9s", p.Name))
		if !ok {
			lines = append(lines, label+sDim.Render("plan limits appear once a session in this profile draws its status line"))
			continue
		}
		var parts []string
		for _, w := range []struct {
			name string
			w    *claude.Window
		}{{"5h", l.FiveHour}, {"7d", l.SevenDay}, {"extra", l.SpendLimit}} {
			if w.w == nil {
				continue
			}
			parts = append(parts, limitBar(w.name, *w.w))
		}
		stale := ""
		if a := age(l.At); a != "now" {
			stale = sDim.Render("  as of " + a + " ago")
		}
		lines = append(lines, label+strings.Join(parts, "   ")+stale)
	}

	var today, week, month float64
	now := time.Now()
	for _, r := range m.vis.usage {
		t, _ := time.ParseInLocation("2006-01-02", r.day, time.Local)
		if r.day == now.Format("2006-01-02") {
			today += r.cost
		}
		if now.Sub(t) < 7*24*time.Hour {
			week += r.cost
		}
		if t.Month() == now.Month() && t.Year() == now.Year() {
			month += r.cost
		}
	}
	y := lipgloss.NewStyle().Foreground(yellow)
	lines = append(lines, "", " "+sDim.Render("API-equivalent cost   today ")+y.Render(money(today))+
		sDim.Render("   last 7 days ")+y.Render(money(week))+
		sDim.Render("   "+now.Format("January")+" ")+y.Render(money(month)), "")
	return strings.Join(lines, "\n") + "\n"
}

func limitBar(name string, w claude.Window) string {
	const n = 20
	p := w.UsedPercentage
	filled := min(n, int(p/100*n+0.5))
	c := green
	switch {
	case p >= 90:
		c = red
	case p >= 70:
		c = yellow
	}
	st := lipgloss.NewStyle().Foreground(c)
	s := sDim.Render(name+" ") + st.Render(strings.Repeat("━", filled)) + sRule.Render(strings.Repeat("━", n-filled)) +
		st.Render(fmt.Sprintf(" %3.0f%%", p))
	if r := w.Resets(); !r.IsZero() {
		f := "15:04"
		if r.Sub(time.Now()) > 20*time.Hour {
			f = "Mon 15:04"
		}
		s += sDim.Render(" resets " + r.Local().Format(f))
	}
	return s
}

func (m *Model) detailUsage() string {
	c := m.cur[vUsage].pos
	if c < 0 || c >= len(m.vis.usage) {
		return m.panel(nil)
	}
	r := m.vis.usage[c]
	t, _ := time.ParseInLocation("2006-01-02", r.day, time.Local)
	lines := []string{sBold.Render(t.Format("Monday 2 January")) + sDim.Render(fmt.Sprintf("  ·  %d requests", r.tokens.Requests))}
	for _, dm := range r.models {
		f, v := modelName(dm.Model)
		cost := "no price"
		if c, ok := dm.Cost(dm.Model); ok {
			cost = money(c)
		}
		lines = append(lines, fmt.Sprintf("%s  %s %s  %s %s  %s %s  %s %s  %s",
			lipgloss.NewStyle().Foreground(modelColor(f)).Width(11).Render(f+" "+v),
			sDim.Render("in"), sText.Render(tokens(dm.Input)),
			sDim.Render("out"), sText.Render(tokens(dm.Output)),
			sDim.Render("write 5m/1h"), sText.Render(tokens(dm.Write5m)+"/"+tokens(dm.Write1h)),
			sDim.Render("read"), sText.Render(tokens(dm.Read)),
			lipgloss.NewStyle().Foreground(yellow).Render(cost)))
	}
	return m.panel(lines)
}

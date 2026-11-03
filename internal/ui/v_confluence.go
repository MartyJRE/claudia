package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/MartyJRE/claudia/internal/atlassian"
)

var pageCols = []col{
	{"SPACE", 8, 0},
	{"PAGE", 0, 0},
	{"EDITED", 8, 0},
}

func (m *Model) filterPages(match func(...string) bool) []atlassian.Page {
	var out []atlassian.Page
	for _, p := range m.conf.Mine {
		if match(p.Title, p.Space) {
			out = append(out, p)
		}
	}
	return out
}

func (m *Model) listConfluence(h int) string {
	rows := m.vis.pages
	empty := "no pages"
	if m.atl == nil && m.atlErr != nil {
		empty = m.atlErr.Error()
	} else if m.conf.FetchedAt.IsZero() {
		empty = spinner[m.frame%len(spinner)] + " asking Confluence…"
	}
	return m.table(pageCols, len(rows), &m.cur[vConfluence], h, func(i int, sel bool) []string {
		p := rows[i]
		st := sText
		if sel {
			st = sBold
		}
		return []string{sDim.Render(p.Space), st.Render(p.Title), sDim.Render(age(p.Modified))}
	}, empty)
}

func (m *Model) currentPage() (atlassian.Page, bool) {
	c := m.cur[vConfluence].pos
	if c < 0 || c >= len(m.vis.pages) {
		return atlassian.Page{}, false
	}
	return m.vis.pages[c], true
}

func (m *Model) detailConfluence() string {
	p, ok := m.currentPage()
	if !ok {
		return m.panel(nil)
	}
	return m.panel([]string{
		sBold.Render(p.Title),
		sDim.Render(p.Space + "  ·  edited " + p.Modified.Local().Format("Mon 2 Jan 15:04")),
		sDim.Render(p.URL),
		sText.Render(cleanExcerpt(p.Excerpt)),
	})
}

func (m *Model) keyConfluence(k string) (tea.Model, tea.Cmd) {
	p, ok := m.currentPage()
	if !ok {
		return m, nil
	}
	switch m.action(k) {
	case "confluence.open":
		return m, openURL(p.URL)
	case "confluence.copy":
		return m, copyText(p.URL, "link")
	}
	return m, nil
}

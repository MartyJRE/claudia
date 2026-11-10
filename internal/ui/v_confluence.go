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
	rows := m.vis.pageRows
	empty := "no pages"
	if m.atl == nil && m.atlErr != nil {
		empty = m.atlErr.Error()
	} else if m.conf.FetchedAt.IsZero() {
		empty = m.spin() + " asking Confluence…"
	}
	return m.table(pageCols, m.vis.widths.pages, len(rows), &m.cur[vConfluence], h, m.pageCells, empty)
}

func (m *Model) pageCells(i int, sel bool) []string {
	r := m.vis.pageRows[i]
	if r.header() {
		return m.listHeader(vConfluence, r, 0)
	}
	p := m.vis.pages[r.item]
	st := sText
	if sel {
		st = sBold
	}
	return []string{sDim.Render(listIndent(r) + p.Space), st.Render(p.Title), sDim.Render(age(p.Modified))}
}

func (m *Model) currentPage() (atlassian.Page, bool) {
	i, ok := m.listItem(vConfluence)
	if !ok {
		return atlassian.Page{}, false
	}
	return m.vis.pages[i], true
}

func (m *Model) detailConfluence() string {
	if g, ok := m.currentGroup(vConfluence); ok {
		return m.detailGroup(vConfluence, g)
	}
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
	act := m.action(k)
	if m.keyList(act) {
		return m, nil
	}
	p, ok := m.currentPage()
	if !ok {
		return m, nil
	}
	switch act {
	case "confluence.open":
		return m, openURL(p.URL)
	case "confluence.copy":
		return m, copyText(p.URL, "link")
	}
	return m, nil
}

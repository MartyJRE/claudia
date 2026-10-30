package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type col struct {
	title string
	w     int // 0 = flex
	min   int // hide below this terminal width
}

type cursor struct{ pos, offset int }

func (c *cursor) clamp(n int) {
	c.pos = max(0, min(c.pos, n-1))
}

// layout resolves column widths for the terminal width; hidden columns get -1.
func layout(cols []col, width int) []int {
	ws := make([]int, len(cols))
	used, flex := 3, -1
	for i, c := range cols {
		switch {
		case c.min > 0 && width < c.min:
			ws[i] = -1
		case c.w == 0:
			flex = i
		default:
			ws[i] = c.w
			used += c.w + 1
		}
	}
	if flex >= 0 {
		ws[flex] = max(10, width-used-1)
	}
	return ws
}

func joinCells(ws []int, cells []string) string {
	var parts []string
	for i, w := range ws {
		if w < 0 || i >= len(cells) {
			continue
		}
		parts = append(parts, fit(cells[i], w))
	}
	return strings.Join(parts, " ")
}

// table renders a scrolling list with a header row and a highlighted cursor row.
func (m *Model) table(cols []col, n int, cur *cursor, h int, cell func(i int, selected bool) []string, empty string) string {
	if h <= 0 {
		return ""
	}
	if n == 0 {
		return lipgloss.Place(m.width, h, lipgloss.Center, lipgloss.Center, sDim.Render(empty))
	}
	ws := layout(cols, m.width)
	var titles []string
	for _, c := range cols {
		titles = append(titles, sDim.Render(c.title))
	}
	rows := h - 1
	cur.clamp(n)
	if cur.pos < cur.offset {
		cur.offset = cur.pos
	}
	if cur.pos >= cur.offset+rows {
		cur.offset = cur.pos - rows + 1
	}
	cur.offset = max(0, min(cur.offset, n-rows))

	var b strings.Builder
	b.WriteString("   " + joinCells(ws, titles))
	for i := cur.offset; i < min(n, cur.offset+rows); i++ {
		sel := i == cur.pos
		cells := cell(i, sel)
		line := joinCells(ws, cells)
		if len(cells) == 1 {
			// A single cell is a group header spanning the whole row.
			line = fit(cells[0], m.width-4)
		}
		b.WriteString("\n")
		if sel {
			b.WriteString(sBar.Render(" ▌ ") + sCursor.Width(m.width-3).Render(line))
		} else {
			b.WriteString("   " + line)
		}
	}
	return lipgloss.NewStyle().Height(h).MaxHeight(h).Render(b.String())
}

func fit(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return s + strings.Repeat(" ", max(0, w-lipgloss.Width(s)))
}

// panel draws the detail box under a list.
func (m *Model) panel(lines []string) string {
	w := m.width - 2
	inner := w - 4
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], inner, "…")
	}
	if len(lines) > detailHeight-2 {
		lines = lines[:detailHeight-2]
	}
	if len(lines) == 0 {
		lines = []string{sDim.Render("—")}
	}
	return sPanel.Width(w).Height(detailHeight - 2).Render(strings.Join(lines, "\n"))
}

package ui

import (
	"slices"
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

// joinCells lays cells out in their widths; the hl-th one gets the cursor
// highlight (-1 for none).
func joinCells(ws []int, cells []string, hl int) string {
	var parts []string
	for i, w := range ws {
		if w < 0 || i >= len(cells) {
			continue
		}
		c := fit(cells[i], w)
		if i == hl {
			c = highlight(c)
		}
		parts = append(parts, c)
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
	b.WriteString("   " + joinCells(ws, titles, -1))
	// The cursor lights up the row's main column (the one that stretches).
	main := slices.IndexFunc(cols, func(c col) bool { return c.w == 0 })
	for i := cur.offset; i < min(n, cur.offset+rows); i++ {
		sel, picked := i == cur.pos, m.inVisual(i)
		cells := cell(i, sel)
		hl := -1
		if sel || picked {
			hl = main
		}
		line := joinCells(ws, cells, hl)
		if len(cells) == 1 {
			// A single cell is a group header spanning the whole row.
			line = fit(cells[0], m.width-4)
			if (sel || picked) && main >= 0 {
				line = highlightSpan(line, colStart(ws, main), ws[main])
			}
		}
		b.WriteString("\n")
		switch {
		case sel:
			b.WriteString(sBar.Render(" ▌ ") + line)
		case picked:
			b.WriteString(sAccent.Render(" ▏ ") + line)
		default:
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

// colStart is where column i begins in a joined line.
func colStart(ws []int, i int) int {
	x := 0
	for j := 0; j < i; j++ {
		if ws[j] >= 0 {
			x += ws[j] + 1
		}
	}
	return x
}

// highlightSpan lights the w cells from start, so a spanning header is lit
// exactly where the main column is on every other row.
func highlightSpan(line string, start, w int) string {
	left := ansi.Truncate(line, start, "")
	mid := fit(ansi.Truncate(ansi.TruncateLeft(line, start, ""), w, ""), w)
	right := ansi.TruncateLeft(line, start+w, "")
	return left + highlight(mid) + right
}

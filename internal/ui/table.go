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

// flexFloor is how narrow the stretching column may get so another can show
// all of its content.
const flexFloor = 40

// layout resolves column widths for the terminal width; hidden columns get -1.
// With natural (the widest content per column, from measure) a column fits its
// content up to its w, and one whose content is wider may take what the others
// gave back, up to twice its w, as long as the stretching column keeps flexFloor.
func layout(cols []col, width int, natural []int) []int {
	ws := make([]int, len(cols))
	used, flex, freed := 3, -1, 0
	for i, c := range cols {
		switch {
		case c.min > 0 && width < c.min:
			ws[i] = -1
		case c.w == 0:
			flex = i
		default:
			ws[i] = c.w
			if i < len(natural) {
				ws[i] = min(c.w, max(1, lipgloss.Width(c.title), natural[i]))
				freed += c.w - ws[i]
			}
			used += ws[i] + 1
		}
	}
	if flex >= 0 {
		room := min(freed, width-used-1-flexFloor)
		for i, c := range cols {
			if room <= 0 {
				break
			}
			if ws[i] < 0 || c.w == 0 || i >= len(natural) || natural[i] <= ws[i] {
				continue
			}
			add := min(min(natural[i], 2*c.w)-ws[i], room)
			ws[i] += add
			used += add
			room -= add
		}
		ws[flex] = max(10, width-used-1)
	}
	return ws
}

// measure is the widest cell each column holds over n rows; group headers
// (single cells) don't count.
func measure(cols []col, n int, cell func(i int, selected bool) []string) []int {
	ws := make([]int, len(cols))
	for i := range n {
		cells := cell(i, false)
		if len(cells) == 1 {
			continue
		}
		for j, c := range cells {
			if j < len(ws) {
				ws[j] = max(ws[j], lipgloss.Width(c))
			}
		}
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

// table renders a scrolling list with a header row and a highlighted cursor
// row. sub, when given, adds a dim second line under a row, starting at its
// main column; an empty one keeps the row to one line.
func (m *Model) table(cols []col, natural []int, n int, cur *cursor, h int, cell func(i int, selected bool) []string, empty string, sub ...func(i int) string) string {
	if h <= 0 {
		return ""
	}
	if n == 0 {
		return lipgloss.Place(m.width, h, lipgloss.Center, lipgloss.Center, sDim.Render(empty))
	}
	ws := layout(cols, m.width, natural)
	var titles []string
	for _, c := range cols {
		titles = append(titles, sDim.Render(c.title))
	}
	under := func(int) string { return "" }
	if len(sub) > 0 && sub[0] != nil {
		under = sub[0]
	}
	subs := make([]string, n)
	height := func(i int) int {
		if subs[i] != "" {
			return 2
		}
		return 1
	}
	for i := range n {
		subs[i] = under(i)
	}
	span := func(from, to int) int { // lines rows from..to take
		t := 0
		for i := from; i <= to; i++ {
			t += height(i)
		}
		return t
	}
	rows := h - 1
	cur.clamp(n)
	cur.offset = max(0, min(cur.offset, n-1))
	if cur.pos < cur.offset {
		cur.offset = cur.pos
	}
	for cur.offset < cur.pos && span(cur.offset, cur.pos) > rows {
		cur.offset++
	}
	for cur.offset > 0 && span(cur.offset-1, n-1) <= rows {
		cur.offset--
	}

	var b strings.Builder
	b.WriteString("   " + joinCells(ws, titles, -1))
	// The cursor lights up the row's main column (the one that stretches).
	main := slices.IndexFunc(cols, func(c col) bool { return c.w == 0 })
	used := 0
	// The first row always shows; with room for one line its sub line is cut.
	for i := cur.offset; i < n && (used+height(i) <= rows || i == cur.offset); i++ {
		used += height(i)
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
		if subs[i] != "" {
			at := max(0, colStart(ws, main))
			b.WriteString("\n")
			if sel {
				b.WriteString(sBar.Render(" ▌ "))
			} else {
				b.WriteString("   ")
			}
			b.WriteString(strings.Repeat(" ", at) + ansi.Truncate(subs[i], max(1, m.width-4-at), "…"))
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

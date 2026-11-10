package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestLayoutShell(t *testing.T) {
	cols := []col{{"A", 5, 0}, {"B", 0, 0}, {"C", 10, 100}, {"D", 4, 0}}
	for _, c := range []struct {
		width int
		want  []int
	}{
		{120, []int{5, 94, 10, 4}},
		{99, []int{5, 84, -1, 4}},
		{20, []int{5, 10, -1, 4}},
	} {
		if got := layout(cols, c.width, nil); !slices.Equal(got, c.want) {
			t.Errorf("width %d: %v, want %v", c.width, got, c.want)
		}
	}
	if got := layout([]col{{"A", 3, 0}}, 80, nil); !slices.Equal(got, []int{3}) {
		t.Errorf("no flex column: %v", got)
	}
}

func TestFitShell(t *testing.T) {
	for _, c := range []struct {
		s    string
		w    int
		want string
	}{
		{"abc", 5, "abc  "},
		{"abcdef", 4, "abc…"},
		{"abcd", 4, "abcd"},
		{sKey.Render("ab"), 3, "ab "},
	} {
		if got := plainShell(fit(c.s, c.w)); got != c.want {
			t.Errorf("fit(%q, %d) = %q, want %q", c.s, c.w, got, c.want)
		}
	}
	if got := joinCells([]int{3, -1, 2}, []string{"a", "hidden", "b"}, -1); got != "a   b " {
		t.Errorf("joinCells skips hidden columns: %q", got)
	}
	if got := colStart([]int{3, -1, 2, 4}, 3); got != 7 {
		t.Errorf("colStart: %d", got)
	}
}

func TestTableScrollShell(t *testing.T) {
	m := &Model{width: 60}
	cols := []col{{"N", 4, 0}, {"NAME", 0, 0}}
	cell := func(i int, sel bool) []string { return []string{fmt.Sprint(i), fmt.Sprintf("item-%02d", i)} }
	cur := &cursor{pos: 30}
	out := m.table(cols, nil, 50, cur, 10, cell, "empty")
	if h := lipgloss.Height(out); h != 10 {
		t.Fatalf("height %d", h)
	}
	lines := strings.Split(plainShell(out), "\n")
	if !strings.Contains(lines[0], "NAME") {
		t.Errorf("first line is the header: %q", lines[0])
	}
	if cur.offset != 22 || !strings.HasPrefix(lines[9], " ▌ ") || !strings.Contains(lines[9], "item-30") {
		t.Errorf("cursor scrolled into view at the bottom: offset %d, last %q", cur.offset, lines[9])
	}
	cur.pos = 5
	lines = strings.Split(plainShell(m.table(cols, nil, 50, cur, 10, cell, "")), "\n")
	if cur.offset != 5 || !strings.Contains(lines[1], "item-05") {
		t.Errorf("scrolling up puts the cursor on top: offset %d %q", cur.offset, lines[1])
	}
	cur.pos = 99
	m.table(cols, nil, 50, cur, 10, cell, "")
	if cur.pos != 49 || cur.offset != 41 {
		t.Errorf("cursor clamps to the last row: %+v", *cur)
	}
	cur.pos, cur.offset = 0, 40
	m.table(cols, nil, 3, cur, 10, cell, "")
	if cur.offset != 0 {
		t.Errorf("a list that shrank resets the offset: %+v", *cur)
	}
	if got := m.table(cols, nil, 5, cur, 0, cell, ""); got != "" {
		t.Errorf("no room renders nothing: %q", got)
	}
	empty := plainShell(m.table(cols, nil, 0, cur, 5, cell, "nothing here"))
	if lipgloss.Height(empty) != 5 || !strings.Contains(empty, "nothing here") {
		t.Errorf("empty message centered in the space: %q", empty)
	}
}

func TestTableGroupHeaderShell(t *testing.T) {
	m := &Model{width: 40}
	cols := []col{{"S", 4, 0}, {"NAME", 0, 0}}
	cell := func(i int, sel bool) []string {
		if i == 0 {
			return []string{"     ▾ group header spanning the whole row and more"}
		}
		return []string{"x", "y"}
	}
	lines := strings.Split(plainShell(m.table(cols, nil, 2, &cursor{}, 3, cell, "")), "\n")
	if w := ansi.StringWidth(lines[1]); w > 40 || !strings.HasSuffix(lines[1], "…") {
		t.Errorf("header spans the row and is cut to it: %d %q", w, lines[1])
	}
}

func TestPanelShell(t *testing.T) {
	m := &Model{width: 30}
	if s := plainShell(m.panel(nil)); !strings.Contains(s, "—") || lipgloss.Height(s) != detailHeight {
		t.Errorf("empty panel: %q", s)
	}
	var lines []string
	for i := range 10 {
		lines = append(lines, fmt.Sprintf("line %d %s", i, strings.Repeat("x", 40)))
	}
	s := plainShell(m.panel(lines))
	if lipgloss.Height(s) != detailHeight {
		t.Errorf("panel keeps its height: %d", lipgloss.Height(s))
	}
	if !strings.Contains(s, "line 5") || strings.Contains(s, "line 6") {
		t.Errorf("panel shows %d lines at most:\n%s", detailHeight-2, s)
	}
	for _, l := range strings.Split(s, "\n") {
		if ansi.StringWidth(l) > 30 {
			t.Errorf("panel line wider than the box: %q", l)
		}
	}
}

func TestHighlightSpanShell(t *testing.T) {
	line := "0123456789"
	if got := plainShell(highlightSpan(line, 2, 4)); got != line {
		t.Errorf("highlight keeps the text: %q", got)
	}
	if got := plainShell(highlightSpan("ab", 1, 4)); got != "ab   " {
		t.Errorf("span past the end is padded: %q", got)
	}
}

func TestTableSubLinesShell(t *testing.T) {
	m := &Model{width: 30}
	cols := []col{{"N", 4, 0}, {"NAME", 0, 0}}
	cell := func(i int, sel bool) []string { return []string{fmt.Sprint(i), fmt.Sprintf("item-%02d", i)} }
	sub := func(i int) string {
		if i%2 == 0 {
			return fmt.Sprintf("sub-%02d", i)
		}
		return ""
	}
	render := func(cur *cursor, h int) []string {
		var out []string
		for _, l := range strings.Split(plainShell(m.table(cols, nil, 6, cur, h, cell, "", sub)), "\n") {
			out = append(out, strings.TrimRight(l, " "))
		}
		return out
	}
	cur := &cursor{}
	if got, want := render(cur, 6), []string{"   N    NAME", " ▌ 0    item-00", " ▌      sub-00", "   1    item-01", "   2    item-02", "        sub-02"}; !slices.Equal(got, want) {
		t.Errorf("sub lines sit under their row at its main column, barred when selected:\n%q\n%q", got, want)
	}
	cur.pos = 5
	if got, want := render(cur, 6), []string{"   N    NAME", "   3    item-03", "   4    item-04", "        sub-04", " ▌ 5    item-05", ""}; !slices.Equal(got, want) || cur.offset != 3 {
		t.Errorf("two-line rows count double when scrolling to the cursor: offset %d\n%q", cur.offset, got)
	}
	if got := render(cur, 8); cur.offset != 1 || got[1] != "   1    item-01" || got[7] != " ▌ 5    item-05" {
		t.Errorf("more room scrolls back as far as fits: offset %d\n%q", cur.offset, got)
	}
	cur.pos, cur.offset = 2, 0
	if got := render(cur, 2); cur.offset != 2 || !slices.Equal(got, []string{"   N    NAME", " ▌ 2    item-02"}) {
		t.Errorf("a two-line cursor row in a one-line space shows without its sub line: offset %d %q", cur.offset, got)
	}

	cur = &cursor{}
	long := func(int) string { return strings.Repeat("x", 40) }
	lines := strings.Split(plainShell(m.table(cols, nil, 1, cur, 3, cell, "", long)), "\n")
	if lines[2] != " ▌      "+strings.Repeat("x", 20)+"…" {
		t.Errorf("a long sub line is cut to the width: %q", lines[2])
	}
	if got := plainShell(m.table(cols, nil, 2, cur, 3, cell, "", nil)); strings.Count(got, "item-") != 2 {
		t.Errorf("a nil sub keeps rows to one line:\n%s", got)
	}
}

func TestCursorClampShell(t *testing.T) {
	for _, c := range []struct{ pos, n, want int }{{5, 3, 2}, {-1, 3, 0}, {1, 3, 1}, {4, 0, 0}} {
		cur := cursor{pos: c.pos}
		cur.clamp(c.n)
		if cur.pos != c.want {
			t.Errorf("clamp(%d) from %d: %d, want %d", c.n, c.pos, cur.pos, c.want)
		}
	}
}

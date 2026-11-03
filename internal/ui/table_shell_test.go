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
		if got := layout(cols, c.width); !slices.Equal(got, c.want) {
			t.Errorf("width %d: %v, want %v", c.width, got, c.want)
		}
	}
	if got := layout([]col{{"A", 3, 0}}, 80); !slices.Equal(got, []int{3}) {
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
	out := m.table(cols, 50, cur, 10, cell, "empty")
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
	lines = strings.Split(plainShell(m.table(cols, 50, cur, 10, cell, "")), "\n")
	if cur.offset != 5 || !strings.Contains(lines[1], "item-05") {
		t.Errorf("scrolling up puts the cursor on top: offset %d %q", cur.offset, lines[1])
	}
	cur.pos = 99
	m.table(cols, 50, cur, 10, cell, "")
	if cur.pos != 49 || cur.offset != 41 {
		t.Errorf("cursor clamps to the last row: %+v", *cur)
	}
	cur.pos, cur.offset = 0, 40
	m.table(cols, 3, cur, 10, cell, "")
	if cur.offset != 0 {
		t.Errorf("a list that shrank resets the offset: %+v", *cur)
	}
	if got := m.table(cols, 5, cur, 0, cell, ""); got != "" {
		t.Errorf("no room renders nothing: %q", got)
	}
	empty := plainShell(m.table(cols, 0, cur, 5, cell, "nothing here"))
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
	lines := strings.Split(plainShell(m.table(cols, 2, &cursor{}, 3, cell, "")), "\n")
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

func TestCursorClampShell(t *testing.T) {
	for _, c := range []struct{ pos, n, want int }{{5, 3, 2}, {-1, 3, 0}, {1, 3, 1}, {4, 0, 0}} {
		cur := cursor{pos: c.pos}
		cur.clamp(c.n)
		if cur.pos != c.want {
			t.Errorf("clamp(%d) from %d: %d, want %d", c.n, c.pos, cur.pos, c.want)
		}
	}
}

package ui

import (
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type choice struct {
	label  string
	hint   string
	search string
	value  any
}

// chooser is the one modal picker: repos, transitions, tickets, sessions.
type chooser struct {
	title  string
	in     textinput.Model
	all    []choice
	shown  []choice
	cur    cursor
	pick   func(*Model, choice) (tea.Model, tea.Cmd)
	tab    func(*Model, choice) // optional: tab cycles an option, e.g. the profile
	status func(*Model, choice) string
	extra  func(query string) []choice // optional: rows always listed last, e.g. "+ new repo"
}

func newChooser(title string, items []choice, pick func(*Model, choice) (tea.Model, tea.Cmd)) *chooser {
	in := textinput.New()
	in.Prompt = title + " › "
	in.PromptStyle = sKey
	in.TextStyle = sText
	in.Focus()
	c := &chooser{title: title, in: in, all: items, pick: pick}
	c.filter()
	return c
}

func (c *chooser) setQuery(q string) {
	c.in.SetValue(q)
	c.in.CursorEnd()
	c.cur = cursor{}
	c.filter()
}

// filter ranks by match quality, keeping the given order within a rank: exact
// name, name prefix, name contains, path contains, then fuzzy.
func (c *chooser) filter() {
	q := strings.ToLower(c.in.Value())
	buckets := make([][]choice, 5)
	for _, it := range c.all {
		s := strings.ToLower(it.search)
		if s == "" {
			s = strings.ToLower(it.label)
		}
		base := filepath.Base(s)
		rank := -1
		switch {
		case q == "" || base == q:
			rank = 0
		case strings.HasPrefix(base, q):
			rank = 1
		case strings.Contains(base, q):
			rank = 2
		case strings.Contains(s, q):
			rank = 3
		case fuzzy(s, q):
			rank = 4
		}
		if rank >= 0 {
			buckets[rank] = append(buckets[rank], it)
		}
	}
	c.shown = c.shown[:0]
	for _, b := range buckets {
		c.shown = append(c.shown, b...)
	}
	if c.extra != nil {
		c.shown = append(c.shown, c.extra(c.in.Value())...)
	}
	c.cur.clamp(len(c.shown))
}

func (c *chooser) selected() (choice, bool) {
	if c.cur.pos < 0 || c.cur.pos >= len(c.shown) {
		return choice{}, false
	}
	return c.shown[c.cur.pos], true
}

func (m *Model) updateChooser(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	c := m.chooser
	switch msg.String() {
	case "esc", "ctrl+c":
		m.chooser = nil
		return m, nil
	case "up", "ctrl+p", "ctrl+k":
		c.cur.pos = max(0, c.cur.pos-1)
		return m, nil
	case "down", "ctrl+n", "ctrl+j":
		c.cur.pos = min(len(c.shown)-1, c.cur.pos+1)
		return m, nil
	case "tab":
		if it, ok := c.selected(); ok && c.tab != nil {
			c.tab(m, it)
		}
		return m, nil
	case "enter":
		it, ok := c.selected()
		m.chooser = nil
		if !ok {
			return m, nil
		}
		return c.pick(m, it)
	}
	var cmd tea.Cmd
	before := c.in.Value()
	c.in, cmd = c.in.Update(msg)
	if c.in.Value() != before {
		c.cur = cursor{}
		c.filter()
	}
	return m, cmd
}

func (m *Model) viewChooser(h int) string {
	c := m.chooser
	var b strings.Builder
	b.WriteString(" " + c.in.View() + "\n\n")
	rows := h - 2
	if c.cur.pos >= c.cur.offset+rows {
		c.cur.offset = c.cur.pos - rows + 1
	}
	if c.cur.pos < c.cur.offset {
		c.cur.offset = c.cur.pos
	}
	for i := c.cur.offset; i < min(len(c.shown), c.cur.offset+rows); i++ {
		it := c.shown[i]
		line := it.label
		if it.hint != "" {
			line += "  " + sDim.Render(it.hint)
		}
		if i == c.cur.pos {
			b.WriteString(sBar.Render(" ▌ ") + highlight(fit(line, m.width-4)) + "\n")
		} else {
			b.WriteString("   " + fit(line, m.width-4) + "\n")
		}
	}
	if len(c.all) == 0 {
		b.WriteString(sDim.Render("   nothing to choose from"))
	}
	return lipgloss.NewStyle().Height(h).MaxHeight(h).Render(b.String())
}

func fuzzy(s, q string) bool {
	i := 0
	for _, c := range s {
		if i < len(q) && rune(q[i]) == c {
			i++
		}
	}
	return i == len(q)
}

func pathChoice(dir, root, hint string, value any) choice {
	rel := strings.TrimPrefix(dir, root+"/")
	d, base := filepath.Split(rel)
	return choice{label: sDim.Render(d) + sBold.Render(base), hint: hint, search: rel, value: value}
}

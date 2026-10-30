package ui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/MartyJRE/claudia/internal/atlassian"
)

// The Jira view as a tree: sprint › epic › story › subtask, following the
// parent links Jira returns. A parent that isn't in the list itself (an epic
// someone else owns) still heads its children, drawn dimmed.

const (
	groupSprint = iota // sprint › epic › …
	groupEpic          // epic › … without sprints
	groupFlat          // the plain list
	nGroups
)

var groupNames = [nGroups]string{"sprint › epic", "epic", "flat"}

type jrow struct {
	sprint    string // header row: the sprint's name
	state     string // the sprint's state
	issue     atlassian.Issue
	depth     int
	context   bool // parent shown only to hold its children
	kids      bool
	collapsed bool
	count     int // tickets underneath (headers and parents)
}

func (r jrow) header() bool { return r.sprint != "" }

func (r jrow) foldKey() string {
	if r.header() {
		return "sprint:" + r.sprint
	}
	return r.issue.Key
}

type jnode struct {
	issue   atlassian.Issue
	context bool
	kids    []*jnode
}

func (n *jnode) size() int {
	c := 0
	if !n.context {
		c = 1
	}
	for _, k := range n.kids {
		c += k.size()
	}
	return c
}

// buildJira turns the filtered issues into visible rows. While a filter is
// typed, everything is unfolded so matches are never hidden.
func (m *Model) buildJira(issues []atlassian.Issue, filtering bool) []jrow {
	less := m.issueOrder()
	if m.jiraPrefs.Group == groupFlat {
		slices.SortStableFunc(issues, less)
		var rows []jrow
		for _, is := range issues {
			rows = append(rows, jrow{issue: is})
		}
		return rows
	}

	type group struct {
		name, state string
		id          int
		issues      []atlassian.Issue
	}
	var groups []*group
	byName := map[string]*group{}
	known := map[string]atlassian.Issue{}
	for _, is := range issues {
		known[is.Key] = is
	}
	add := func(name, state string, id int, is atlassian.Issue) {
		g := byName[name]
		if g == nil {
			g = &group{name: name, state: state, id: id}
			byName[name] = g
			groups = append(groups, g)
		}
		g.issues = append(g.issues, is)
	}
	// An epic without a sprint only heads its children where they are, rather
	// than also showing up alone under "No sprint".
	hasKids := map[string]bool{}
	for _, is := range issues {
		hasKids[is.Parent] = true
	}
	for _, is := range issues {
		switch {
		case m.jiraPrefs.Group == groupEpic:
			add("all", "", 0, is)
		case is.Sprint != nil:
			add(is.Sprint.Name, is.Sprint.State, is.Sprint.ID, is)
		case is.Level >= 1 && hasKids[is.Key]:
		default:
			add("No sprint", "", 1<<30, is)
		}
	}
	rank := map[string]int{"active": 0, "future": 1, "": 2, "closed": 3}
	slices.SortStableFunc(groups, func(a, b *group) int {
		if d := rank[a.state] - rank[b.state]; d != 0 {
			return d
		}
		return a.id - b.id
	})

	var rows []jrow
	for _, g := range groups {
		roots := forest(g.issues, known)
		sortForest(roots, less)
		depth := 0
		if m.jiraPrefs.Group == groupSprint {
			total := 0
			for _, r := range roots {
				total += r.size()
			}
			h := jrow{sprint: g.name, state: g.state, kids: true, count: total}
			h.collapsed = !filtering && m.folded(h.foldKey())
			rows = append(rows, h)
			if h.collapsed {
				continue
			}
			depth = 1
		}
		for _, r := range roots {
			rows = m.flatten(rows, r, depth, filtering)
		}
	}
	return rows
}

// forest links issues to their parents. A missing parent becomes a context
// node built from the child's parent fields, or from the issue itself when it
// is in the list but outside this group (an epic without a sprint).
func forest(issues []atlassian.Issue, known map[string]atlassian.Issue) []*jnode {
	nodes := map[string]*jnode{}
	for _, is := range issues {
		nodes[is.Key] = &jnode{issue: is}
	}
	var roots []*jnode
	for _, is := range issues {
		n := nodes[is.Key]
		if is.Parent == "" {
			roots = append(roots, n)
			continue
		}
		p := nodes[is.Parent]
		if p == nil {
			pi, ok := known[is.Parent]
			if !ok {
				pi = atlassian.Issue{Key: is.Parent, Summary: is.ParentSummary, Type: is.ParentType,
					Status: is.ParentStatus, Category: is.ParentCategory, Level: is.Level + 1}
			}
			p = &jnode{issue: pi, context: !ok}
			nodes[is.Parent] = p
			roots = append(roots, p)
		}
		p.kids = append(p.kids, n)
	}
	return roots
}

func sortForest(ns []*jnode, less func(a, b atlassian.Issue) int) {
	slices.SortStableFunc(ns, func(a, b *jnode) int { return less(a.issue, b.issue) })
	for _, n := range ns {
		sortForest(n.kids, less)
	}
}

func (m *Model) flatten(rows []jrow, n *jnode, depth int, filtering bool) []jrow {
	r := jrow{issue: n.issue, depth: depth, context: n.context, kids: len(n.kids) > 0, count: n.size()}
	r.collapsed = r.kids && !filtering && m.folded(r.foldKey())
	rows = append(rows, r)
	if r.collapsed {
		return rows
	}
	for _, k := range n.kids {
		rows = m.flatten(rows, k, depth+1, filtering)
	}
	return rows
}

func (m *Model) folded(key string) bool { return slices.Contains(m.jiraPrefs.Collapsed, key) }

func (m *Model) setFolded(key string, fold bool) {
	i := slices.Index(m.jiraPrefs.Collapsed, key)
	switch {
	case fold && i < 0:
		m.jiraPrefs.Collapsed = append(m.jiraPrefs.Collapsed, key)
	case !fold && i >= 0:
		m.jiraPrefs.Collapsed = slices.Delete(m.jiraPrefs.Collapsed, i, i+1)
	}
}

// foldAll collapses every sprint and parent, or opens everything.
func (m *Model) foldAll(fold bool) {
	m.jiraPrefs.Collapsed = nil
	if fold {
		for _, r := range m.buildJira(m.jiraIssues(func(...string) bool { return true }), true) {
			if r.kids {
				m.jiraPrefs.Collapsed = append(m.jiraPrefs.Collapsed, r.foldKey())
			}
		}
	}
	m.saveJiraPrefs()
	m.refilter()
}

// toggleFold folds the row under the cursor; on a leaf it folds its parent.
func (m *Model) toggleFold() {
	c := m.cur[vJira].pos
	if c < 0 || c >= len(m.vis.jira) {
		return
	}
	r := m.vis.jira[c]
	if !r.kids {
		for i := c - 1; i >= 0; i-- {
			if m.vis.jira[i].depth < r.depth && m.vis.jira[i].kids {
				m.cur[vJira].pos = i
				m.setFolded(m.vis.jira[i].foldKey(), true)
				break
			}
		}
	} else {
		m.setFolded(r.foldKey(), !r.collapsed)
	}
	m.saveJiraPrefs()
	m.refilter()
}

// unfoldTo opens the sprint and every parent above an issue.
func (m *Model) unfoldTo(key string) {
	src := m.jira.Sprint
	if m.teamJira {
		src = m.jira.Team
	}
	byKey := map[string]atlassian.Issue{}
	for _, is := range src {
		byKey[is.Key] = is
	}
	is, ok := byKey[key]
	if !ok {
		return
	}
	if is.Sprint != nil {
		m.setFolded("sprint:"+is.Sprint.Name, false)
	}
	m.setFolded("sprint:No sprint", false)
	for p := is.Parent; p != ""; p = byKey[p].Parent {
		m.setFolded(p, false)
		if _, ok := byKey[p]; !ok {
			break
		}
	}
}

func (m *Model) jiraKeyCell(r jrow) string {
	indent := strings.Repeat("  ", r.depth)
	glyph := "  "
	if r.kids {
		glyph = "▾ "
		if r.collapsed {
			glyph = "▸ "
		}
	}
	if r.header() {
		return sAccent.Render(glyph)
	}
	key := sTicket.Render(r.issue.Key)
	if r.context {
		key = sDim.Render(r.issue.Key)
	}
	return sDim.Render(indent+glyph) + key
}

func (m *Model) jiraSummaryCell(r jrow, sel bool) string {
	if r.header() {
		s := sBold.Render(r.sprint)
		switch r.state {
		case "active":
			s += " " + sOK.Render("active")
		case "future":
			s += " " + sDim.Render("upcoming")
		}
		return s + sDim.Render(fmt.Sprintf("  %d tickets", r.count))
	}
	is := r.issue
	st := sText
	switch {
	case r.context:
		st = sDim
	case is.Level >= 1:
		st = lipgloss.NewStyle().Foreground(purple).Bold(true)
	case sel:
		st = sBold
	case is.Category == "done":
		st = sDim
	}
	s := st.Render(is.Summary)
	if r.kids && r.collapsed {
		s += sDim.Render(fmt.Sprintf("  (%d)", r.count-1+boolInt(r.context)))
	}
	return s
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

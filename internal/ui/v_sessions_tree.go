package ui

import (
	"cmp"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/MartyJRE/claudia/internal/state"
)

// Sessions as a tree: grouped by repository, or by epic › ticket, or flat.

const (
	sessByRepo = iota
	sessByTicket
	sessFlat
	nSessGroups
)

var sessGroupNames = [nSessGroups]string{"by repo", "by epic › ticket", "flat"}

type srow struct {
	isSess    bool
	sess      row
	key       string // fold key for headers
	title     string // header: repo name or ticket summary
	ticket    string // header: ticket key, when grouped by ticket
	via       string // header: the epic whose only ticket this is, its own header left out
	single    bool   // a session standing in for its one-session group
	group     string // single: the group's name, shown on the row instead of a header
	depth     int
	collapsed bool
	n, live   int
	waiting   int
	done      int
}

// id identifies the row across rebuilds, so the cursor can stay on it.
func (r srow) id() string {
	if r.isSess {
		return "s:" + r.sess.ID
	}
	return "h:" + r.key
}

type sessionPrefs struct {
	Group     int      `json:"group"`
	Collapsed []string `json:"collapsed"`
}

const sessionPrefsFile = "sessions-prefs.json"

func loadSessionPrefs() sessionPrefs {
	var p sessionPrefs
	state.Load(sessionPrefsFile, &p)
	return p
}

func (m *Model) saveSessionPrefs() { _ = state.Save(sessionPrefsFile, m.sessPrefs) }

// repoOf names the repository a session runs in: the deepest known repo that
// contains its directory, else the directory's own name.
func (m *Model) repoOf(dir string) string {
	best := ""
	for _, r := range m.repos {
		if (dir == r.Dir || strings.HasPrefix(dir, r.Dir+"/")) && len(r.Dir) > len(best) {
			best = r.Dir
		}
	}
	if best == "" {
		return project(dir)
	}
	return filepath.Base(best)
}

type sgroup struct {
	key, title, ticket string
	sessions           []row
	subs               []*sgroup
	last               time.Time
}

type gstats struct {
	n, live, waiting, done int
	last                   time.Time
}

// name labels the group on a row that stands in for it; "No ticket" says
// nothing the empty TICKET column doesn't.
func (g *sgroup) name() string {
	if g.key == "ticket:-" {
		return ""
	}
	return cmp.Or(g.title, g.ticket)
}

// only is the session of a group that holds just one, and the names of the
// groups down to it.
func (g *sgroup) only() (row, string) {
	if len(g.sessions) > 0 {
		return g.sessions[0], g.name()
	}
	r, below := g.subs[0].only()
	return r, path(g.name(), below)
}

func path(names ...string) string {
	return strings.Join(slices.DeleteFunc(names, func(s string) bool { return s == "" }), " › ")
}

func (g *sgroup) stats() gstats {
	var out gstats
	for _, r := range g.sessions {
		out.n++
		if r.live() {
			out.live++
		}
		switch {
		case r.needsYou():
			out.waiting++
		case r.done():
			out.done++
		}
		if t := r.LastActive(); t.After(out.last) {
			out.last = t
		}
	}
	for _, s := range g.subs {
		st := s.stats()
		out.n, out.live, out.waiting, out.done = out.n+st.n, out.live+st.live, out.waiting+st.waiting, out.done+st.done
		if st.last.After(out.last) {
			out.last = st.last
		}
	}
	return out
}

// started is the newest start among the group's live sessions.
func (g *sgroup) started() time.Time {
	var t time.Time
	for _, r := range g.sessions {
		if r.live() && r.Started().After(t) {
			t = r.Started()
		}
	}
	for _, s := range g.subs {
		if st := s.started(); st.After(t) {
			t = st
		}
	}
	return t
}

// sortGroups: groups with live sessions first, by when their newest one
// started, so activity doesn't reshuffle them; the rest by last activity.
func sortGroups(gs []*sgroup) {
	slices.SortStableFunc(gs, func(a, b *sgroup) int {
		as, bs := a.stats(), b.stats()
		al, at, bl, bt := as.live, as.last, bs.live, bs.last
		switch {
		case (al > 0) != (bl > 0):
			return boolCmp(bl > 0, al > 0)
		case al > 0:
			return b.started().Compare(a.started())
		}
		return bt.Compare(at)
	})
	for _, g := range gs {
		sortGroups(g.subs)
	}
}

func boolCmp(a, b bool) int {
	if a == b {
		return 0
	}
	if a {
		return 1
	}
	return -1
}

const recentClosed = 8

// sessionRows is the Sessions tab: the tree, and unless closed sessions are
// shown anyway, the few closed most recently, to revive with enter.
func (m *Model) sessionRows(match func(...string) bool, filtering bool) []srow {
	return append(m.buildSessions(m.filterSessions(match), filtering), m.recentClosed(match, filtering)...)
}

func (m *Model) recentClosed(match func(...string) bool, filtering bool) []srow {
	if m.allSessions {
		return nil
	}
	var closed []row
	for _, r := range m.sessions {
		if !r.live() && r.Dir() != "" && match(r.Name(), project(r.Dir()), branch(r), m.ix.sessionTicket[r.ID], r.Profile.Name) {
			closed = append(closed, r)
		}
	}
	if len(closed) == 0 {
		return nil
	}
	slices.SortStableFunc(closed, func(a, b row) int { return b.LastActive().Compare(a.LastActive()) })
	closed = closed[:min(len(closed), recentClosed)]
	h := srow{key: "recent", title: "Recently closed", n: len(closed)}
	h.collapsed = !filtering && slices.Contains(m.sessPrefs.Collapsed, h.key)
	out := []srow{h}
	if !h.collapsed {
		for _, r := range closed {
			out = append(out, srow{isSess: true, sess: r, depth: 1})
		}
	}
	return out
}

func (m *Model) buildSessions(sessions []row, filtering bool) []srow {
	if m.sessPrefs.Group == sessFlat {
		var out []srow
		for _, r := range sessions {
			out = append(out, srow{isSess: true, sess: r})
		}
		return out
	}
	var roots []*sgroup
	index := map[string]*sgroup{}
	group := func(parent *[]*sgroup, key, title, ticket string) *sgroup {
		if g := index[key]; g != nil {
			return g
		}
		g := &sgroup{key: key, title: title, ticket: ticket}
		index[key] = g
		*parent = append(*parent, g)
		return g
	}
	for _, r := range sessions {
		if m.sessPrefs.Group == sessByRepo {
			name := m.repoOf(r.Dir())
			g := group(&roots, "repo:"+name, name, "")
			g.sessions = append(g.sessions, r)
			continue
		}
		key := m.ix.sessionTicket[r.ID]
		if key == "" {
			g := group(&roots, "ticket:-", "No ticket", "")
			g.sessions = append(g.sessions, r)
			continue
		}
		is := m.ix.issues[key]
		// An epic's own sessions sit directly under the epic.
		if is.Level >= 1 {
			g := group(&roots, "ticket:"+key, is.Summary, key)
			g.sessions = append(g.sessions, r)
			continue
		}
		parent := &roots
		if is.Parent != "" {
			epic := m.ix.issues[is.Parent]
			title := epic.Summary
			if title == "" {
				title = is.ParentSummary
			}
			parent = &group(&roots, "ticket:"+is.Parent, title, is.Parent).subs
		}
		g := group(parent, "ticket:"+key, is.Summary, key)
		g.sessions = append(g.sessions, r)
	}
	sortGroups(roots)

	// A group of one session is that session's row, with the group named on
	// it; an epic with one ticket leaves its header out and names itself on
	// the ticket's.
	var out []srow
	var walk func(gs []*sgroup, depth int, via string)
	walk = func(gs []*sgroup, depth int, via string) {
		for _, g := range gs {
			st := g.stats()
			switch {
			case st.n == 1:
				r, name := g.only()
				out = append(out, srow{isSess: true, sess: r, depth: depth, single: true, group: path(via, name)})
				continue
			case len(g.sessions) == 0 && len(g.subs) == 1:
				walk(g.subs, depth, path(via, g.name()))
				continue
			}
			h := srow{key: g.key, title: g.title, ticket: g.ticket, via: via, depth: depth, n: st.n, live: st.live, waiting: st.waiting, done: st.done}
			h.collapsed = !filtering && slices.Contains(m.sessPrefs.Collapsed, g.key)
			out = append(out, h)
			if h.collapsed {
				continue
			}
			walk(g.subs, depth+1, "")
			for _, r := range g.sessions {
				out = append(out, srow{isSess: true, sess: r, depth: depth + 1})
			}
		}
	}
	walk(roots, 0, "")
	return out
}

func (m *Model) sessionHeaderCells(r srow) []string {
	glyph := "▾ "
	if r.collapsed {
		glyph = "▸ "
	}
	indent := strings.Repeat("  ", r.depth)
	title := sBold.Render(r.title)
	if r.ticket != "" {
		title = sTicket.Render(r.ticket) + " " + sBold.Render(r.title)
	}
	if r.via != "" {
		title = sDim.Render(r.via+" › ") + title
	}
	counts := sDim.Render(fmt.Sprintf("  %d session(s)", r.n))
	if r.live > 0 {
		counts += sDim.Render(" · ") + sOK.Render(fmt.Sprintf("%d live", r.live))
	}
	if r.waiting > 0 {
		counts += sDim.Render(" · ") + lipgloss.NewStyle().Foreground(yellow).Bold(true).Render(fmt.Sprintf("%d needs you", r.waiting))
	}
	if r.done > 0 {
		counts += sDim.Render(" · ") + sOK.Render(fmt.Sprintf("%d done", r.done))
	}
	// Spans the row, starting where the SESSION column starts.
	return []string{strings.Repeat(" ", sessionCols[0].w+1) + sDim.Render(indent) + sAccent.Render(glyph) + title + counts}
}

func (m *Model) toggleSessionFold() {
	c := m.cur[vSessions].pos
	if c < 0 || c >= len(m.vis.sessions) {
		return
	}
	r := m.vis.sessions[c]
	key := r.key
	if r.isSess {
		// On a session, fold the group it sits in.
		for i := c - 1; i >= 0; i-- {
			if h := m.vis.sessions[i]; !h.isSess && h.depth < r.depth {
				key, m.cur[vSessions].pos = h.key, i
				break
			}
		}
	}
	if key == "" {
		return
	}
	if i := slices.Index(m.sessPrefs.Collapsed, key); i >= 0 {
		m.sessPrefs.Collapsed = slices.Delete(m.sessPrefs.Collapsed, i, i+1)
	} else {
		m.sessPrefs.Collapsed = append(m.sessPrefs.Collapsed, key)
	}
	m.saveSessionPrefs()
	m.refilter()
}

func (m *Model) foldAllSessions(fold bool) {
	m.sessPrefs.Collapsed = nil
	if fold {
		for _, r := range m.sessionRows(func(...string) bool { return true }, true) {
			if !r.isSess {
				m.sessPrefs.Collapsed = append(m.sessPrefs.Collapsed, r.key)
			}
		}
	}
	m.saveSessionPrefs()
	m.refilter()
}

// sessionIndent lines a session up under its group's name, one level deeper;
// one standing in for its group lines up with the group names beside it.
func sessionIndent(r srow) string {
	if r.depth == 0 && !r.single {
		return ""
	}
	return strings.Repeat("  ", r.depth) + "  "
}

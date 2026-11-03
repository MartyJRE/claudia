package ui

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/MartyJRE/claudia/internal/claude"
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
	depth     int
	collapsed bool
	n, live   int
	waiting   int
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

func (g *sgroup) stats() (n, live, waiting int, last time.Time) {
	for _, r := range g.sessions {
		n++
		if r.live() {
			live++
			if r.Live.Status == claude.StatusWaiting {
				waiting++
			}
		}
		if t := r.LastActive(); t.After(last) {
			last = t
		}
	}
	for _, s := range g.subs {
		sn, sl, sw, st := s.stats()
		n, live, waiting = n+sn, live+sl, waiting+sw
		if st.After(last) {
			last = st
		}
	}
	return
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
		_, al, _, at := a.stats()
		_, bl, _, bt := b.stats()
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

	var out []srow
	var walk func(gs []*sgroup, depth int)
	walk = func(gs []*sgroup, depth int) {
		for _, g := range gs {
			n, live, waiting, _ := g.stats()
			h := srow{key: g.key, title: g.title, ticket: g.ticket, depth: depth, n: n, live: live, waiting: waiting}
			h.collapsed = !filtering && slices.Contains(m.sessPrefs.Collapsed, g.key)
			out = append(out, h)
			if h.collapsed {
				continue
			}
			walk(g.subs, depth+1)
			for _, r := range g.sessions {
				out = append(out, srow{isSess: true, sess: r, depth: depth + 1})
			}
		}
	}
	walk(roots, 0)
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
	counts := sDim.Render(fmt.Sprintf("  %d session(s)", r.n))
	if r.live > 0 {
		counts += sDim.Render(" · ") + sOK.Render(fmt.Sprintf("%d live", r.live))
	}
	if r.waiting > 0 {
		counts += sDim.Render(" · ") + lipgloss.NewStyle().Foreground(yellow).Bold(true).Render(fmt.Sprintf("%d needs you", r.waiting))
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
		for _, r := range m.buildSessions(m.filterSessions(func(...string) bool { return true }), true) {
			if !r.isSess {
				m.sessPrefs.Collapsed = append(m.sessPrefs.Collapsed, r.key)
			}
		}
	}
	m.saveSessionPrefs()
	m.refilter()
}

// sessionIndent lines a session up under its group's name, one level deeper.
func sessionIndent(r srow) string {
	if r.depth == 0 {
		return ""
	}
	return strings.Repeat("  ", r.depth) + "  "
}

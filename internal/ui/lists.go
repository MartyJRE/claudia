package ui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/MartyJRE/claudia/internal/state"
)

// Inbox, the MR views, Mentions and Confluence switch between the plain list
// and grouped layouts, whose headers fold like Jira's. A group of one is just
// its item's row, so there is no header to scroll past.

type lgroup struct {
	key, label string
	ticket     string // the label is this ticket's summary
	rank       int    // groups sort by rank, then by first appearance
}

type listLayout struct {
	name  string
	group func(m *Model, i int) lgroup // nil: the plain list
	// inline: no column shows the group, so an item alone in one names it.
	inline bool
}

type listSpec struct {
	noun    string
	layouts []listLayout
}

var listSpecs = map[view]listSpec{
	vInbox:      {"items", []listLayout{{name: "oldest first"}, {name: "by kind", group: inboxKindGroup}}},
	vReview:     {"MRs", []listLayout{{name: "flat"}, {name: "by repo", group: mrRepoGroup}}},
	vMine:       {"MRs", []listLayout{{name: "flat"}, {name: "by repo", group: mrRepoGroup}, {name: "by state", group: mrStateGroup, inline: true}}},
	vMentions:   {"mentions", []listLayout{{name: "flat"}, {name: "by ticket/space", group: mentionGroup}}},
	vConfluence: {"pages", []listLayout{{name: "flat"}, {name: "by space", group: pageGroup}}},
}

func inboxKindGroup(m *Model, i int) lgroup {
	k := m.vis.inbox[i].kind
	return lgroup{key: "kind:" + kindLabel[k], label: kindLabel[k], rank: int(k)}
}

func mrRepoGroup(m *Model, i int) lgroup {
	p := m.vis.mrs[i].Project()
	return lgroup{key: "repo:" + p, label: p}
}

// mrStates is the order Mine's states are listed in: what needs you first.
var mrStates = []string{"pipeline failed", "open threads", "needs rebase", "in review", "approved", "draft", "merged"}

func mrStateGroup(m *Model, i int) lgroup {
	r := m.vis.mrs[i]
	s := "in review"
	switch {
	case r.merged:
		s = "merged"
	case r.Draft:
		s = "draft"
	case r.PipelineStatus() == "failed":
		s = "pipeline failed"
	case r.OpenThreads() > 0:
		s = "open threads"
	case r.MergeStatus == "need_rebase" || r.MergeStatus == "conflict":
		s = "needs rebase"
	case r.Approved:
		s = "approved"
	}
	return lgroup{key: "state:" + s, label: s, rank: slices.Index(mrStates, s)}
}

func mentionGroup(m *Model, i int) lgroup {
	x := m.vis.mentions[i]
	if x.jira {
		return lgroup{key: "where:" + x.where, label: x.title, ticket: x.where}
	}
	return lgroup{key: "where:" + x.where, label: x.where}
}

func pageGroup(m *Model, i int) lgroup {
	s := m.vis.pages[i].Space
	return lgroup{key: "space:" + s, label: s}
}

// lrow is a row of a groupable list: a group header, or one of the view's items.
type lrow struct {
	item      int // index into the view's items; -1 on a header
	group     lgroup
	n         int
	collapsed bool
	nested    bool // drawn under its group's header
	lone      bool // its group's only item, drawn in the header's place
}

func (r lrow) header() bool { return r.item < 0 }

type listPrefs struct {
	Layout    map[string]int `json:"layout"`
	Collapsed []string       `json:"collapsed"`
}

const listPrefsFile = "list-prefs.json"

func loadListPrefs() listPrefs {
	var p listPrefs
	state.Load(listPrefsFile, &p)
	return p
}

func (m *Model) saveListPrefs() { _ = state.Save(listPrefsFile, m.listPrefs) }

func listKey(v view) string { return strings.ReplaceAll(strings.ToLower(viewNames[v]), " ", "-") }

func listFoldKey(v view, g lgroup) string { return listKey(v) + ":" + g.key }

func (m *Model) layoutIndex(v view) int {
	if i := m.listPrefs.Layout[listKey(v)]; i > 0 && i < len(listSpecs[v].layouts) {
		return i
	}
	return 0
}

func (m *Model) listLayout(v view) listLayout {
	if ls := listSpecs[v].layouts; len(ls) > 0 {
		return ls[m.layoutIndex(v)]
	}
	return listLayout{}
}

func (m *Model) listRows(v view) []lrow {
	switch v {
	case vInbox:
		return m.vis.inboxRows
	case vReview, vMine:
		return m.vis.mrRows
	case vMentions:
		return m.vis.mentionRows
	case vConfluence:
		return m.vis.pageRows
	}
	return nil
}

// groupRows lays the view's n items out in its layout. While a filter is
// typed, every group is open so matches are never hidden.
func (m *Model) groupRows(v view, n int, filtering bool) []lrow {
	rows := []lrow{}
	group := m.listLayout(v).group
	if group == nil {
		for i := range n {
			rows = append(rows, lrow{item: i})
		}
		return rows
	}
	type bucket struct {
		g     lgroup
		items []int
	}
	var bs []*bucket
	byKey := map[string]*bucket{}
	for i := range n {
		g := group(m, i)
		b := byKey[g.key]
		if b == nil {
			b = &bucket{g: g}
			byKey[g.key] = b
			bs = append(bs, b)
		}
		b.items = append(b.items, i)
	}
	slices.SortStableFunc(bs, func(a, b *bucket) int { return a.g.rank - b.g.rank })
	for _, b := range bs {
		if len(b.items) == 1 {
			rows = append(rows, lrow{item: b.items[0], group: b.g, lone: true})
			continue
		}
		h := lrow{item: -1, group: b.g, n: len(b.items)}
		h.collapsed = !filtering && slices.Contains(m.listPrefs.Collapsed, listFoldKey(v, b.g))
		rows = append(rows, h)
		if h.collapsed {
			continue
		}
		for _, i := range b.items {
			rows = append(rows, lrow{item: i, group: b.g, nested: true})
		}
	}
	return rows
}

// listItem is the index of the item under the cursor, if it's on one.
func (m *Model) listItem(v view) (int, bool) {
	rows := m.listRows(v)
	c := m.cur[v].pos
	if c < 0 || c >= len(rows) || rows[c].header() {
		return -1, false
	}
	return rows[c].item, true
}

func (m *Model) currentGroup(v view) (lrow, bool) {
	rows := m.listRows(v)
	c := m.cur[v].pos
	if c < 0 || c >= len(rows) || !rows[c].header() {
		return lrow{}, false
	}
	return rows[c], true
}

// keyList handles the layout and fold keys every groupable list shares.
func (m *Model) keyList(act string) bool {
	v := m.view
	switch strings.TrimPrefix(act, viewScope(v)+".") {
	case "layout":
		m.cycleLayout(v)
	case "fold":
		m.toggleListFold(v)
	case "fold_all":
		m.foldAllList(v, true)
	case "unfold_all":
		m.foldAllList(v, false)
	default:
		return false
	}
	return true
}

// cycleLayout moves to the next layout, keeping the cursor on its item.
func (m *Model) cycleLayout(v view) {
	item, ok := m.listItem(v)
	if m.listPrefs.Layout == nil {
		m.listPrefs.Layout = map[string]int{}
	}
	m.listPrefs.Layout[listKey(v)] = (m.layoutIndex(v) + 1) % len(listSpecs[v].layouts)
	m.saveListPrefs()
	m.refilter()
	if ok {
		m.cursorToItem(v, item)
	}
	m.setFlash("layout: "+m.listLayout(v).name, false)
}

// cursorToItem puts the cursor on the item, or on its folded group's header.
func (m *Model) cursorToItem(v view, item int) {
	rows := m.listRows(v)
	if i := slices.IndexFunc(rows, func(r lrow) bool { return r.item == item }); i >= 0 {
		m.cur[v].pos = i
		return
	}
	g := m.listLayout(v).group(m, item)
	m.cur[v].pos = slices.IndexFunc(rows, func(r lrow) bool { return r.header() && r.group.key == g.key })
}

// toggleListFold folds the header under the cursor; on an item it folds the
// item's group and moves to its header.
func (m *Model) toggleListFold(v view) {
	rows := m.listRows(v)
	c := m.cur[v].pos
	if c < 0 || c >= len(rows) || rows[c].lone || rows[c].group.key == "" {
		return
	}
	r := rows[c]
	k := listFoldKey(v, r.group)
	if i := slices.Index(m.listPrefs.Collapsed, k); i >= 0 {
		m.listPrefs.Collapsed = slices.Delete(m.listPrefs.Collapsed, i, i+1)
	} else {
		m.listPrefs.Collapsed = append(m.listPrefs.Collapsed, k)
	}
	m.saveListPrefs()
	m.refilter()
	m.cur[v].pos = slices.IndexFunc(m.listRows(v), func(h lrow) bool { return h.header() && h.group.key == r.group.key })
}

func (m *Model) foldAllList(v view, fold bool) {
	prefix := listKey(v) + ":"
	m.listPrefs.Collapsed = slices.DeleteFunc(m.listPrefs.Collapsed, func(k string) bool { return strings.HasPrefix(k, prefix) })
	if fold {
		for _, r := range m.groupRows(v, m.listLen(v), true) {
			if r.header() {
				m.listPrefs.Collapsed = append(m.listPrefs.Collapsed, listFoldKey(v, r.group))
			}
		}
	}
	m.saveListPrefs()
	m.refilter()
}

func (m *Model) listLen(v view) int {
	switch v {
	case vInbox:
		return len(m.vis.inbox)
	case vReview, vMine:
		return len(m.vis.mrs)
	case vMentions:
		return len(m.vis.mentions)
	}
	return len(m.vis.pages)
}

// listHeader is a group's header row, spanning the table from pad columns in.
func (m *Model) listHeader(v view, r lrow, pad int) []string {
	glyph := "▾ "
	if r.collapsed {
		glyph = "▸ "
	}
	label := sBold.Render(r.group.label)
	if r.group.ticket != "" {
		label = sTicket.Render(r.group.ticket) + " " + label
	}
	return []string{strings.Repeat(" ", pad) + sAccent.Render(glyph) + label + sDim.Render(fmt.Sprintf("  %d %s", r.n, listSpecs[v].noun))}
}

// listIndent lines an item up under its group's name.
func listIndent(r lrow) string {
	if r.nested {
		return "  "
	}
	return ""
}

// listInline names the group of an item standing in for it, where no column does.
func (m *Model) listInline(v view, r lrow) string {
	if r.lone && m.listLayout(v).inline {
		return sDim.Render(r.group.label + " › ")
	}
	return ""
}

func (m *Model) detailGroup(v view, r lrow) string {
	sc := viewScope(v)
	label := sBold.Render(r.group.label)
	if r.group.ticket != "" {
		label = sTicket.Render(r.group.ticket) + " " + label
	}
	return m.panel([]string{label, sDim.Render(fmt.Sprintf("%d %s · %s folds · %s folds all · %s opens all · %s switches layout",
		r.n, listSpecs[v].noun, m.key(sc+".fold"), m.key(sc+".fold_all"), m.key(sc+".unfold_all"), m.key(sc+".layout")))})
}

// listMode is the subtitle's " · <layout>" for a view with layouts.
func (m *Model) listMode(v view) string { return " · " + m.listLayout(v).name }

// withLayout puts a groupable view's layout key, and its fold key while
// grouped, after the view's first footer hint.
func (m *Model) withLayout(keys [][2]string) [][2]string {
	if _, ok := listSpecs[m.view]; !ok {
		return keys
	}
	sc := viewScope(m.view)
	extra := m.hints(sc + ".layout")
	if m.listLayout(m.view).group != nil {
		extra = m.hints(sc+".fold", sc+".layout")
	}
	return slices.Concat(keys[:1], extra, keys[1:])
}

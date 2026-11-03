package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MartyJRE/claudia/internal/launch"
)

// visualSel is a range of rows picked with V, from anchor to the cursor.
type visualSel struct {
	view   view
	anchor int
}

const batchSession = "batch"

func visualView(v view) bool {
	switch v {
	case vInbox, vReview, vMine, vJira, vMentions, vConfluence:
		return true
	}
	return false
}

// visualRange is the selected rows of the current view, if V is on there.
func (m *Model) visualRange() (lo, hi int, ok bool) {
	if m.visual == nil || m.visual.view != m.view {
		return 0, 0, false
	}
	lo, hi = m.visual.anchor, m.cur[m.view].pos
	if lo > hi {
		lo, hi = hi, lo
	}
	return max(lo, 0), min(hi, m.count()-1), true
}

func (m *Model) inVisual(i int) bool {
	lo, hi, ok := m.visualRange()
	return ok && i >= lo && i <= hi
}

// updateVisual handles keys while V is on: moving extends the range, c and C
// hand the rows to Claude, esc and V leave.
func (m *Model) updateVisual(k string) (tea.Model, tea.Cmd, bool) {
	switch id := m.km.byScope["visual"][k]; id {
	case "visual.claude", "visual.claude_bg":
		m.background = id == "visual.claude_bg"
		items := m.selectedItems()
		m.visual = nil
		if len(items) == 0 {
			m.setFlash("nothing to hand over in the selection", true)
			return m, nil, true
		}
		m.askBatch(items)
		return m, nil, true
	}
	switch m.action(k) {
	case "global.up", "global.down", "global.top", "global.bottom":
		return nil, nil, false
	case "global.back", "global.visual":
		m.visual = nil
		return m, nil, true
	}
	return m, nil, true
}

// selectedItems describes each selected row so Claude can find it: a title
// plus the link or key it lives under. Group headers are skipped.
func (m *Model) selectedItems() []string {
	lo, hi, ok := m.visualRange()
	if !ok {
		return nil
	}
	var out []string
	for i := lo; i <= hi; i++ {
		switch m.view {
		case vInbox:
			it := m.vis.inbox[i]
			out = append(out, fmt.Sprintf("%s · %s · %s (%s)", kindLabel[it.kind], it.where, oneLine(it.text), it.url))
		case vReview, vMine:
			r := m.vis.mrs[i]
			state := "open"
			if r.merged {
				state = "merged"
			}
			out = append(out, fmt.Sprintf("%s !%d · %s · %s (%s)", r.Repo(), r.IID, r.Title, state, r.WebURL))
		case vJira:
			r := m.vis.jira[i]
			if r.header() || r.context {
				continue
			}
			out = append(out, fmt.Sprintf("%s · %s · %s (%s)", r.issue.Key, r.issue.Summary, r.issue.Status, m.ticketURL(r.issue.Key)))
		case vMentions:
			x := m.vis.mentions[i]
			out = append(out, fmt.Sprintf("mention on %s · %s · %s (%s)", x.where, x.author, oneLine(x.text), x.url))
		case vConfluence:
			p := m.vis.pages[i]
			out = append(out, fmt.Sprintf("page · %s (%s)", p.Title, p.URL))
		}
	}
	return out
}

func (m *Model) ticketURL(key string) string {
	if m.atl != nil {
		return m.atl.IssueURL(key)
	}
	return key
}

// askBatch asks what to do with the items, then starts one session for all.
func (m *Model) askBatch(items []string) {
	in := newInput("› ", "e.g. close all of these, I don't need them anymore")
	in.Width = max(40, m.width-12)
	in.Focus()
	m.prompt = &promptBox{
		title: fmt.Sprintf("what should Claude do with these %d?", len(items)),
		in:    in,
		submit: func(m *Model, what string) (tea.Model, tea.Cmd) {
			return m.startBatch(items, what)
		},
	}
}

func (m *Model) startBatch(items []string, what string) (tea.Model, tea.Cmd) {
	prompt := fmt.Sprintf("I picked these %d items in claudia:\n\n- %s\n\nWhat I want: %s\n\n"+
		"Use glab for GitLab and the Atlassian MCP for Jira and Confluence. Before anything that can't be undone "+
		"(closing or merging MRs, moving or commenting on tickets, posting anything), list exactly what you'll do "+
		"for each item and wait for my go-ahead.",
		len(items), strings.Join(items, "\n- "), what)
	r := []rune(what)
	if len(r) > 40 {
		r = append(r[:39], '…')
	}
	dir := getConfig().CodeRoot
	sp, _ := launch.New(dir, launch.WorkProfile(m.store.Profiles), "batch: "+string(r), prompt)
	sp.Kind, sp.Session = "batch", batchSession
	return m.start(sp)
}

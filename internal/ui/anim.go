package ui

import (
	"slices"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MartyJRE/claudia/internal/board"
	"github.com/MartyJRE/claudia/internal/runs"
)

type frameMsg time.Time

type frames struct {
	glyphs []string
	every  int // frame ticks per glyph
}

var spinner = frames{[]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}, 1}

// One loop per thing a session does; every glyph is one cell wide and not of
// ambiguous width, so the STATUS column stays put.
var activityFrames = map[string]frames{
	"running":    {[]string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}, 1},
	"editing":    {[]string{"❙", "❚", "❙", "❘"}, 3},
	"reading":    {[]string{"◜", "◝", "◞", "◟"}, 2},
	"reviewing":  {[]string{"◜", "◝", "◞", "◟"}, 2},
	"delegating": {[]string{"⠢", "⠦", "⠶", "⠴", "⠤", "⠄", "⠂"}, 1},
	"thinking":   {[]string{"✶", "✷", "✸", "✹", "✸", "✷"}, 2},
	"working":    spinner,
}

func frame() tea.Cmd {
	return tickAfter(120*time.Millisecond, func(t time.Time) tea.Msg { return frameMsg(t) })
}

// glyph is the frame to draw now; still, it's always the first.
func (m *Model) glyph(f frames) string {
	if !getConfig().Animations() {
		return f.glyphs[0]
	}
	return f.glyphs[m.frame/f.every%len(f.glyphs)]
}

func (m *Model) spin() string { return m.glyph(spinner) }

// activity is what a working session is doing, as a verb and what it acts on.
func activity(r row) (string, string) { return board.Activity(r.sess()) }

func (m *Model) activityGlyph(verb string) string {
	return m.glyph(activityFrames[verb]) + " " + verb
}

// animate starts the frame ticks when something on screen moves and they
// aren't running yet.
func (m *Model) animate() tea.Cmd {
	if m.animating || !m.animates() {
		return nil
	}
	m.animating = true
	return frame()
}

// animates says whether anything on screen is drawn from the frame counter;
// when nothing is, the ticks stop and claudia sits idle.
func (m *Model) animates() bool {
	if !getConfig().Animations() {
		return false
	}
	if u := m.upd; u != nil {
		return u.stage < upDone && u.err == nil
	}
	if src, ok := m.viewSourceOf(); ok && m.busy[src] {
		return true
	}
	switch m.view {
	case vSessions:
		if !m.sessLoaded {
			return true
		}
		for _, r := range m.vis.sessions {
			if r.isSess && r.sess.live() && r.sess.Live.Status.Working() {
				return true
			}
		}
	case vInbox:
		return !m.briefPending.IsZero()
	case vBoard:
		if m.jira.FetchedAt.IsZero() && m.atlErr == nil {
			return true
		}
		for _, col := range m.vis.board {
			if slices.ContainsFunc(col, func(c boardCard) bool {
				return c.turn == turnAgent || slices.ContainsFunc(c.runs, func(r runs.Run) bool { return r.Outcome == runs.Running })
			}) {
				return true
			}
		}
	case vReview, vMine:
		return m.gl.FetchedAt.IsZero() && m.errs[srcGitlab] == nil
	case vJira:
		return m.jira.FetchedAt.IsZero() && m.atlErr == nil
	case vConfluence:
		return m.conf.FetchedAt.IsZero() && m.atlErr == nil
	case vNotes:
		return m.vault != nil && m.vaultNotes == nil
	case vUsage:
		return !m.usageLoaded
	}
	return false
}

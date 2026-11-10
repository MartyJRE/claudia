package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/MartyJRE/claudia/internal/update"
)

// The release notes page: one release at a time, newest first, [ and ] step
// to the previous and next version.

type releasesView struct {
	list    []update.Release
	err     error
	loaded  bool
	i       int
	current string
	vp      viewport.Model
	width   int
	shown   int
}

type releasesMsg struct {
	list []update.Release
	err  error
}

// maxReleasePages caps how far back the page loads, at PerPage releases a page.
const maxReleasePages = 10

func (m *Model) openReleases() tea.Cmd {
	if m.rel != nil {
		return nil
	}
	cur, _ := currentVersion()
	m.rel = &releasesView{current: cur}
	m.renderReleases()
	return func() tea.Msg {
		var all []update.Release
		for page := 1; page <= maxReleasePages; page++ {
			l, err := fetchReleases(page)
			if err != nil {
				return releasesMsg{all, err}
			}
			all = append(all, l...)
			if len(l) < update.PerPage {
				break
			}
		}
		return releasesMsg{all, nil}
	}
}

func (m *Model) releasesLoaded(msg releasesMsg) {
	r := m.rel
	r.list, r.err, r.loaded, r.i = msg.list, msg.err, true, 0
	m.renderReleases()
}

func (m *Model) renderReleases() {
	r := m.rel
	w, h := m.width, max(5, m.height-4)
	var text string
	switch {
	case !r.loaded:
		text = "\n  " + sDim.Render("asking GitHub for the releases…")
	case len(r.list) == 0 && r.err != nil:
		text = "\n  " + sErr.Render("couldn't load the releases: "+r.err.Error())
	case len(r.list) == 0:
		text = "\n  " + sDim.Render("no releases yet")
	default:
		body := strings.TrimSpace(r.list[r.i].Body)
		if body == "" {
			body = "_no notes for this release_"
		}
		text = strings.TrimRight(renderMarkdown(body, min(w-4, 110)), "\n")
	}
	off := 0
	if r.width == w && r.shown == r.i {
		off = r.vp.YOffset
	}
	r.vp = viewport.New(w, h)
	r.vp.SetContent(text)
	r.vp.SetYOffset(off)
	r.width, r.shown = w, r.i
}

func (m *Model) updateReleases(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	r := m.rel
	switch msg.String() {
	case "esc", "q", "backspace":
		m.rel = nil
		return m, nil
	case "[", "p":
		r.i = max(0, min(len(r.list)-1, r.i+1))
		m.renderReleases()
		return m, nil
	case "]", "n":
		r.i = max(0, r.i-1)
		m.renderReleases()
		return m, nil
	case "g", "home":
		r.vp.GotoTop()
		return m, nil
	case "G", "end":
		r.vp.GotoBottom()
		return m, nil
	}
	var cmd tea.Cmd
	r.vp, cmd = r.vp.Update(msg)
	return m, cmd
}

func (m *Model) viewReleases() string {
	r := m.rel
	title := sAccent.Render(" ✻ release notes")
	if len(r.list) > 0 {
		rel := r.list[r.i]
		title = sAccent.Render(" ✻ " + rel.Tag)
		var meta []string
		if rel.Name != "" && rel.Name != rel.Tag {
			meta = append(meta, rel.Name)
		}
		if !rel.Published.IsZero() {
			meta = append(meta, rel.Published.Format(time.DateOnly))
		}
		meta = append(meta, fmt.Sprintf("%d of %d", r.i+1, len(r.list)))
		title += sDim.Render(" · " + strings.Join(meta, " · "))
		if rel.Tag == r.current {
			title += "  " + sOK.Render("● installed")
		}
		if r.err != nil {
			title += "  " + sErr.Render("older releases didn't load")
		}
	}
	title = ansi.Truncate(title, m.width, "…")
	pct := fmt.Sprintf("%3.0f%%", r.vp.ScrollPercent()*100)
	keys := [][2]string{{"[", "older"}, {"]", "newer"}, {"j/k", "scroll"}, {"esc", "back"}}
	var parts []string
	for _, k := range keys {
		parts = append(parts, sKey.Render(k[0])+" "+sDim.Render(k[1]))
	}
	footer := ansi.Truncate(" "+strings.Join(parts, sRule.Render("  │  "))+"  "+sDim.Render(pct), m.width, "…")
	return lipgloss.JoinVertical(lipgloss.Left, title, sRule.Render(strings.Repeat("─", m.width)), r.vp.View(), footer)
}

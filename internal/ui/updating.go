package ui

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/MartyJRE/claudia/internal/update"
)

// The update screen: what's new, then the build as it happens, then enter restarts.

const (
	upChecking = iota
	upBuilding
	upWatcher
	upDone
)

type updateView struct {
	stage   int
	plan    update.Plan
	planErr error
	log     []string
	err     error
	stream  chan tea.Msg
	notes   viewport.Model
}

type upPlanMsg struct {
	plan update.Plan
	err  error
}
type upLogMsg string
type upDoneMsg struct{ err error }

func (m *Model) startUpdate() tea.Cmd {
	if m.upd != nil {
		return nil
	}
	m.upd = &updateView{}
	return func() tea.Msg {
		p, err := fetchUpdate()
		return upPlanMsg{p, err}
	}
}

func waitStream(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

func (m *Model) updateMsg(msg tea.Msg) tea.Cmd {
	u := m.upd
	switch msg := msg.(type) {
	case upPlanMsg:
		u.plan, u.planErr = msg.plan, msg.err
		u.stage = upBuilding
		m.renderUpdateNotes()
		u.stream = make(chan tea.Msg, 64)
		ch := u.stream
		go func() {
			err := installUpdate(func(l string) { ch <- upLogMsg(l) })
			if err == nil {
				stopWatcher()
			}
			ch <- upDoneMsg{err}
		}()
		return waitStream(ch)
	case upLogMsg:
		// Fetching straight from git adds a "get …: found meta tag" line per
		// dependency; the downloads and package names say enough.
		if l := string(msg); !strings.HasPrefix(l, "get ") {
			u.log = append(u.log, l)
		}
		return waitStream(u.stream)
	case upDoneMsg:
		if msg.err != nil {
			u.err = msg.err
			return nil
		}
		u.stage = upDone
	}
	return nil
}

// planMarkdown is what's new in the plan: each release's notes, or the commits
// for a build that predates releases.
func planMarkdown(p update.Plan) string {
	var b strings.Builder
	for _, r := range p.Releases {
		b.WriteString("## " + r.Tag + "\n\n")
		if !r.Published.IsZero() {
			b.WriteString("_" + r.Published.Format(time.DateOnly) + "_\n\n")
		}
		b.WriteString(strings.TrimSpace(r.Body) + "\n\n")
	}
	for _, c := range p.Commits {
		b.WriteString("- `" + c.SHA + "` " + c.Subject + "\n")
	}
	return b.String()
}

func (m *Model) renderUpdateNotes() {
	u := m.upd
	u.notes = viewport.New(m.width, 1)
	u.notes.SetContent(strings.TrimRight(renderMarkdown(planMarkdown(u.plan), min(m.width-4, 110)), "\n"))
}

func (m *Model) updateKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	u := m.upd
	k := msg.String()
	if m.km.byScope["global"][k] == "global.releases" {
		return m, m.openReleases()
	}
	switch k {
	case "enter":
		if u.stage == upDone {
			m.Result.Restart = true
			return m, tea.Quit
		}
	case "esc", "q":
		// The build can't be stopped halfway; once it's over, esc goes back.
		if u.err != nil || u.stage == upDone {
			m.upd = nil
		}
	case "g", "home":
		u.notes.GotoTop()
	case "G", "end":
		u.notes.GotoBottom()
	default:
		var cmd tea.Cmd
		u.notes, cmd = u.notes.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) viewUpdate() string {
	u := m.upd
	spin := lipgloss.NewStyle().Foreground(blue).Render(spinner[m.frame%len(spinner)])
	ok := sOK.Render("✓")
	todo := sDim.Render("○")
	bad := sErr.Render("✗")
	step := func(n int, text string) string {
		mark := todo
		switch {
		case u.stage > n:
			mark = ok
		case u.stage == n && u.err != nil:
			mark = bad
		case u.stage == n:
			mark = spin
		}
		st := sDim
		if u.stage >= n {
			st = sText
		}
		return "   " + mark + " " + st.Render(text)
	}

	var top []string
	top = append(top, "", sAccent.Render(" ✻ updating claudia"), "")
	from, to := u.plan.Current, u.plan.Latest
	if len(from) > 7 && !strings.HasPrefix(from, "v") {
		from = from[:7]
	}
	ver := ""
	switch {
	case from != "" && to != "":
		ver = sDim.Render(" " + from + " → " + to)
	case to != "":
		ver = sDim.Render(" → " + to)
	}
	top = append(top, step(upChecking, "checking GitHub for the latest version")+ver)
	top = append(top, step(upBuilding, "downloading and building"))
	top = append(top, step(upWatcher, "restarting the background watcher"))
	top = append(top, step(upDone, "restart claudia"))
	top = append(top, "", sBold.Render(" what's new"))

	var bottom []string
	if len(u.log) > 0 || u.stage == upBuilding {
		bottom = append(bottom, "", sBold.Render(" build output"))
		start := max(0, len(u.log)-8)
		for _, l := range u.log[start:] {
			bottom = append(bottom, "   "+sDim.Render(l))
		}
	}
	if u.err != nil {
		bottom = append(bottom, "", "   "+sErr.Render("update failed: "+u.err.Error()), "   "+sKey.Render("esc")+sDim.Render(" back to claudia"))
	}
	keys := [][2]string{{"j/k", "scroll"}, {m.key("global.releases"), "release notes"}}
	if u.stage == upDone {
		keys = append([][2]string{{"enter", "restart now"}, {"esc", "later"}}, keys...)
	}
	var parts []string
	for _, k := range keys {
		parts = append(parts, sKey.Render(k[0])+" "+sDim.Render(k[1]))
	}
	bottom = append(bottom, "", " "+strings.Join(parts, sRule.Render("  │  ")))

	var middle []string
	switch {
	case u.planErr != nil:
		middle = append(middle, "   "+sDim.Render("couldn't ask GitHub ("+u.planErr.Error()+"); updating anyway"))
	case u.stage == upChecking:
		middle = append(middle, "   "+sDim.Render("…"))
	case len(u.plan.Commits) == 0 && len(u.plan.Releases) == 0:
		middle = append(middle, "   "+sDim.Render("nothing new — you already have the latest, reinstalling it"))
	default:
		u.notes.Height = max(3, m.height-len(top)-len(bottom))
		middle = strings.Split(u.notes.View(), "\n")
	}

	b := append(append(top, middle...), bottom...)
	for i := range b {
		b[i] = ansi.Truncate(b[i], m.width, "…")
	}
	return strings.Join(b, "\n")
}

package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/MartyJRE/claudia/internal/update"
)

// The update screen: what's new, then the build as it happens, then a restart.

const (
	upChecking = iota
	upBuilding
	upWatcher
	upDone
)

type updateView struct {
	stage    int
	plan     update.Plan
	planErr  error
	log      []string
	err      error
	stream   chan tea.Msg
	restarts time.Time
}

type upPlanMsg struct {
	plan update.Plan
	err  error
}
type upLogMsg string
type upDoneMsg struct{ err error }
type upRestartMsg struct{}

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
		u.restarts = time.Now().Add(3 * time.Second)
		return tickAfter(3*time.Second, func(time.Time) tea.Msg { return upRestartMsg{} })
	case upRestartMsg:
		m.Result.Restart = true
		return tea.Quit
	}
	return nil
}

func (m *Model) updateKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	u := m.upd
	switch msg.String() {
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

	var b []string
	b = append(b, "", sAccent.Render(" ✻ updating claudia"), "")
	from, to := u.plan.Current, u.plan.Latest
	if len(from) > 7 {
		from = from[:7]
	}
	ver := ""
	switch {
	case from != "" && to != "":
		ver = sDim.Render(" " + from + " → " + to)
	case to != "":
		ver = sDim.Render(" → " + to)
	}
	b = append(b, step(upChecking, "checking GitHub for the latest version")+ver)
	b = append(b, step(upBuilding, "downloading and building"))
	b = append(b, step(upWatcher, "restarting the background watcher"))
	restart := "restarting claudia"
	if u.stage == upDone {
		left := max(0, int(time.Until(u.restarts).Seconds()+0.99))
		restart += sDim.Render(fmt.Sprintf(" in %d…  ", left)) + sKey.Render("enter") + sDim.Render(" now")
	}
	b = append(b, step(upDone, restart))

	b = append(b, "", sBold.Render(" what's new"))
	switch {
	case u.planErr != nil:
		b = append(b, "   "+sDim.Render("couldn't ask GitHub ("+u.planErr.Error()+"); updating anyway"))
	case u.stage == upChecking:
		b = append(b, "   "+sDim.Render("…"))
	case len(u.plan.Commits) == 0:
		b = append(b, "   "+sDim.Render("nothing new — you already have the latest, reinstalling it"))
	default:
		for i, c := range u.plan.Commits {
			if i == 12 {
				b = append(b, "   "+sDim.Render(fmt.Sprintf("… and %d more", len(u.plan.Commits)-12)))
				break
			}
			b = append(b, "   "+sDim.Render(c.SHA+"  ")+sText.Render(c.Subject))
		}
	}

	if len(u.log) > 0 || u.stage == upBuilding {
		b = append(b, "", sBold.Render(" build output"))
		start := max(0, len(u.log)-8)
		for _, l := range u.log[start:] {
			b = append(b, "   "+sDim.Render(l))
		}
	}
	if u.err != nil {
		b = append(b, "", "   "+sErr.Render("update failed: "+u.err.Error()), "   "+sKey.Render("esc")+sDim.Render(" back to claudia"))
	}
	for i := range b {
		b[i] = ansi.Truncate(b[i], m.width, "…")
	}
	return strings.Join(b, "\n")
}

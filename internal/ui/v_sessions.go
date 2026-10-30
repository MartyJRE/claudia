package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/launch"
	"github.com/MartyJRE/claudia/internal/repos"
)

var sessionCols = []col{
	{"STATUS", 12, 0},
	{"SESSION", 0, 0},
	{"REPO", 16, 95},
	{"BRANCH", 22, 130},
	{"TICKET", 10, 0},
	{"MODEL", 10, 95},
	{"AGE", 5, 0},
	{"WHERE", 16, 130},
}

func (m *Model) filterSessions(match func(...string) bool) []row {
	var out []row
	for _, r := range m.sessions {
		if !m.allSessions && !r.live() {
			continue
		}
		if match(r.Name(), project(r.Dir()), branch(r), m.ix.sessionTicket[r.ID], r.Profile.Name) {
			out = append(out, r)
		}
	}
	return out
}

func (m *Model) listSessions(h int) string {
	if !m.sessLoaded {
		return lipgloss.Place(m.width, h, lipgloss.Center, lipgloss.Center, sDim.Render(spinner[m.frame%len(spinner)]+" reading sessions…"))
	}
	if len(m.restore) > 0 {
		banner := pill(black, yellow, fmt.Sprintf("%d sessions were running before the restart", len(m.restore))) +
			"  " + sKey.Render("X") + sDim.Render(" restore all  ") + sKey.Render("ctrl+x") + sDim.Render(" forget")
		return " " + banner + "\n" + m.listSessionsTable(h-1)
	}
	return m.listSessionsTable(h)
}

func (m *Model) listSessionsTable(h int) string {
	empty := "no live sessions — a for closed ones, n for a new one"
	if m.allSessions || m.filter.Value() != "" {
		empty = "nothing matches"
	}
	rows := m.vis.sessions
	return m.table(sessionCols, len(rows), &m.cur[vSessions], h, func(i int, sel bool) []string {
		r := rows[i]
		return []string{
			m.status(r),
			sessionTitle(r, sel),
			sText.Render(project(r.Dir())),
			sBranch.Render(branch(r)),
			sTicket.Render(m.ix.sessionTicket[r.ID]),
			modelChip(r),
			sDim.Render(age(r.LastActive())),
			m.where(r),
		}
	}, empty)
}

func (m *Model) status(r row) string {
	if !r.live() {
		return sDim.Render("○ closed")
	}
	switch s := r.Live.Status; {
	case s == claude.StatusWaiting:
		return lipgloss.NewStyle().Foreground(yellow).Bold(true).Render("◆ needs you")
	case s.Working():
		return sOK.Render(spinner[m.frame%len(spinner)] + " working")
	default:
		return lipgloss.NewStyle().Foreground(blue).Render("● your turn")
	}
}

func sessionTitle(r row, selected bool) string {
	st := sText
	if selected || r.live() {
		st = sBold
	}
	s := st.Render(r.Name())
	if r.Profile.Name == "personal" {
		s += sDim.Render(" ·personal")
	}
	return s
}

func (m *Model) where(r row) string {
	switch {
	case r.pane != nil:
		return sOK.Render("tmux ") + sDim.Render(r.pane.Target())
	case r.live() && r.host != "":
		return sDim.Render(strings.ToLower(r.host))
	case r.live():
		return sDim.Render("external")
	}
	return ""
}

func modelChip(r row) string {
	if r.Transcript == nil || r.Model == "" {
		return ""
	}
	fam, ver := modelName(r.Model)
	return lipgloss.NewStyle().Foreground(modelColor(fam)).Render(fam + " " + ver)
}

// modelName turns "claude-opus-5-5" into ("Opus", "5.5").
func modelName(id string) (string, string) {
	parts := strings.Split(strings.TrimPrefix(id, "claude-"), "-")
	if len(parts) == 0 || parts[0] == "" {
		return id, ""
	}
	fam := strings.ToUpper(parts[0][:1]) + parts[0][1:]
	var ver []string
	for _, p := range parts[1:] {
		if len(p) > 2 {
			break
		}
		ver = append(ver, p)
	}
	return fam, strings.Join(ver, ".")
}

func (m *Model) currentSession() (row, bool) {
	c := m.cur[vSessions].pos
	if c < 0 || c >= len(m.vis.sessions) {
		return row{}, false
	}
	return m.vis.sessions[c], true
}

func (m *Model) detailSession() string {
	r, ok := m.currentSession()
	if !ok {
		return m.panel(nil)
	}
	head := sBold.Render(r.Name())
	if t := m.ix.sessionTicket[r.ID]; t != "" {
		head += "  " + sTicket.Render(t)
		if is, ok := m.ix.issues[t]; ok {
			head += sDim.Render(" "+is.Summary+" · ") + statusText(is)
		}
	}
	lines := []string{head}
	meta := []string{sDim.Render(shortHome(r.Dir()))}
	if b := branch(r); b != "" {
		meta = append(meta, sBranch.Render(" "+b))
	}
	meta = append(meta, sDim.Render(r.Profile.Name), sDim.Render(r.ID[:8]))
	lines = append(lines, strings.Join(meta, sDim.Render("  ·  ")))

	if r.Transcript != nil {
		if p := oneLine(r.LastPrompt); p != "" {
			lines = append(lines, sDim.Render("› ")+sText.Render(p))
		}
	}
	if urls := m.ix.sessionMRs[r.ID]; len(urls) > 0 {
		var prs []string
		for _, u := range urls {
			prs = append(prs, m.mrRef(u))
		}
		lines = append(lines, strings.Join(prs, "   "))
	}
	if r.Transcript != nil {
		var info []string
		if u := r.Usage; u.Requests > 0 {
			info = append(info, sDim.Render(fmt.Sprintf("%d req · %s out · %s cache read", u.Requests, tokens(u.Output), tokens(u.CacheRead))))
		}
		if len(r.Models) > 1 {
			var ms []string
			for _, id := range r.Models {
				f, v := modelName(id)
				ms = append(ms, f+" "+v)
			}
			info = append(info, sDim.Render("models: "+strings.Join(ms, " → ")))
		}
		if len(info) > 0 {
			lines = append(lines, strings.Join(info, sDim.Render("   ")))
		}
	}
	return m.panel(m.withNotes(lines, m.ix.sessionTicket[r.ID]))
}

// mrRef renders an MR link with whatever claudia knows about its state.
func (m *Model) mrRef(u string) string {
	mr, ok := m.ix.mrs[u]
	if !ok {
		iid := u[strings.LastIndex(u, "/")+1:]
		proj := u
		if i := strings.Index(u, "/-/"); i >= 0 {
			proj = filepath.Base(u[:i])
		}
		return sKey.Render("!"+iid) + sDim.Render(" "+proj)
	}
	return sKey.Render(fmt.Sprintf("!%d", mr.IID)) + sDim.Render(" "+mr.Repo()+" ") + mrState(mr)
}

func (m *Model) keySessions(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "a":
		m.allSessions = !m.allSessions
		m.refilter()
	case "enter":
		return m.jumpSession()
	case "R":
		r, ok := m.currentSession()
		if ok {
			return m.resume(r)
		}
	case "t":
		if r, ok := m.currentSession(); ok {
			m.pickTicket(r)
		}
	case "J":
		if r, ok := m.currentSession(); ok {
			return m.gotoTicket(m.ix.sessionTicket[r.ID])
		}
	case "o":
		r, ok := m.currentSession()
		if ok && len(m.ix.sessionMRs[r.ID]) > 0 {
			urls := m.ix.sessionMRs[r.ID]
			return m, openURL(urls[len(urls)-1])
		}
		m.setFlash("no MR linked to this session", true)
	case "y":
		if r, ok := m.currentSession(); ok {
			return m, copyText(r.ID, "session id")
		}
	case "X":
		return m.restoreAll()
	case "ctrl+x":
		m.forgetRestore()
	}
	return m, nil
}

func (m *Model) pickTicket(r row) {
	var items []choice
	items = append(items, choice{label: sDim.Render("automatic"), search: "automatic", value: ""})
	items = append(items, choice{label: sDim.Render("none"), search: "none", value: "-"})
	seen := map[string]bool{}
	for _, is := range append(append([]atlassian.Issue{}, m.jira.Sprint...), m.jira.Team...) {
		if seen[is.Key] {
			continue
		}
		seen[is.Key] = true
		items = append(items, choice{label: sTicket.Render(is.Key) + " " + is.Summary, hint: is.Status, search: is.Key + " " + is.Summary, value: is.Key})
	}
	m.chooser = newChooser("ticket for "+r.Name(), items, func(m *Model, ch choice) (tea.Model, tea.Cmd) {
		if err := m.links.SetTicket(r.ID, ch.value.(string)); err != nil {
			m.setFlash(err.Error(), true)
		}
		m.reindex()
		return m, nil
	})
}

func (m *Model) jumpSession() (tea.Model, tea.Cmd) {
	r, ok := m.currentSession()
	if !ok {
		return m, nil
	}
	return m.jumpTo(r)
}

func (m *Model) jumpTo(r row) (tea.Model, tea.Cmd) {
	switch {
	case r.pane != nil:
		return m.goTo(r.pane.Target() + "." + r.pane.Index)
	case r.live():
		if r.host == "" {
			m.setFlash("runs outside tmux, in an unknown terminal", true)
			return m, nil
		}
		m.setFlash("runs in "+r.host+", outside tmux — bringing it forward", false)
		return m, run(exec.Command("open", "-a", r.host))
	}
	return m.resume(r)
}

func (m *Model) resume(r row) (tea.Model, tea.Cmd) {
	if r.live() {
		m.setFlash("already running — resuming would fork the transcript", true)
		return m, nil
	}
	if r.Dir() == "" {
		m.setFlash("no working directory recorded for this session", true)
		return m, nil
	}
	return m.start(launch.Resume(r.Session))
}

func project(dir string) string {
	if dir == "" {
		return ""
	}
	return filepath.Base(dir)
}

func branch(r row) string {
	if r.Transcript == nil {
		return ""
	}
	return r.Branch
}

func shortHome(p string) string {
	home, _ := os.UserHomeDir()
	if strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

func tokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.0fk", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

// newSession starts a plain claude in a picked repo, nothing linked. From the
// Sessions tab the current session's repo is preselected.
func (m *Model) newSession() {
	dir := ""
	if r, ok := m.currentSession(); ok && m.view == vSessions {
		dir = r.Dir()
	}
	m.pickRepo("new session in", "", func(r repos.Repo) (bool, string) {
		return dir != "" && (dir == r.Dir || strings.HasPrefix(dir, r.Dir+"/")), ""
	}, func(m *Model, dir string, p claude.Profile) (tea.Model, tea.Cmd) {
		sp, _ := launch.New(dir, p, filepath.Base(dir), "")
		return m.start(sp)
	})
}

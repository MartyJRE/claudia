package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/launch"
	"github.com/MartyJRE/claudia/internal/repos"
	"github.com/MartyJRE/claudia/internal/tmux"
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
			"  " + sKey.Render(m.key("sessions.restore")) + sDim.Render(" restore all  ") + sKey.Render(m.key("sessions.forget_restore")) + sDim.Render(" forget")
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
		if !rows[i].isSess {
			return m.sessionHeaderCells(rows[i])
		}
		r := rows[i].sess
		return []string{
			m.status(r),
			sessionIndent(rows[i]) + sessionTitle(r, sel),
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
	if m.marked[r.ID] {
		return sAccent.Render("✓ marked")
	}
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
	case r.pane != nil && r.pane.Session == tmux.SplitSession:
		return sOK.Render("split")
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
	// Older ids put the version first: claude-3-5-haiku-20241022.
	short := func(p string) bool { return len(p) <= 2 && strings.Trim(p, "0123456789") == "" }
	i := 0
	for i < len(parts)-1 && short(parts[i]) {
		i++
	}
	fam := strings.ToUpper(parts[i][:1]) + parts[i][1:]
	ver := slices.Clone(parts[:i])
	if i == 0 {
		for _, p := range parts[1:] {
			if !short(p) {
				break
			}
			ver = append(ver, p)
		}
	}
	return fam, strings.Join(ver, ".")
}

func (m *Model) currentSession() (row, bool) {
	c := m.cur[vSessions].pos
	if c < 0 || c >= len(m.vis.sessions) || !m.vis.sessions[c].isSess {
		return row{}, false
	}
	return m.vis.sessions[c].sess, true
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
	meta = append(meta, sDim.Render(r.Profile.Name), sDim.Render(r.ShortID()))
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
		if i := strings.Index(u, "/-/"); i >= 0 {
			return sKey.Render("!"+iid) + sDim.Render(" "+filepath.Base(u[:i]))
		}
		if i := strings.Index(u, "/pull/"); i >= 0 {
			return sKey.Render("#"+iid) + sDim.Render(" "+filepath.Base(u[:i]))
		}
		return sDim.Render(shortLink(u))
	}
	return sKey.Render(fmt.Sprintf("!%d", mr.IID)) + sDim.Render(" "+mr.Repo()+" ") + mrState(mr)
}

func (m *Model) keySessions(k string) (tea.Model, tea.Cmd) {
	switch m.action(k) {
	case "sessions.fold":
		m.toggleSessionFold()
		return m, nil
	case "sessions.fold_all":
		m.foldAllSessions(true)
		return m, nil
	case "sessions.unfold_all":
		m.foldAllSessions(false)
		return m, nil
	case "sessions.group":
		m.sessPrefs.Group = (m.sessPrefs.Group + 1) % nSessGroups
		m.saveSessionPrefs()
		m.refilter()
		m.setFlash("sessions "+sessGroupNames[m.sessPrefs.Group], false)
		return m, nil
	case "sessions.all":
		m.allSessions = !m.allSessions
		m.refilter()
	case "sessions.jump":
		if c := m.cur[vSessions].pos; c >= 0 && c < len(m.vis.sessions) && !m.vis.sessions[c].isSess {
			m.toggleSessionFold()
			return m, nil
		}
		return m.jumpSession()
	case "sessions.resume":
		r, ok := m.currentSession()
		if ok {
			return m.resume(r)
		}
	case "sessions.close":
		if r, ok := m.currentSession(); ok {
			return m.closeSession(r)
		}
	case "sessions.mark":
		if r, ok := m.currentSession(); ok {
			m.toggleMark(r)
		}
	case "sessions.split":
		return m.split()
	case "sessions.unsplit":
		if r, ok := m.currentSession(); ok {
			return m.unsplit(r)
		}
	case "sessions.set_ticket":
		if r, ok := m.currentSession(); ok {
			m.pickTicket(r)
		}
	case "sessions.ticket":
		if r, ok := m.currentSession(); ok {
			return m.gotoTicket(m.ix.sessionTicket[r.ID])
		}
	case "sessions.mr":
		r, ok := m.currentSession()
		if ok && len(m.ix.sessionMRs[r.ID]) > 0 {
			urls := m.ix.sessionMRs[r.ID]
			return m, openURL(urls[len(urls)-1])
		}
		m.setFlash("no MR linked to this session", true)
	case "sessions.copy_id":
		if r, ok := m.currentSession(); ok {
			return m, copyText(r.ID, "session id")
		}
	case "sessions.restore":
		return m.restoreAll()
	case "sessions.forget_restore":
		m.forgetRestore()
	}
	return m, nil
}

// closeSession ends the claude process; a window claudia opened goes with it,
// and the transcript stays for resuming.
func (m *Model) closeSession(r row) (tea.Model, tea.Cmd) {
	if !r.live() {
		m.setFlash("not running", true)
		return m, nil
	}
	if err := syscall.Kill(r.Live.PID, syscall.SIGTERM); err != nil {
		m.setFlash("close "+r.Name()+": "+err.Error(), true)
		return m, nil
	}
	delete(m.marked, r.ID)
	m.setFlash("closed "+r.Name()+" · "+m.key("sessions.resume")+" resumes it", false)
	return m, m.loadSessions()
}

func (m *Model) toggleMark(r row) {
	if r.pane == nil {
		m.setFlash("only live sessions in tmux can be split", true)
		return
	}
	if m.marked == nil {
		m.marked = map[string]bool{}
	}
	if m.marked[r.ID] {
		delete(m.marked, r.ID)
	} else {
		m.marked[r.ID] = true
	}
	m.cur[vSessions].pos = min(m.cur[vSessions].pos+1, len(m.vis.sessions)-1)
}

// split tiles the marked sessions, plus the one under the cursor, into one window.
func (m *Model) split() (tea.Model, tea.Cmd) {
	var panes []tmux.Pane
	seen := map[string]bool{}
	add := func(r row) {
		if r.pane != nil && !seen[r.pane.ID] {
			seen[r.pane.ID] = true
			panes = append(panes, *r.pane)
		}
	}
	if r, ok := m.currentSession(); ok {
		add(r)
	}
	for _, r := range m.sessions {
		if m.marked[r.ID] {
			add(r)
		}
	}
	if len(panes) < 2 {
		m.setFlash("mark sessions with "+m.key("sessions.mark")+" first, then "+m.key("sessions.split")+" puts them side by side", true)
		return m, nil
	}
	target, err := tmux.JoinSplit(panes)
	if err != nil {
		m.setFlash("split: "+err.Error(), true)
		return m, nil
	}
	m.marked = nil
	return m.goTo(target)
}

func (m *Model) unsplit(r row) (tea.Model, tea.Cmd) {
	if r.pane == nil || r.pane.Session != tmux.SplitSession {
		m.setFlash("not in a split", true)
		return m, nil
	}
	if err := tmux.Unsplit(r.pane.WindowID); err != nil {
		m.setFlash("unsplit: "+err.Error(), true)
		return m, nil
	}
	m.setFlash("sent the split's sessions back to their windows", false)
	return m, m.loadSessions()
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
	case n >= 999_500: // would round to "1000k"
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

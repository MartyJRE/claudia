package ui

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/launch"
	"github.com/MartyJRE/claudia/internal/links"
	"github.com/MartyJRE/claudia/internal/repos"
	"github.com/MartyJRE/claudia/internal/tmux"
	"github.com/MartyJRE/claudia/internal/vault"
)

type Options struct {
	Popup bool
}

// Result is what main does after the TUI exits: outside tmux it attaches to Attach.
type Result struct {
	Attach string
}

type view int

const (
	vInbox view = iota
	vSessions
	vReview
	vMine
	vJira
	vMentions
	vConfluence
	vNotes
	vUsage
	nViews
)

var viewNames = [nViews]string{"Inbox", "Sessions", "Review", "My MRs", "Jira", "Mentions", "Confluence", "Notes", "Usage"}

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const detailHeight = 8

type row struct {
	claude.Session
	pane *tmux.Pane
	host string
}

func (r row) live() bool { return r.Live != nil }

type Model struct {
	opts   Options
	Result Result

	store  *claude.Store
	links  *links.Links
	atl    *atlassian.Client
	atlErr error

	sessions     []row
	usage        []claude.DayModel
	usageScanner *claude.UsageScanner
	usageLoaded  bool
	usageLoading bool
	usageAt      time.Time
	jiraPrefs    jiraPrefs
	sessLoaded   bool
	sessLoading  bool
	gl           gitlab.Snapshot
	jira         atlassian.JiraSnapshot
	conf         atlassian.ConfluenceSnapshot
	repos        []repos.Repo
	ix           index
	seen         seenStore
	seenExisted  bool
	dismissed    dismissed
	dismissedAt  time.Time
	vault        *vault.Index
	vaultNotes   []vault.Note
	vaultLoading bool
	vaultAt      time.Time
	notesPrefs   notesPrefs
	restore      []restoreItem

	busy      [nSources]bool
	errs      [nSources]error
	fetchedAt [nSources]time.Time

	view        view
	cur         [nViews]cursor
	allSessions bool
	teamJira    bool
	showDone    bool
	showAll     bool

	vis visible

	filter    textinput.Model
	filtering bool
	chooser   *chooser
	prompt    *promptBox
	reader    *reader

	confirmKey string
	confirmAt  time.Time

	frame         int
	width, height int
	flash         string
	flashErr      bool
	flashAt       time.Time
}

func New(opts Options) *Model {
	f := textinput.New()
	f.Prompt = "/ "
	f.PromptStyle = sKey
	f.TextStyle = sText
	f.Placeholder = "filter"
	m := &Model{
		opts:       opts,
		store:      claude.NewStore(claude.DefaultProfiles(), 7*24*time.Hour),
		jiraPrefs:  loadJiraPrefs(),
		vault:      newVault(),
		notesPrefs: loadNotesPrefs(),
		view:       vSessions,
		links:      links.Load(),
		filter:     f,
		dismissed:  loadDismissed(),
	}
	m.atl, m.atlErr = atlassian.New()
	m.seen, m.seenExisted = loadSeen()
	m.loadCaches()
	m.restore = pendingRestore()
	m.usageScanner = claude.NewUsageScanner(m.store.Profiles, 31)
	return m
}

type tickMsg time.Time
type frameMsg time.Time
type flashMsg struct {
	text string
	err  bool
}
type refreshMsg source

func (m *Model) Init() tea.Cmd {
	m.sessLoading = true
	return tea.Batch(m.loadSessions(), m.refreshStale(), m.maybeUsage(), m.refreshVault(), tick(), frame())
}

func tick() tea.Cmd {
	return tea.Tick(1500*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func frame() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg { return frameMsg(t) })
}

func (m *Model) refreshStale() tea.Cmd {
	var cmds []tea.Cmd
	for s := source(0); s < nSources; s++ {
		if m.stale(s) {
			m.busy[s] = true
			cmds = append(cmds, m.fetch(s))
		}
	}
	return tea.Batch(cmds...)
}

func (m *Model) refreshAll() tea.Cmd {
	for s := range m.fetchedAt {
		if !m.busy[s] {
			m.fetchedAt[s] = time.Time{}
		}
	}
	return m.refreshStale()
}

func (m *Model) reindex() {
	m.ix = buildIndex(m.sessions, m.gl, m.jira, m.links)
	m.refilter()
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.reader != nil {
			m.renderReader()
		}
		m.filter.Width = msg.Width / 2
		return m, nil
	case sessionsMsg:
		m.sessions, m.sessLoaded, m.sessLoading = msg, true, false
		m.restore = notLive(m.restore, m.sessions)
		saveLive(m.sessions, m.restore != nil)
		m.reindex()
		return m, nil
	case fetched:
		m.applyFetched(msg)
		if msg.err != nil && msg.src != srcRepos {
			m.setFlash(viewSource(msg.src)+": "+msg.err.Error(), true)
		}
		m.reindex()
		return m, nil
	case tickMsg:
		var cmds []tea.Cmd
		if !m.sessLoading {
			m.sessLoading = true
			cmds = append(cmds, m.loadSessions())
		}
		if time.Since(m.vaultAt) > 30*time.Second {
			cmds = append(cmds, m.refreshVault())
		}
		if m.reloadDismissed() {
			m.refilter()
		}
		if link, ok := m.takeOpenRequest(); ok {
			_, cmd := m.openLink(link)
			cmds = append(cmds, cmd)
		}
		cmds = append(cmds, m.refreshStale(), m.maybeUsage(), tick())
		if m.flash != "" && time.Since(m.flashAt) > 5*time.Second {
			m.flash = ""
		}
		return m, tea.Batch(cmds...)
	case frameMsg:
		m.frame++
		return m, frame()
	case flashMsg:
		m.setFlash(msg.text, msg.err)
		return m, nil
	case refreshMsg:
		m.fetchedAt[source(msg)] = time.Time{}
		return m, m.refreshStale()
	case usageMsg:
		m.usage, m.usageLoaded, m.usageLoading = msg, true, false
		m.usageAt = time.Now()
		m.refilter()
		return m, nil
	case imagesMsg:
		return m, m.openImages(msg)
	case startMsg:
		return m.start(msg.sp)
	case transitionsMsg:
		m.showTransitions(msg)
		return m, nil
	case vaultMsg:
		m.applyVault(msg)
		return m, nil
	case editedMsg:
		if msg.err != nil {
			m.setFlash(msg.err.Error(), true)
		}
		return m, m.refreshVault()
	case mrLoadedMsg:
		if msg.err != nil {
			m.setFlash(msg.err.Error(), true)
			return m, nil
		}
		m.flash = ""
		return m.reviewWithClaude(msg.mr)
	case tea.KeyMsg:
		switch {
		case m.reader != nil:
			return m.updateReader(msg)
		case m.prompt != nil:
			return m.updatePrompt(msg)
		case m.chooser != nil:
			return m.updateChooser(msg)
		case m.filtering:
			return m.updateFilter(msg)
		}
		return m.updateKey(msg)
	}
	return m, nil
}

func viewSource(s source) string {
	return [...]string{"GitLab", "Jira", "Confluence", "repos"}[s]
}

func (m *Model) setFlash(s string, err bool) {
	m.flash, m.flashErr, m.flashAt = s, err, time.Now()
}

func (m *Model) updateFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filter.SetValue("")
		m.filter.Blur()
		m.filtering = false
		m.refilter()
		return m, nil
	case "enter":
		m.filter.Blur()
		m.filtering = false
		return m, nil
	case "up", "ctrl+p", "down", "ctrl+n":
		return m.updateKey(msg)
	}
	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(msg)
	m.refilter()
	return m, cmd
}

func (m *Model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	c := &m.cur[m.view]
	switch k {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		if m.filter.Value() != "" {
			m.filter.SetValue("")
			m.refilter()
			return m, nil
		}
		return m, tea.Quit
	case "up", "k", "ctrl+p":
		c.pos = max(0, c.pos-1)
		return m, nil
	case "down", "j", "ctrl+n":
		c.pos = min(m.count()-1, c.pos+1)
		return m, nil
	case "g", "home":
		c.pos = 0
		return m, nil
	case "G", "end":
		c.pos = m.count() - 1
		return m, nil
	case "tab", "right", "l":
		m.switchView((m.view + 1) % nViews)
		return m, nil
	case "shift+tab", "left", "h":
		m.switchView((m.view + nViews - 1) % nViews)
		return m, nil
	case "u":
		return m, m.askLink()
	case "N":
		m.chooseNotes()
		return m, nil
	case "w":
		return m, m.writeNote()
	case "n":
		m.newSession()
		return m, nil
	case "/":
		m.filtering = true
		return m, m.filter.Focus()
	case "ctrl+r":
		m.setFlash("refreshing…", false)
		return m, m.refreshAll()
	}
	if len(k) == 1 && k[0] >= '1' && k[0] < '1'+byte(nViews) {
		m.switchView(view(k[0] - '1'))
		return m, nil
	}
	switch m.view {
	case vNotes:
		return m.keyNotes(k)
	case vInbox:
		return m.keyInbox(k)
	case vSessions:
		return m.keySessions(k)
	case vReview, vMine:
		return m.keyMRs(k)
	case vJira:
		return m.keyJira(k)
	case vMentions:
		return m.keyMentions(k)
	case vConfluence:
		return m.keyConfluence(k)
	}
	return m, nil
}

func (m *Model) switchView(v view) {
	if v == m.view {
		return
	}
	m.view = v
	m.filter.SetValue("")
	m.refilter()
}

// confirm makes a destructive or outward key need a second press within 3s.
func (m *Model) confirm(key, prompt string) bool {
	if m.confirmKey == key && time.Since(m.confirmAt) < 3*time.Second {
		m.confirmKey = ""
		return true
	}
	m.confirmKey, m.confirmAt = key, time.Now()
	m.setFlash(prompt+" — press again to confirm", false)
	return false
}

func (m *Model) View() string {
	if m.width == 0 {
		return ""
	}
	if m.reader != nil {
		return m.viewReader()
	}
	header := m.viewHeader()
	footer := m.viewFooter()
	body := m.height - lipgloss.Height(header) - lipgloss.Height(footer)
	if m.prompt != nil {
		return lipgloss.JoinVertical(lipgloss.Left, header, m.viewPrompt(body), footer)
	}
	if m.chooser != nil {
		return lipgloss.JoinVertical(lipgloss.Left, header, m.viewChooser(body), footer)
	}
	detail := m.viewDetail()
	list := m.viewList(body - lipgloss.Height(detail))
	return lipgloss.JoinVertical(lipgloss.Left, header, list, detail, footer)
}

func (m *Model) viewList(h int) string {
	switch m.view {
	case vNotes:
		return m.listNotes(h)
	case vInbox:
		return m.listInbox(h)
	case vSessions:
		return m.listSessions(h)
	case vReview, vMine:
		return m.listMRs(h)
	case vJira:
		return m.listJira(h)
	case vMentions:
		return m.listMentions(h)
	case vConfluence:
		return m.listConfluence(h)
	case vUsage:
		return m.listUsage(h)
	}
	return ""
}

func (m *Model) viewDetail() string {
	switch m.view {
	case vNotes:
		return m.detailNote()
	case vInbox:
		return m.detailInbox()
	case vSessions:
		return m.detailSession()
	case vReview, vMine:
		return m.detailMR()
	case vJira:
		return m.detailJira()
	case vMentions:
		return m.detailMention()
	case vConfluence:
		return m.detailConfluence()
	case vUsage:
		return m.detailUsage()
	}
	return ""
}

// maybeUsage rescans transcripts at most once a minute; the scan is incremental.
func (m *Model) maybeUsage() tea.Cmd {
	if m.usageLoading || time.Since(m.usageAt) < time.Minute {
		return nil
	}
	m.usageLoading = true
	return m.loadUsage()
}

func (m *Model) viewHeader() string {
	var waiting, working, ready int
	for _, r := range m.sessions {
		switch {
		case !r.live():
		case r.Live.Status == claude.StatusWaiting:
			waiting++
		case r.Live.Status.Working():
			working++
		default:
			ready++
		}
	}
	title := sAccent.Render(" ✻ claudia")
	var pills []string
	if waiting > 0 {
		pills = append(pills, pill(black, yellow, fmt.Sprintf("%d needs you", waiting)))
	}
	if n := m.pendingReviews(); n > 0 {
		pills = append(pills, pill(black, orange, fmt.Sprintf("%d to review", n)))
	}
	if n := m.unansweredMentions(); n > 0 {
		pills = append(pills, pill(black, purple, fmt.Sprintf("%d mentions", n)))
	}
	if n := m.failedPipelines(); n > 0 {
		pills = append(pills, pill(black, red, fmt.Sprintf("%d failed", n)))
	}
	if working > 0 {
		pills = append(pills, pill(black, green, fmt.Sprintf("%d working", working)))
	}
	if ready > 0 {
		pills = append(pills, pill(black, blue, fmt.Sprintf("%d your turn", ready)))
	}
	right := strings.Join(pills, " ")
	gap := max(1, m.width-lipgloss.Width(title)-lipgloss.Width(right)-1)
	top := ansi.Truncate(title+strings.Repeat(" ", gap)+right, m.width, "")

	var tabs []string
	for v := view(0); v < nViews; v++ {
		label := fmt.Sprintf("%d %s", v+1, viewNames[v])
		if b := m.badge(v); b != "" {
			label += " " + b
		}
		if v == m.view {
			tabs = append(tabs, sTabOn.Render(label))
		} else {
			tabs = append(tabs, sTabOff.Render(label))
		}
	}
	line := " " + strings.Join(tabs, " ")
	status := m.sourceStatus()
	if m.filtering || m.filter.Value() != "" {
		status = m.filter.View()
	}
	line2 := " " + m.subtitle()
	gap2 := max(1, m.width-lipgloss.Width(line2)-lipgloss.Width(status)-1)
	return top + "\n\n" + ansi.Truncate(line, m.width, "") + "\n" + ansi.Truncate(line2+strings.Repeat(" ", gap2)+status, m.width, "") + "\n"
}

func (m *Model) badge(v view) string {
	n := 0
	switch v {
	case vInbox:
		n = m.inboxCount()
	case vSessions:
		n = m.countLive()
	case vReview:
		n = m.pendingReviews()
	case vMine:
		n = len(m.gl.Mine)
	case vMentions:
		n = m.unansweredMentions()
	default:
		return ""
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprint(n)
}

func (m *Model) viewSourceOf() (source, bool) {
	switch m.view {
	case vReview, vMine, vInbox:
		return srcGitlab, true
	case vJira, vMentions:
		return srcJira, true
	case vConfluence:
		return srcConfluence, true
	}
	return 0, false
}

func (m *Model) sourceStatus() string {
	src, ok := m.viewSourceOf()
	if !ok {
		return ""
	}
	switch {
	case m.busy[src]:
		return sDim.Render(spinner[m.frame%len(spinner)] + " fetching " + viewSource(src))
	case m.errs[src] != nil:
		return sErr.Render("✗ " + viewSource(src) + " failed")
	case m.fetchedAt[src].IsZero():
		return ""
	}
	if a := age(m.fetchedAt[src]); a != "now" {
		return sDim.Render("updated " + a + " ago")
	}
	return sDim.Render("updated just now")
}

func (m *Model) subtitle() string {
	switch m.view {
	case vNotes:
		if m.notesPrefs.Recent {
			return sDim.Render("recently edited notes") + "  " + sKey.Render("v") + sDim.Render(" folders")
		}
		return sDim.Render("your vault") + "  " + sKey.Render("v") + sDim.Render(" recently edited") + "  " + sKey.Render("N") + sDim.Render(" on any ticket lists its notes, ") + sKey.Render("w") + sDim.Render(" writes one")
	case vInbox:
		s := "everyone waiting on you, oldest first"
		if m.showAll {
			s += " · incl. snoozed"
		}
		return sDim.Render(s) + "  " + sKey.Render("x") + sDim.Render(" snooze until it changes  ") + sKey.Render("a") + sDim.Render(" show snoozed")
	case vSessions:
		if m.allSessions {
			return sDim.Render("all sessions, last 7 days") + "  " + sKey.Render("a") + sDim.Render(" live only")
		}
		return sDim.Render("live sessions") + "  " + sKey.Render("a") + sDim.Render(" show closed too")
	case vReview:
		if m.showAll {
			return sDim.Render("all review requests") + "  " + sKey.Render("a") + sDim.Render(" hide approved")
		}
		return sDim.Render("review requests not yet approved by you") + "  " + sKey.Render("a") + sDim.Render(" show all")
	case vMine:
		return sDim.Render("open, then merged in the last 5 days")
	case vJira:
		s := "my sprint + in progress"
		if m.teamJira {
			s = "team sprint"
		}
		s += " · " + groupNames[m.jiraPrefs.Group] + " · by " + sortNames[m.jiraPrefs.Sort]
		if n := len(m.jiraPrefs.Hidden); n > 0 {
			s += fmt.Sprintf(" · %d statuses hidden", n)
		}
		hint := sKey.Render("a") + sDim.Render(" mine/team  ") + sKey.Render("d") + sDim.Render(" done")
		return sDim.Render(s) + "  " + hint
	case vMentions:
		hint := sKey.Render("x") + sDim.Render(" dismiss  ") + sKey.Render("a") + sDim.Render(" show handled")
		return sDim.Render("Jira and Confluence mentions, last 30 days") + "  " + hint
	case vConfluence:
		return sDim.Render("pages you edited recently")
	case vUsage:
		return sDim.Render("tokens from local transcripts incl. subagents, last 31 days, priced at API list rates")
	}
	return ""
}

func (m *Model) viewFooter() string {
	var keys [][2]string
	if m.prompt != nil {
		keys = [][2]string{{"enter", "open"}, {"esc", "back"}}
	} else if m.chooser != nil {
		keys = [][2]string{{"enter", "choose"}, {"esc", "back"}}
		if it, ok := m.chooser.selected(); ok && m.chooser.status != nil {
			keys = append(keys, [2]string{"tab", m.chooser.status(m, it)})
		}
	} else {
		keys = m.help()
		keys = append(keys, [2]string{"u", "open link"}, [2]string{"n", "new"}, [2]string{"1-9", "views"}, [2]string{"/", "filter"}, [2]string{"q", "quit"})
	}
	var parts []string
	for _, k := range keys {
		parts = append(parts, sKey.Render(k[0])+" "+sDim.Render(k[1]))
	}
	help := " " + strings.Join(parts, sRule.Render("  │  "))
	if m.flash != "" {
		st := sOK
		if m.flashErr {
			st = sErr
		}
		help = " " + st.Render(m.flash)
	}
	return ansi.Truncate(help, m.width, "…")
}

func (m *Model) help() [][2]string {
	switch m.view {
	case vNotes:
		return [][2]string{{"enter", "read"}, {"space", "fold"}, {"e", "edit"}, {"o", "Obsidian"}, {"v", "recent/tree"}, {"J", "ticket"}}
	case vInbox:
		return [][2]string{{"enter", "open"}, {"r", "review with claude"}, {"v", "threads"}, {"A", "approve"}, {"B", "rebase"}, {"x", "snooze"}, {"J", "ticket"}, {"c", "claude does what fits"}}
	case vSessions:
		return [][2]string{{"enter", "jump"}, {"R", "resume"}, {"J", "ticket"}, {"t", "set ticket"}, {"o", "MR"}, {"X", "restore"}}
	case vReview:
		return [][2]string{{"enter", "review with claude"}, {"o", "open"}, {"J", "ticket"}, {"B", "rebase"}, {"A", "approve"}, {"s", "seen"}, {"c", "sessions"}}
	case vMine:
		return [][2]string{{"enter", "session"}, {"v", "threads"}, {"f", "fix threads"}, {"o", "open"}, {"J", "ticket"}, {"B", "rebase"}, {"p", "pipeline"}, {"s", "seen"}}
	case vJira:
		return [][2]string{{"space", "fold"}, {"v", "layout"}, {"enter", "sessions"}, {"c", "claude (fits the state)"}, {"b", "summary"}, {"m", "move"}, {"f", "statuses"}, {"s", "sort"}, {"e", "copy brief"}, {"i", "images"}, {"o", "open"}}
	case vMentions:
		return [][2]string{{"enter", "open"}, {"x", "dismiss"}, {"J", "ticket"}, {"c", "draft reply"}}
	case vConfluence:
		return [][2]string{{"enter", "open"}, {"y", "copy link"}}
	}
	return nil
}

func (m *Model) count() int {
	switch m.view {
	case vNotes:
		return len(m.vis.notes)
	case vInbox:
		return len(m.vis.inbox)
	case vSessions:
		return len(m.vis.sessions)
	case vReview, vMine:
		return len(m.vis.mrs)
	case vJira:
		return len(m.vis.jira)
	case vMentions:
		return len(m.vis.mentions)
	case vConfluence:
		return len(m.vis.pages)
	case vUsage:
		return len(m.vis.usage)
	}
	return 0
}

type visible struct {
	inbox    []inboxItem
	notes    []nrow
	sessions []row
	mrs      []mrRow
	jira     []jrow
	mentions []mention
	pages    []atlassian.Page
	usage    []usageRow
}

func (m *Model) refilter() {
	terms := strings.Fields(strings.ToLower(m.filter.Value()))
	match := func(parts ...string) bool {
		hay := strings.ToLower(strings.Join(parts, " "))
		for _, t := range terms {
			if !strings.Contains(hay, t) {
				return false
			}
		}
		return true
	}
	m.vis.inbox = m.filterInbox(match)
	m.vis.notes = m.filterNotes(match, len(terms) > 0)
	m.vis.sessions = m.filterSessions(match)
	m.vis.mrs = m.filterMRs(match)
	m.vis.jira = m.buildJira(m.jiraIssues(match), len(terms) > 0)
	m.vis.mentions = m.filterMentions(match)
	m.vis.pages = m.filterPages(match)
	m.vis.usage = m.usageRows()
	for v := view(0); v < nViews; v++ {
		saved := m.view
		m.view = v
		m.cur[v].clamp(m.count())
		m.view = saved
	}
}

// start opens a claude window and jumps to it.
func (m *Model) start(sp launch.Spec) (tea.Model, tea.Cmd) {
	target, err := launch.Start(sp)
	if err != nil {
		m.setFlash(err.Error(), true)
		return m, nil
	}
	return m.goTo(target)
}

// goTo switches the tmux client, or outside tmux hands the target to main to attach.
func (m *Model) goTo(target string) (tea.Model, tea.Cmd) {
	if !tmux.Inside() {
		m.Result.Attach = target
		return m, tea.Quit
	}
	if err := tmux.Switch(target); err != nil {
		m.setFlash(err.Error(), true)
		return m, nil
	}
	if m.opts.Popup {
		return m, tea.Quit
	}
	return m, nil
}

// pickRepo opens the repo chooser with query already typed in; preferred repos
// are listed first.
func (m *Model) pickRepo(title, query string, preferred func(repos.Repo) (bool, string), then func(*Model, string, claude.Profile) (tea.Model, tea.Cmd)) {
	root := repos.Root()
	var first, rest []choice
	for _, r := range repos.ByPreference(m.repos, root) {
		ok, hint := false, ""
		if preferred != nil {
			ok, hint = preferred(r)
		}
		if hint == "" && r.Branch != "" {
			hint = r.Branch
		}
		ch := pathChoice(r.Dir, root, hint, r.Dir)
		if ok {
			first = append(first, ch)
		} else {
			rest = append(rest, ch)
		}
	}
	shift := map[string]int{}
	c := newChooser(title, append(first, rest...), func(m *Model, ch choice) (tea.Model, tea.Cmd) {
		dir := ch.value.(string)
		return then(m, dir, m.profileFor(dir, shift[dir]))
	})
	c.tab = func(_ *Model, ch choice) { shift[ch.value.(string)]++ }
	c.status = func(m *Model, ch choice) string {
		dir := ch.value.(string)
		return "profile: " + m.profileFor(dir, shift[dir]).Name
	}
	c.setQuery(query)
	if len(m.repos) == 0 {
		m.setFlash("still scanning repos…", false)
	}
	m.chooser = c
}

func (m *Model) profileFor(dir string, shift int) claude.Profile {
	ps := m.store.Profiles
	guess := launch.ProfileFor(dir, ps)
	for i, p := range ps {
		if p.Name == guess.Name {
			return ps[(i+shift)%len(ps)]
		}
	}
	return guess
}

func (m *Model) countLive() int {
	n := 0
	for _, r := range m.sessions {
		if r.live() {
			n++
		}
	}
	return n
}

func run(c *exec.Cmd) tea.Cmd {
	return func() tea.Msg {
		if out, err := c.CombinedOutput(); err != nil {
			return flashMsg{strings.TrimSpace(string(out)) + " " + err.Error(), true}
		}
		return nil
	}
}

func openURL(u string) tea.Cmd { return run(exec.Command("open", u)) }

func copyText(s, what string) tea.Cmd {
	return func() tea.Msg {
		c := exec.Command("pbcopy")
		c.Stdin = strings.NewReader(s)
		if err := c.Run(); err != nil {
			return flashMsg{err.Error(), true}
		}
		return flashMsg{"copied " + what, false}
	}
}

func age(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

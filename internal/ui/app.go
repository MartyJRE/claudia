package ui

import (
	"cmp"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/brief"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/launch"
	"github.com/MartyJRE/claudia/internal/links"
	"github.com/MartyJRE/claudia/internal/repos"
	"github.com/MartyJRE/claudia/internal/teams"
	"github.com/MartyJRE/claudia/internal/tmux"
	"github.com/MartyJRE/claudia/internal/vault"
	"github.com/MartyJRE/claudia/internal/watch"
)

type Options struct {
	Popup bool
	// PlanOnly shows just one session's plan review and quits when it closes,
	// for `claudia plan` in a tmux popup.
	PlanOnly bool
}

// Result is what main does after the TUI exits: outside tmux it attaches to Attach.
type Result struct {
	Attach  string
	Restart bool // re-exec after an update
}

type view int

const (
	vInbox view = iota
	vBoard
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

var viewNames = [nViews]string{"Inbox", "Board", "Sessions", "Review", "My MRs", "Jira", "Mentions", "Confluence", "Notes", "Usage"}

const detailHeight = 8

type row struct {
	claude.Session
	pane    *tmux.Pane
	host    string
	summary string // where the session stands, written by the watcher
	kind    string // what claudia started it for, e.g. review
	// summaryFor is the transcript update the summary was written from.
	summaryFor time.Time
}

func (r row) live() bool { return r.Live != nil }

// worker: one of claudia's own headless runs, kept apart from the user's sessions.
func (r row) worker() bool { return r.Live != nil && r.Live.Worker != "" }

func splitWorkers(rows []row) (sessions, workers []row) {
	for _, r := range rows {
		if r.worker() {
			workers = append(workers, r)
		} else {
			sessions = append(sessions, r)
		}
	}
	return sessions, workers
}

const (
	needsYouPrefix = "Needs you:"
	donePrefix     = "Done:"
	waitingPrefix  = "Waiting:"
)

// summarized: the summary was written from the session's latest turn.
func (r row) summarized(prefix string) bool {
	return strings.HasPrefix(r.summary, prefix) && r.Transcript != nil && r.summaryFor.Equal(r.Updated)
}

// done: stopped, and its summary of that very turn says nothing is left, so
// it's likely safe to close.
func (r row) done() bool {
	return r.live() && !r.Live.Status.Working() && !r.needsYou() && r.summarized(donePrefix)
}

// needsYou: Claude is waiting on a prompt, or it stopped and its summary of
// that very turn says it's asking you something.
func (r row) needsYou() bool {
	if !r.live() || r.Live.Status.Working() {
		return false
	}
	if r.Live.Status == claude.StatusWaiting {
		return true
	}
	return r.summarized(needsYouPrefix)
}

type Model struct {
	opts   Options
	Result Result

	km     keymap
	marked map[string]bool // session ids marked for a split

	store      *claude.Store
	links      *links.Links
	atl        *atlassian.Client
	atlErr     error
	visual     *visualSel // rows picked with V
	background bool       // the action being run was C: start claude without switching to it

	sessions     []row
	workers      []row // claudia's own runs, only ever shown in the Sessions tab
	usage        []claude.DayModel
	usageScanner *claude.UsageScanner
	usageLoaded  bool
	usageLoading bool
	usageAt      time.Time
	ledger       *claude.Ledger
	costs        map[string]claude.TicketCost
	jiraPrefs    jiraPrefs
	boardPrefs   boardPrefs
	boardFocus   bool            // stepped into the Board: h/l move columns, not tabs
	boardCol     int             // the Board column the cursor is in
	boardOff     int             // the first column shown when they don't all fit
	boardCur     [nStages]cursor // each column's cursor while another has it
	boardLater   int             // Up next tickets folded away as later sprints
	boardNeeds   int
	sessLoaded   bool
	sessLoading  bool
	gl           gitlab.Snapshot
	jira         atlassian.JiraSnapshot
	conf         atlassian.ConfluenceSnapshot
	teams        teams.Snapshot
	tm           *teams.Client
	postMRs      map[string]bool // done-ness of MRs in Teams posts that claudia doesn't track
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
	sessPrefs    sessionPrefs
	listPrefs    listPrefs
	brief        brief.Brief
	briefAt      time.Time
	briefPending time.Time
	updateAvail  bool
	updateAt     time.Time
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
	upd       *updateView
	rel       *releasesView

	confirmKey string
	confirmAt  time.Time

	frame         int
	animating     bool
	width, height int
	plan          *planReview
	settings      *settingsPage
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
		boardPrefs: loadBoardPrefs(),
		ledger:     claude.OpenLedger(),
		vault:      newVault(),
		notesPrefs: loadNotesPrefs(),
		sessPrefs:  loadSessionPrefs(),
		listPrefs:  loadListPrefs(),
		view:       vSessions,
		links:      links.Load(),
		filter:     f,
		dismissed:  loadDismissed(),
	}
	var problems []string
	m.km, problems = newKeymap(getConfig().Keys)
	m.atl, m.atlErr = atlassian.New()
	m.tm, _ = newTeams()
	m.seen, m.seenExisted = loadSeen()
	m.loadCaches()
	m.restore = pendingRestore()
	m.usageScanner = claude.NewUsageScanner(m.store.Profiles, 31)
	m.costs = ticketCosts(m.ledger)
	m.reloadBrief()
	if watch.Notifier() == "" {
		m.setFlash("notifications open Script Editor when clicked: run `claudia setup` once to install the notification helper", true)
		m.flashAt = time.Now().Add(20 * time.Second)
	}
	if len(problems) > 0 {
		m.setFlash("config keys: "+strings.Join(problems, "; "), true)
		m.flashAt = time.Now().Add(20 * time.Second)
	}
	return m
}

type tickMsg time.Time
type flashMsg struct {
	text string
	err  bool
}
type refreshMsg source

func (m *Model) Init() tea.Cmd {
	m.sessLoading = true
	return tea.Batch(m.loadSessions(), m.refreshStale(), m.maybeUsage(), m.refreshVault(), tick(), m.animate())
}

func tick() tea.Cmd {
	return tickAfter(1500*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
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
	model, cmd := m.update(msg)
	if a := m.animate(); a != nil {
		cmd = tea.Batch(cmd, a)
	}
	return model, cmd
}

func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.reader != nil {
			m.renderReader()
		}
		if m.upd != nil {
			m.renderUpdateNotes()
		}
		if m.rel != nil {
			m.renderReleases()
		}
		m.filter.Width = msg.Width / 2
		return m, nil
	case sessionsMsg:
		m.sessions, m.workers = splitWorkers(msg)
		m.sessLoaded, m.sessLoading = true, false
		m.restore = notLive(m.restore, m.sessions)
		saveLive(m.sessions, m.restore != nil)
		m.reindex()
		return m, nil
	case planSentMsg:
		return m.planSent(msg)
	case fetched:
		m.applyFetched(msg)
		if msg.err != nil && msg.src != srcRepos {
			m.setFlash(viewSource(msg.src)+": "+msg.err.Error(), true)
		}
		m.reindex()
		if msg.src == srcTeams {
			return m, m.lookupPostMRs()
		}
		return m, nil
	case postMRsMsg:
		m.applyPostMRs(msg)
		m.refilter()
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
		m.reloadBrief()
		if time.Since(m.updateAt) > 30*time.Second {
			m.updateAt, m.updateAvail = time.Now(), updateAvailable()
			// An update or a crash may have ended the watcher; the summaries need it.
			spawnWatcher()
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
		if !m.animates() {
			m.animating = false
			return m, nil
		}
		m.frame++
		return m, frame()
	case flashMsg:
		m.setFlash(msg.text, msg.err)
		return m, nil
	case repoCreatedMsg:
		return m.repoCreated(msg)
	case refreshMsg:
		m.fetchedAt[source(msg)] = time.Time{}
		return m, m.refreshStale()
	case usageMsg:
		m.usage, m.usageLoaded, m.usageLoading = msg, true, false
		m.usageAt = time.Now()
		m.updateLedger()
		m.refilter()
		return m, nil
	case imagesMsg:
		return m, m.openImages(msg)
	case startMsg:
		m.background = msg.background
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
	case upPlanMsg, upLogMsg, upDoneMsg:
		if m.upd != nil {
			return m, m.updateMsg(msg)
		}
		return m, nil
	case releasesMsg:
		if m.rel != nil {
			m.releasesLoaded(msg)
		}
		return m, nil
	case settingEditedMsg:
		return m, m.settingEdited(msg)
	case mrLoadedMsg:
		if msg.err != nil {
			m.setFlash(msg.err.Error(), true)
			return m, nil
		}
		m.flash = ""
		return m.reviewWithClaude(msg.mr)
	case tea.KeyMsg:
		switch {
		case m.rel != nil:
			return m.updateReleases(msg)
		case m.upd != nil:
			return m.updateKeys(msg)
		case m.reader != nil:
			return m.updateReader(msg)
		case m.plan != nil:
			return m.updatePlan(msg)
		case m.settings != nil:
			return m.updateSettings(msg)
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
	return [...]string{"GitLab", "Jira", "Confluence", "repos", "Teams"}[s]
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
	// Holds through the repo or intent chooser the action may open.
	m.background = strings.HasSuffix(m.action(k), "_bg")
	if m.visual != nil {
		if mm, cmd, done := m.updateVisual(k); done {
			return mm, cmd
		}
	}
	if m.view == vBoard {
		if mm, cmd, done := m.boardNav(k); done {
			return mm, cmd
		}
	}
	switch m.action(k) {
	case "global.visual":
		if !visualView(m.view) || m.count() == 0 {
			m.setFlash("nothing to select here", true)
			return m, nil
		}
		m.visual = &visualSel{view: m.view, anchor: c.pos}
		return m, nil
	case "global.quit":
		return m, tea.Quit
	case "global.back":
		if m.filter.Value() != "" {
			m.filter.SetValue("")
			m.refilter()
			return m, nil
		}
		return m, tea.Quit
	case "global.up":
		c.pos = max(0, c.pos-1)
		return m, nil
	case "global.down":
		c.pos = min(m.count()-1, c.pos+1)
		return m, nil
	case "global.top":
		c.pos = 0
		return m, nil
	case "global.bottom":
		c.pos = m.count() - 1
		return m, nil
	case "global.next_view":
		m.switchView((m.view + 1) % nViews)
		return m, nil
	case "global.prev_view":
		m.switchView((m.view + nViews - 1) % nViews)
		return m, nil
	case "global.assistant":
		return m.openAssistant()
	case "global.update":
		return m, m.startUpdate()
	case "global.releases":
		return m, m.openReleases()
	case "global.settings":
		return m, m.openSettings()
	case "global.open_link":
		return m, m.askLink()
	case "global.ticket_notes":
		m.chooseNotes()
		return m, nil
	case "global.write_note":
		return m, m.writeNote()
	case "global.new_session":
		m.newSession()
		return m, nil
	case "global.filter":
		m.filtering = true
		return m, m.filter.Focus()
	case "global.refresh":
		m.setFlash("refreshing…", false)
		return m, m.refreshAll()
	}
	if len(k) == 1 && k[0] >= '1' && k[0] <= '9' && int(k[0]-'1') < int(nViews) {
		m.switchView(view(k[0] - '1'))
		return m, nil
	}
	switch m.view {
	case vNotes:
		return m.keyNotes(k)
	case vInbox:
		return m.keyInbox(k)
	case vBoard:
		return m.keyBoard(k)
	case vUsage:
		return m.keyUsage(k)
	case vSessions:
		return m.keySessions(k)
	case vReview, vMine:
		return m.keyMRs(k)
	case vJira:
		return m.keyJira(k)
	case vMentions:
		return m.keyMentions(k)
	}
	return m.keyConfluence(k)
}

func (m *Model) switchView(v view) {
	if v == m.view {
		return
	}
	m.view = v
	m.boardFocus = false
	m.visual = nil
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
	if m.rel != nil {
		return m.viewReleases()
	}
	if m.upd != nil {
		return m.viewUpdate()
	}
	if m.reader != nil {
		return m.viewReader()
	}
	if m.plan != nil {
		return m.viewPlan()
	}
	if m.settings != nil {
		return m.viewSettings()
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
	case vBoard:
		return m.listBoard(h)
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
	case vBoard:
		return m.detailBoard()
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
	var waiting, working, ready, done int
	for _, r := range m.sessions {
		switch {
		case !r.live():
		case r.needsYou():
			waiting++
		case r.Live.Status.Working():
			working++
		case r.done():
			done++
		default:
			ready++
		}
	}
	title := sAccent.Render(" ✻ claudia")
	var pills []string
	if m.updateAvail {
		pills = append(pills, pill(black, cyan, "⬆ update · ctrl+u"))
	}
	if waiting > 0 {
		pills = append(pills, pill(black, yellow, fmt.Sprintf("%d needs you", waiting)))
	}
	if n := m.pendingReviews(); n > 0 {
		pills = append(pills, pill(black, orange, fmt.Sprintf("%d to review", n)))
	}
	if n := m.unansweredMentions(); n > 0 {
		pills = append(pills, pill(black, purple, fmt.Sprintf("%d mentions", n)))
	}
	if n := m.failedOpen(); n > 0 {
		pills = append(pills, pill(black, red, failedPill(n)))
	}
	if working > 0 {
		pills = append(pills, pill(black, green, fmt.Sprintf("%d working", working)))
	}
	if ready > 0 {
		pills = append(pills, pill(black, blue, fmt.Sprintf("%d your turn", ready)))
	}
	if done > 0 {
		pills = append(pills, pill(black, commentGrey, fmt.Sprintf("%d done", done)))
	}
	right := strings.Join(pills, " ")
	gap := max(1, m.width-lipgloss.Width(title)-lipgloss.Width(right)-1)
	top := ansi.Truncate(title+strings.Repeat(" ", gap)+right, m.width, "")

	var tabs []string
	for v := view(0); v < nViews; v++ {
		// Only nine views have a digit; the rest are a tab away.
		label := viewNames[v]
		if v < 9 {
			label = fmt.Sprintf("%d %s", v+1, label)
		}
		if b := m.badge(v); b != "" {
			label += " " + b
		}
		if v == m.view {
			tabs = append(tabs, tab(label, true))
		} else {
			tabs = append(tabs, tab(label, false))
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
	case vBoard:
		n = m.boardNeeds
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
	case vJira, vMentions, vBoard:
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
		return sDim.Render(m.spin() + " fetching " + viewSource(src))
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
			return sDim.Render("your vault · recently edited")
		}
		return sDim.Render("your vault · folders") + "  " + sKey.Render(m.key("global.ticket_notes")) + sDim.Render(" on any ticket lists its notes, ") + sKey.Render(m.key("global.write_note")) + sDim.Render(" writes one")
	case vInbox:
		s := "everyone waiting on you" + m.listMode(vInbox)
		if m.showAll {
			s += " · incl. snoozed"
		}
		return sDim.Render(s) + "  " + sKey.Render(m.key("inbox.snooze")) + sDim.Render(" snooze until it changes  ") + sKey.Render(m.key("inbox.all")) + sDim.Render(" show snoozed")
	case vSessions:
		layout := "  " + sKey.Render(m.key("sessions.group")) + sDim.Render(" layout")
		g := " · " + sessGroupNames[m.sessPrefs.Group]
		if m.allSessions {
			return sDim.Render("all sessions, last 7 days"+g) + layout + "  " + sKey.Render(m.key("sessions.all")) + sDim.Render(" live only")
		}
		return sDim.Render("live sessions"+g) + layout + "  " + sKey.Render(m.key("sessions.all")) + sDim.Render(" show closed too")
	case vReview:
		if m.showAll {
			return sDim.Render("all review requests"+m.listMode(vReview)) + "  " + sKey.Render(m.key("mrs.all")) + sDim.Render(" hide approved")
		}
		return sDim.Render("review requests not yet approved by you"+m.listMode(vReview)) + "  " + sKey.Render(m.key("mrs.all")) + sDim.Render(" show all")
	case vMine:
		return sDim.Render("open, then merged in the last 5 days" + m.listMode(vMine))
	case vJira:
		s := "my sprint + in progress"
		if m.teamJira {
			s = "team sprint"
		}
		s += " · " + groupNames[m.jiraPrefs.Group] + " · by " + sortNames[m.jiraPrefs.Sort]
		if n := len(m.jiraPrefs.Hidden); n > 0 {
			s += fmt.Sprintf(" · %d statuses hidden", n)
		}
		hint := sKey.Render(m.key("jira.team")) + sDim.Render(" mine/team  ") + sKey.Render(m.key("jira.done")) + sDim.Render(" done")
		return sDim.Render(s) + "  " + hint
	case vMentions:
		hint := sKey.Render(m.key("mentions.dismiss")) + sDim.Render(" dismiss  ") + sKey.Render(m.key("mentions.all")) + sDim.Render(" show handled")
		return sDim.Render("Jira and Confluence mentions, last 30 days"+m.listMode(vMentions)) + "  " + hint
	case vConfluence:
		return sDim.Render("pages you edited recently" + m.listMode(vConfluence))
	case vBoard:
		s := "my tickets"
		if m.boardPrefs.Team {
			s = "team sprint"
		}
		if sp := m.currentSprint(); sp != "" {
			s += " in " + sp
		}
		if m.boardPrefs.Done {
			s += " incl. done"
		}
		hint := sKey.Render(m.key("board.team")) + sDim.Render(" mine/team  ") + sKey.Render(m.key("board.done")) + sDim.Render(" done  ") + sKey.Render(m.key("board.later")) + sDim.Render(" future sprints  ")
		if m.boardFocus {
			hint += sKey.Render("↑") + sDim.Render(" on a top card steps out")
		} else {
			hint += sKey.Render("↓") + sDim.Render(" steps in")
		}
		return sDim.Render(s+" by stage, worked out from Jira, MRs and sessions") + "  " + hint
	case vUsage:
		layout := "  " + sKey.Render(m.key("usage.layout")) + sDim.Render(" layout")
		if m.usageByTicket() {
			return sDim.Render("cost per ticket under its epic, kept in a ledger that outlives transcript cleanup") + layout
		}
		return sDim.Render("tokens from local transcripts incl. subagents, last 31 days, priced at API list rates") + layout
	}
	return ""
}

func (m *Model) viewFooter() string {
	var keys [][2]string
	if m.prompt != nil {
		keys = [][2]string{{"enter", cmp.Or(m.prompt.verb, "open")}, {"esc", "back"}}
	} else if m.chooser != nil {
		keys = [][2]string{{"enter", "choose"}, {"esc", "back"}}
		if it, ok := m.chooser.selected(); ok && m.chooser.status != nil {
			if st := m.chooser.status(m, it); st != "" {
				keys = append(keys, [2]string{"tab", st})
			}
		}
	} else if lo, hi, ok := m.visualRange(); ok {
		keys = [][2]string{{"j/k", "extend"}, m.hint("visual.claude", fmt.Sprintf("claude on these %d", hi-lo+1)),
			m.hint("visual.claude_bg", "in the background"), {"esc", "cancel"}}
	} else {
		keys = m.withLayout(m.help())
		keys = append(keys, m.hints("global.assistant", "global.open_link", "global.new_session")...)
		keys = append(keys, [2]string{"1-9", "views"})
		keys = append(keys, m.hints("global.filter", "global.quit")...)
	}
	if m.flash != "" {
		st := sOK
		if m.flashErr {
			st = sErr
		}
		return ansi.Truncate(" "+st.Render(m.flash), m.width, "…")
	}
	// Wraps rather than truncates, so no key falls off a narrow terminal.
	sep := sRule.Render("  │  ")
	var lines []string
	line := ""
	for _, k := range keys {
		part := sKey.Render(k[0]) + " " + sDim.Render(k[1])
		if line != "" && ansi.StringWidth(line+sep+part) > m.width {
			lines = append(lines, line)
			line = ""
		}
		if line == "" {
			line = " " + part
		} else {
			line += sep + part
		}
	}
	return strings.Join(append(lines, line), "\n")
}

func (m *Model) help() [][2]string {
	switch m.view {
	case vNotes:
		return m.hints("notes.read", "notes.fold", "notes.edit", "notes.obsidian", "notes.layout", "notes.ticket")
	case vInbox:
		return m.hints("inbox.open", "inbox.brief", "inbox.review", "inbox.threads", "inbox.approve", "inbox.rebase", "inbox.snooze", "inbox.ticket", "inbox.claude", "inbox.claude_bg")
	case vSessions:
		return m.hints("sessions.jump", "sessions.fold", "sessions.group", "sessions.resume", "sessions.close", "sessions.mark", "sessions.split", "sessions.unsplit", "sessions.ticket", "sessions.set_ticket", "sessions.mr", "sessions.restore")
	case vReview:
		return append([][2]string{m.hint("mrs.primary", "review with claude"), m.hint("mrs.claude_bg", "review in the background")},
			m.hints("mrs.open", "mrs.ticket", "mrs.rebase", "mrs.approve", "mrs.seen", "mrs.sessions")...)
	case vMine:
		return append([][2]string{m.hint("mrs.primary", "session")},
			m.hints("mrs.threads", "mrs.fix", "mrs.claude_bg", "mrs.open", "mrs.ticket", "mrs.rebase", "mrs.pipeline", "mrs.seen")...)
	case vJira:
		ids := []string{"jira.fold", "jira.layout", "jira.sessions", "jira.claude", "jira.claude_bg", "jira.summary", "jira.move", "jira.statuses", "jira.sort", "jira.copy_brief", "jira.images", "jira.open"}
		if !imagesShown() {
			ids = slices.DeleteFunc(ids, func(id string) bool { return id == "jira.images" })
		}
		return m.hints(ids...)
	case vMentions:
		return m.hints("mentions.open", "mentions.dismiss", "mentions.ticket", "mentions.reply", "mentions.reply_bg")
	case vConfluence:
		return m.hints("confluence.open", "confluence.copy")
	case vBoard:
		if !m.boardFocus {
			return append([][2]string{{"↓", "step in"}}, m.hints("board.team", "board.done", "board.later")...)
		}
		return append([][2]string{{m.key("board.left") + "/" + m.key("board.right"), "columns"}},
			m.hints("board.sessions", "board.claude", "board.claude_bg", "board.open", "board.mr", "board.threads", "board.move", "board.copy")...)
	case vUsage:
		return m.hints("usage.layout")
	}
	return nil
}

func (m *Model) count() int {
	switch m.view {
	case vNotes:
		return len(m.vis.notes)
	case vInbox:
		return len(m.vis.inboxRows)
	case vSessions:
		return len(m.vis.sessions)
	case vReview, vMine:
		return len(m.vis.mrRows)
	case vJira:
		return len(m.vis.jira)
	case vMentions:
		return len(m.vis.mentionRows)
	case vConfluence:
		return len(m.vis.pageRows)
	case vUsage:
		if m.usageByTicket() {
			return len(m.vis.costRows)
		}
		return len(m.vis.usage)
	case vBoard:
		if m.boardCol < len(m.vis.board) {
			return len(m.vis.board[m.boardCol])
		}
	}
	return 0
}

type visible struct {
	board    [][]boardCard // per shown column
	costRows []costRow
	inbox    []inboxItem
	notes    []nrow
	sessions []srow
	mrs      []mrRow
	jira     []jrow
	mentions []mention
	pages    []atlassian.Page
	usage    []usageRow

	inboxRows, mrRows, mentionRows, pageRows []lrow
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
	selected := ""
	if c := m.cur[vSessions].pos; c >= 0 && c < len(m.vis.sessions) {
		selected = m.vis.sessions[c].id()
	}
	m.vis.sessions = m.sessionRows(match, len(terms) > 0)
	if i := slices.IndexFunc(m.vis.sessions, func(r srow) bool { return r.id() == selected }); selected != "" && i >= 0 {
		m.cur[vSessions].pos = i
	}
	m.vis.mrs = m.filterMRs(match)
	m.vis.jira = m.buildJira(m.jiraIssues(match), len(terms) > 0)
	m.vis.mentions = m.filterMentions(match)
	m.vis.pages = m.filterPages(match)
	m.vis.inboxRows = m.groupRows(vInbox, len(m.vis.inbox), len(terms) > 0)
	m.vis.mrRows = m.groupRows(m.view, len(m.vis.mrs), len(terms) > 0)
	m.vis.mentionRows = m.groupRows(vMentions, len(m.vis.mentions), len(terms) > 0)
	m.vis.pageRows = m.groupRows(vConfluence, len(m.vis.pages), len(terms) > 0)
	m.vis.usage = m.usageRows()
	m.vis.costRows = m.costRows()
	m.refilterBoard(match)
	for v := view(0); v < nViews; v++ {
		saved := m.view
		m.view = v
		m.cur[v].clamp(m.count())
		m.view = saved
	}
}

// start opens a claude window and jumps to it.
func (m *Model) start(sp launch.Spec) (tea.Model, tea.Cmd) {
	sp = m.colored(sp, nil)
	target, err := startSession(sp)
	if err != nil {
		m.setFlash(err.Error(), true)
		return m, nil
	}
	if m.background {
		m.background = false
		m.setFlash("started "+sp.Name+" in the background · you'll get a ping when it needs you", false)
		return m, m.loadSessions()
	}
	return m.goTo(target)
}

// colored gives a session without a colour the one least used among live
// sessions (and those in also), so the ones running side by side differ.
func (m *Model) colored(sp launch.Spec, also []string) launch.Spec {
	used := map[string]int{}
	for _, c := range also {
		used[c]++
	}
	for _, r := range m.sessions {
		if r.ID == sp.ID && r.Transcript != nil && r.Color != "" {
			return sp
		}
		if r.live() && r.Transcript != nil && r.Color != "" {
			used[r.Color]++
		}
	}
	sp.Color = launch.Colors[0]
	for _, c := range launch.Colors {
		if used[c] < used[sp.Color] {
			sp.Color = c
		}
	}
	return sp
}

// goTo switches the tmux client, or outside tmux hands the target to main to attach.
func (m *Model) goTo(target string) (tea.Model, tea.Cmd) {
	if !tmux.Inside() {
		m.Result.Attach = target
		return m, tea.Quit
	}
	if err := switchClient(target); err != nil {
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
	c.tab = func(_ *Model, ch choice) {
		if dir, ok := ch.value.(string); ok {
			shift[dir]++
		}
	}
	c.status = func(m *Model, ch choice) string {
		dir, ok := ch.value.(string)
		if !ok {
			return ""
		}
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
		if out, err := cmdCombinedOutput(c); err != nil {
			return flashMsg{strings.TrimSpace(string(out)) + " " + err.Error(), true}
		}
		return nil
	}
}

// openURL opens a link; a Teams one goes to the desktop app through its
// msteams: scheme, and to the browser only when that fails.
func openURL(u string) tea.Cmd {
	app, ok := teamsAppURL(u)
	if !ok {
		return run(exec.Command("open", u))
	}
	return func() tea.Msg {
		if _, err := cmdCombinedOutput(exec.Command("open", app)); err == nil {
			return nil
		}
		return run(exec.Command("open", u))()
	}
}

func teamsAppURL(u string) (string, bool) {
	for _, host := range []string{"https://teams.microsoft.com/", "https://teams.cloud.microsoft/"} {
		if rest, ok := strings.CutPrefix(u, host); ok && strings.HasPrefix(rest, "l/") {
			return "msteams:/" + rest, true
		}
	}
	return "", false
}

func copyText(s, what string) tea.Cmd {
	return func() tea.Msg {
		c := exec.Command("pbcopy")
		c.Stdin = strings.NewReader(s)
		if err := cmdRun(c); err != nil {
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

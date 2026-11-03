package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/repos"
)

func TestViewsRenderShell(t *testing.T) {
	m := newModelShell(t)
	header := []string{"✻ claudia", "1 needs you", "1 to review", "1 mentions", "2 failed", "1 working", "1 your turn",
		"1 Inbox 4", "2 Sessions 3", "3 Review 1", "4 My MRs 1", "6 Mentions 1", "9 Usage"}
	for _, c := range []struct {
		v     view
		wants []string
		lacks []string
	}{
		{vInbox, []string{"everyone waiting on you, oldest first", "✻ brief", "mention", "threads", "review", "claude",
			"billing !21", "payments !11", "1 open: rename this", "Eve Smith", "waiting since"}, []string{"!12", "!13"}},
		{vSessions, []string{"live sessions · by repo", "◆ needs you", "working", "● your turn", "○ closed",
			"Recently closed", "export csv", "invoice pdf ·personal", "Opus 5.5", "Sonnet 4.5", "tmux work:3", "ghostty", "external",
			"1 session(s) · 1 live · 1 needs you"}, nil},
		{vReview, []string{"review requests not yet approved by you", "!11 payments", "Draft: wip billing", "✓ passed", "1 needed",
			"feat/ABC-1/export → devel", "ABC-1 Payments export · Dev in progress"}, []string{"!13", "REVIEWERS"}},
		{vMine, []string{"open, then merged in the last 5 days", "REVIEWERS", "carol, ✓ dave", "✗ failed", "1, 1 left",
			"1 open threads", "!22 ledger", "reviewers: carol, ✓ dave", "…/pay/export.go:7 rename this  (2 replies, last me)"}, []string{"AUTHOR"}},
	} {
		m.switchView(c.v)
		out := m.View()
		s := plainShell(out)
		containsAllShell(t, viewNames[c.v], s, append(header, c.wants...)...)
		lacksShell(t, viewNames[c.v], s, c.lacks...)
		if h := lipgloss.Height(out); h != m.height {
			t.Errorf("%s: %d lines, want the full %d", viewNames[c.v], h, m.height)
		}
		for i, l := range strings.Split(out, "\n") {
			if w := ansi.StringWidth(l); w > m.width {
				t.Errorf("%s: line %d is %d wide, terminal is %d", viewNames[c.v], i, w, m.width)
			}
		}
	}
}

func TestViewNoSizeShell(t *testing.T) {
	m := newModelShell(t)
	m.width = 0
	if got := m.View(); got != "" {
		t.Errorf("before the first WindowSizeMsg: %q", got)
	}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	if m.width != 120 || m.height != 30 || m.filter.Width != 60 {
		t.Errorf("size %dx%d filter %d", m.width, m.height, m.filter.Width)
	}
	m.view = vReview
	if h := lipgloss.Height(m.View()); h != 30 {
		t.Errorf("height %d", h)
	}
}

func TestFooterWrapsShell(t *testing.T) {
	for _, v := range []view{vInbox, vSessions, vReview, vMine} {
		for _, w := range []int{50, 90, 400} {
			m := newModelShell(t)
			m.width = w
			m.switchView(v)
			raw := m.viewFooter()
			lines := strings.Split(raw, "\n")
			for i, l := range lines {
				if lw := ansi.StringWidth(l); lw > w && strings.Contains(l, "│") {
					t.Errorf("%s at %d: line %d is %d wide", viewNames[v], w, i, lw)
				}
			}
			if w == 400 && len(lines) != 1 {
				t.Errorf("%s: a wide terminal fits the footer on one line, got %d", viewNames[v], len(lines))
			}
			if w == 50 && len(lines) < 3 {
				t.Errorf("%s: a narrow terminal should wrap, got %d lines", viewNames[v], len(lines))
			}
			s := plainShell(raw)
			for _, k := range append(m.help(), [2]string{"1-9", "views"}, [2]string{"q", "quit"}, [2]string{"/", "filter"}) {
				if !strings.Contains(s, k[0]+" "+k[1]) {
					t.Errorf("%s at %d: key %q %q fell off the footer:\n%s", viewNames[v], w, k[0], k[1], s)
				}
			}
		}
	}
}

func TestFooterModesShell(t *testing.T) {
	m := newModelShell(t)
	m.setFlash("approved !11", false)
	if got := plainShell(m.viewFooter()); got != " approved !11" {
		t.Errorf("flash replaces the keys: %q", got)
	}
	m.width = 10
	m.setFlash("a very long error message", true)
	if got := plainShell(m.viewFooter()); ansi.StringWidth(got) > 10 || !strings.HasSuffix(got, "…") {
		t.Errorf("long flash is cut to the width: %q", got)
	}
	m.width, m.flash = 160, ""

	m.prompt = &promptBox{}
	if got := plainShell(m.viewFooter()); got != " enter open  │  esc back" {
		t.Errorf("prompt footer: %q", got)
	}
	m.prompt = nil
	m.pickRepo("new session in", "", nil, nil)
	m.repos = []repos.Repo{{Dir: "/code/payments"}}
	m.pickRepo("new session in", "", nil, nil)
	m.flash = ""
	got := plainShell(m.viewFooter())
	containsAllShell(t, "chooser footer", got, "enter choose", "esc back", "tab profile: ")
}

func TestViewSwitchingShell(t *testing.T) {
	m := newModelShell(t)
	m.view = vSessions
	for _, c := range []struct {
		key  string
		want view
	}{
		{"tab", vReview},
		{"l", vMine},
		{"right", vJira},
		{"shift+tab", vMine},
		{"h", vReview},
		{"left", vSessions},
		{"1", vInbox},
		{"shift+tab", vUsage},
		{"tab", vInbox},
		{"9", vUsage},
		{"4", vMine},
		{"0", vMine},
	} {
		switch c.key {
		case "right":
			m.Update(tea.KeyMsg{Type: tea.KeyRight})
		case "left":
			m.Update(tea.KeyMsg{Type: tea.KeyLeft})
		default:
			pressShell(m, c.key)
		}
		if m.view != c.want {
			t.Fatalf("after %q: view %s, want %s", c.key, viewNames[m.view], viewNames[c.want])
		}
	}
}

func TestSwitchViewClearsFilterShell(t *testing.T) {
	m := newModelShell(t)
	m.switchView(vReview)
	pressShell(m, "/")
	typeShell(m, "billing")
	pressShell(m, "enter")
	if len(m.vis.mrs) != 1 {
		t.Fatalf("filter: %d rows", len(m.vis.mrs))
	}
	pressShell(m, "tab")
	if m.filter.Value() != "" {
		t.Errorf("filter survived the view switch: %q", m.filter.Value())
	}
	pressShell(m, "shift+tab")
	if len(m.vis.mrs) != 2 {
		t.Errorf("rows after the filter went: %d", len(m.vis.mrs))
	}
}

func TestCursorShell(t *testing.T) {
	m := newModelShell(t)
	m.switchView(vInbox)
	n := len(m.vis.inbox)
	if n != 4 {
		t.Fatalf("inbox has %d items", n)
	}
	for _, c := range []struct {
		key  string
		want int
	}{
		{"k", 0}, {"j", 1}, {"down", 2}, {"j", 3}, {"j", 3}, {"up", 2}, {"g", 0}, {"G", 3}, {"home", 0}, {"end", 3},
	} {
		pressShell(m, c.key)
		if got := m.cur[vInbox].pos; got != c.want {
			t.Fatalf("after %q: cursor %d, want %d", c.key, got, c.want)
		}
	}
	if m.cur[vReview].pos != 0 {
		t.Errorf("each view keeps its own cursor")
	}
	m.gl.Mine, m.gl.Merged, m.gl.Review = nil, nil, nil
	m.sessions = nil
	m.jira.Mentions = nil
	m.reindex()
	if m.cur[vInbox].pos != 0 {
		t.Errorf("cursor must clamp when the list shrinks, got %d", m.cur[vInbox].pos)
	}
}

func TestFilterShell(t *testing.T) {
	m := newModelShell(t)
	m.switchView(vInbox)
	pressShell(m, "/")
	if !m.filtering {
		t.Fatal("/ starts filtering")
	}
	typeShell(m, "PAY review")
	if len(m.vis.inbox) != 1 || m.vis.inbox[0].kind != kReview {
		t.Fatalf("terms are ANDed, case-insensitively: %+v", m.vis.inbox)
	}
	if !strings.Contains(screenShell(m), "/ PAY review") {
		t.Errorf("header shows the filter while typing")
	}
	pressShell(m, "j")
	if m.filter.Value() != "PAY reviewj" {
		t.Errorf("letters go to the filter while typing, got %q", m.filter.Value())
	}
	pressShell(m, "backspace", "down")
	if m.filter.Value() != "PAY review" {
		t.Errorf("arrows move the cursor without typing: %q", m.filter.Value())
	}
	pressShell(m, "enter")
	if m.filtering || m.filter.Value() != "PAY review" {
		t.Errorf("enter keeps the filter: filtering=%v value=%q", m.filtering, m.filter.Value())
	}
	if cmd := pressShell(m, "esc"); cmd != nil || m.filter.Value() != "" || len(m.vis.inbox) != 4 {
		t.Errorf("esc with a filter clears it rather than quitting")
	}
	if cmd := pressShell(m, "esc"); !isQuitShell(cmd) {
		t.Errorf("esc without a filter quits")
	}

	pressShell(m, "/")
	typeShell(m, "zzz")
	pressShell(m, "esc")
	if m.filtering || m.filter.Value() != "" || len(m.vis.inbox) != 4 {
		t.Errorf("esc while typing drops the filter")
	}
}

func TestQuitShell(t *testing.T) {
	for _, k := range []string{"q", "ctrl+c"} {
		m := newModelShell(t)
		if !isQuitShell(pressShell(m, k)) {
			t.Errorf("%s should quit", k)
		}
	}
}

func TestMessagesShell(t *testing.T) {
	m := newModelShell(t)

	m.Update(flashMsg{"copied MR link", false})
	if m.flash != "copied MR link" || m.flashErr {
		t.Errorf("flashMsg: %q %v", m.flash, m.flashErr)
	}

	m.Update(fetched{src: srcGitlab, err: errors.New("401 Unauthorized")})
	if m.flash != "GitLab: 401 Unauthorized" || !m.flashErr || m.errs[srcGitlab] == nil {
		t.Errorf("fetch error: %q", m.flash)
	}
	if m.fetchedAt[srcGitlab].IsZero() {
		t.Errorf("a failed fetch still backs off until the next interval")
	}
	m.flash = ""
	m.Update(fetched{src: srcRepos, err: errors.New("walk")})
	if m.flash != "" {
		t.Errorf("repo scan errors stay quiet: %q", m.flash)
	}

	snap := glSnapshotShell()
	snap.Review = snap.Review[:1]
	m.Update(fetched{src: srcGitlab, data: snap})
	if m.errs[srcGitlab] != nil || len(m.gl.Review) != 1 || m.pendingReviews() != 1 {
		t.Errorf("snapshot not applied: %d review", len(m.gl.Review))
	}

	m.Update(fetched{src: srcJira, data: atlassian.JiraSnapshot{Sprint: []atlassian.Issue{{Key: "ABC-9", Summary: "new one"}}}})
	if _, ok := m.ix.issues["ABC-9"]; !ok {
		t.Errorf("Jira snapshot must be reindexed")
	}
	m.Update(fetched{src: srcConfluence, data: atlassian.ConfluenceSnapshot{Mine: []atlassian.Page{{Title: "Runbook"}}}})
	m.Update(fetched{src: srcRepos, data: []repos.Repo{{Dir: "/code/payments"}}})
	if len(m.conf.Mine) != 1 || len(m.repos) != 1 || len(m.vis.pages) != 1 {
		t.Errorf("confluence/repos not applied")
	}

	before := m.frame
	if _, cmd := m.Update(frameMsg{}); m.frame != before+1 || cmd == nil {
		t.Errorf("frame advances and schedules the next one")
	}

	m.Update(usageMsg{{Day: "2026-11-01", Model: "claude-opus-5-5"}})
	if !m.usageLoaded || m.usageLoading || len(m.vis.usage) != 1 || m.usageAt.IsZero() {
		t.Errorf("usage not applied")
	}

	m.Update(mrLoadedMsg{err: errors.New("no such MR")})
	if m.flash != "no such MR" || !m.flashErr {
		t.Errorf("mrLoadedMsg error: %q", m.flash)
	}

	m.fetchedAt = [nSources]time.Time{time.Now(), time.Now(), time.Now(), time.Now()}
	if _, cmd := m.Update(refreshMsg(srcJira)); cmd == nil || !m.busy[srcJira] || m.busy[srcGitlab] {
		t.Errorf("refreshMsg refetches just that source: busy %v", m.busy)
	}

	if _, cmd := m.Update(upDoneMsg{}); cmd != nil {
		t.Errorf("update messages without the update view are dropped")
	}
}

func TestFirstGitlabFetchMarksSeenShell(t *testing.T) {
	m := newModelShell(t)
	m.seenExisted = false
	m.Update(fetched{src: srcGitlab, data: glSnapshotShell()})
	if !m.seenExisted || len(m.seen) != 4 {
		t.Fatalf("first run marks every review and own MR seen, got %d", len(m.seen))
	}
	m.switchView(vReview)
	for _, r := range m.vis.mrs {
		if c := m.chipsText(r); c != "" {
			t.Errorf("!%d: nothing is new on the first run, got %q", r.IID, plainShell(c))
		}
	}
}

func TestSessionsMsgShell(t *testing.T) {
	m := newModelShell(t)
	m.sessions, m.sessLoaded, m.sessLoading = nil, false, true
	m.restore = []restoreItem{{ID: "aaaaaaaa-wait"}, {ID: "eeeeeeee-gone"}}
	m.Update(sessionsMsg(sessionsShell()))
	if !m.sessLoaded || m.sessLoading || len(m.sessions) != 4 {
		t.Fatalf("sessions not applied")
	}
	if len(m.restore) != 1 || m.restore[0].ID != "eeeeeeee-gone" {
		t.Errorf("sessions that came back are no longer offered: %+v", m.restore)
	}
	if m.ix.sessionTicket["aaaaaaaa-wait"] != "ABC-1" {
		t.Errorf("index not rebuilt")
	}
}

func TestConfirmShell(t *testing.T) {
	m := newModelShell(t)
	if m.confirm("A1", "approve !1") {
		t.Fatal("first press only asks")
	}
	if m.flash != "approve !1 — press again to confirm" {
		t.Errorf("flash %q", m.flash)
	}
	if m.confirm("B1", "rebase !1") {
		t.Errorf("another key restarts the confirmation")
	}
	if !m.confirm("B1", "rebase !1") {
		t.Errorf("second press of the same key confirms")
	}
	if m.confirm("B1", "rebase !1") {
		t.Errorf("a confirmation is used up")
	}
	m.confirmAt = time.Now().Add(-4 * time.Second)
	if m.confirm("B1", "rebase !1") {
		t.Errorf("a stale first press doesn't count")
	}
}

func TestAgeShell(t *testing.T) {
	for _, c := range []struct {
		ago  time.Duration
		want string
	}{
		{20 * time.Second, "now"},
		{5 * time.Minute, "5m"},
		{59 * time.Minute, "59m"},
		{3 * time.Hour, "3h"},
		{47 * time.Hour, "47h"},
		{72 * time.Hour, "3d"},
	} {
		if got := age(time.Now().Add(-c.ago)); got != c.want {
			t.Errorf("%v ago: %q, want %q", c.ago, got, c.want)
		}
	}
	if age(time.Time{}) != "" {
		t.Errorf("zero time has no age")
	}
	if got := oneLine("  a\n\tb   c \n"); got != "a b c" {
		t.Errorf("oneLine: %q", got)
	}
}

func TestSourceStatusShell(t *testing.T) {
	m := newModelShell(t)
	m.view = vReview
	for _, c := range []struct {
		name string
		set  func()
		want string
	}{
		{"fresh", func() { m.fetchedAt[srcGitlab] = time.Now() }, "updated just now"},
		{"older", func() { m.fetchedAt[srcGitlab] = time.Now().Add(-5 * time.Minute) }, "updated 5m ago"},
		{"never", func() { m.fetchedAt[srcGitlab] = time.Time{} }, ""},
		{"failed", func() { m.errs[srcGitlab] = errors.New("x") }, "✗ GitLab failed"},
		{"busy", func() { m.busy[srcGitlab] = true }, "⠋ fetching GitLab"},
	} {
		c.set()
		if got := plainShell(m.sourceStatus()); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	for v, want := range map[view]string{vJira: "Jira", vMentions: "Jira", vConfluence: "Confluence", vInbox: "GitLab"} {
		m.view = v
		m.busy = [nSources]bool{true, true, true, true}
		if got := plainShell(m.sourceStatus()); !strings.HasSuffix(got, want) {
			t.Errorf("%s: %q", viewNames[v], got)
		}
	}
	for _, v := range []view{vSessions, vNotes, vUsage} {
		m.view = v
		if got := m.sourceStatus(); got != "" {
			t.Errorf("%s has no source: %q", viewNames[v], got)
		}
	}
}

func TestSubtitleShell(t *testing.T) {
	m := newModelShell(t)
	for _, c := range []struct {
		v    view
		set  func()
		want string
	}{
		{vInbox, func() { m.showAll = true }, "everyone waiting on you, oldest first · incl. snoozed"},
		{vSessions, func() { m.allSessions = true }, "all sessions, last 7 days · by repo  a live only"},
		{vSessions, func() { m.allSessions, m.sessPrefs.Group = false, sessFlat }, "live sessions · flat  a show closed too"},
		{vReview, func() { m.showAll = true }, "all review requests  a hide approved"},
		{vReview, func() { m.showAll = false }, "review requests not yet approved by you  a show all"},
		{vMine, func() {}, "open, then merged in the last 5 days"},
		{vJira, func() { m.teamJira, m.jiraPrefs.Hidden = true, []string{"Done", "QA"} }, "team sprint"},
		{vJira, func() {}, "2 statuses hidden"},
		{vMentions, func() {}, "x dismiss  a show handled"},
		{vConfluence, func() {}, "pages you edited recently"},
		{vUsage, func() {}, "priced at API list rates"},
		{vNotes, func() { m.notesPrefs.Recent = true }, "recently edited notes  v folders"},
		{vNotes, func() { m.notesPrefs.Recent = false }, "your vault  v recently edited"},
	} {
		m.view = c.v
		c.set()
		if got := plainShell(m.subtitle()); !strings.Contains(got, c.want) {
			t.Errorf("%s: %q, want %q", viewNames[c.v], got, c.want)
		}
	}
}

func TestHeaderPillsShell(t *testing.T) {
	m := newModelShell(t)
	m.gl, m.jira, m.sessions = gitlab.Snapshot{}, atlassian.JiraSnapshot{}, nil
	m.reindex()
	s := plainShell(m.viewHeader())
	lacksShell(t, "quiet header", s, "needs you", "to review", "mentions", "failed", "working", "your turn", "⬆ update")
	if strings.Contains(s, "Inbox 0") {
		t.Errorf("zero badges are hidden")
	}
	m.updateAvail = true
	m.sessions = []row{{Session: claude.Session{ID: "ffffffff", Live: &claude.Live{Status: claude.StatusShell}}}}
	containsAllShell(t, "header", plainShell(m.viewHeader()), "⬆ update · ctrl+u", "1 working", "2 Sessions 1")
	m.width = 30
	for _, l := range strings.Split(m.viewHeader(), "\n") {
		if ansi.StringWidth(l) > 30 {
			t.Errorf("header line wider than the terminal: %q", plainShell(l))
		}
	}
}

func TestCountShell(t *testing.T) {
	m := newModelShell(t)
	m.Update(usageMsg{{Day: "2026-11-01"}, {Day: "2026-11-02"}})
	m.conf.Mine = []atlassian.Page{{Title: "a"}}
	m.refilter()
	for v, want := range map[view]int{vInbox: 4, vSessions: 8, vReview: 2, vMine: 2, vMentions: 1, vConfluence: 1, vUsage: 2, vNotes: 0} {
		m.switchView(v)
		if got := m.count(); got != want {
			t.Errorf("%s: %d rows, want %d", viewNames[v], got, want)
		}
	}
}

func TestNewSessionPicksRepoShell(t *testing.T) {
	m := newModelShell(t)
	m.repos = []repos.Repo{{Dir: "/code/billing", Branch: "devel"}, {Dir: "/code/payments", Branch: "feat/ABC-1/export"}}
	m.switchView(vSessions)
	pressShell(m, "j")
	pressShell(m, "n")
	if m.chooser == nil || m.chooser.title != "new session in" {
		t.Fatal("n opens the repo chooser")
	}
	first, _ := m.chooser.selected()
	if first.value != "/code/payments" || first.hint != "feat/ABC-1/export" {
		t.Errorf("the current session's repo comes first, with its branch: %+v", first)
	}
	s := screenShell(m)
	containsAllShell(t, "chooser", s, "new session in ›", "payments", "billing", "tab profile: ")
	pressShell(m, "tab")
	before := plainShell(m.viewFooter())
	pressShell(m, "tab")
	if after := plainShell(m.viewFooter()); before == after {
		t.Errorf("tab cycles the profile: %q", after)
	}
	pressShell(m, "esc")
	if m.chooser != nil {
		t.Errorf("esc closes the chooser")
	}
	m.repos = nil
	pressShell(m, "n")
	if m.flash != "still scanning repos…" {
		t.Errorf("flash %q", m.flash)
	}
}

func TestViewSourceShell(t *testing.T) {
	for s, want := range map[source]string{srcGitlab: "GitLab", srcJira: "Jira", srcConfluence: "Confluence", srcRepos: "repos"} {
		if got := viewSource(s); got != want {
			t.Errorf("%d: %q", s, got)
		}
	}
}

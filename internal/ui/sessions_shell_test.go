package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/links"
	"github.com/MartyJRE/claudia/internal/tmux"
)

func sessionsModelShell(t *testing.T) *Model {
	m := newModelShell(t)
	m.switchView(vSessions)
	return m
}

func cursorOnShell(t *testing.T, m *Model, id string) {
	t.Helper()
	i := slices.IndexFunc(m.vis.sessions, func(r srow) bool { return r.isSess && r.sess.ID == id })
	if i < 0 {
		t.Fatalf("session %s not listed", id)
	}
	m.cur[vSessions].pos = i
}

func sessionIDsShell(m *Model) string {
	var out []string
	for _, r := range m.vis.sessions {
		if r.isSess {
			out = append(out, r.sess.ID[:4])
		} else {
			s := "#" + r.title
			if r.collapsed {
				s += "+"
			}
			out = append(out, s)
		}
	}
	return strings.Join(out, " ")
}

func TestSessionsListStatesShell(t *testing.T) {
	m := sessionsModelShell(t)
	m.sessLoaded = false
	if s := plainShell(m.listSessions(10)); !strings.Contains(s, "reading sessions…") {
		t.Errorf("before the first scan: %q", s)
	}
	m.sessLoaded = true
	m.sessions = nil
	m.reindex()
	if s := plainShell(m.listSessions(10)); !strings.Contains(s, "no live sessions — a for closed ones, n for a new one") {
		t.Errorf("empty: %q", s)
	}
	m.allSessions = true
	if s := plainShell(m.listSessions(10)); !strings.Contains(s, "nothing matches") {
		t.Errorf("empty with closed shown: %q", s)
	}
}

func TestSessionsShowClosedShell(t *testing.T) {
	m := sessionsModelShell(t)
	if got, want := sessionIDsShell(m), "aaaa bbbb cccc #Recently closed dddd"; got != want {
		t.Fatalf("live tree:\n got %s\nwant %s", got, want)
	}
	pressShell(m, "a")
	if got, want := sessionIDsShell(m), "#payments aaaa dddd bbbb cccc"; got != want {
		t.Errorf("a puts closed sessions in their repo:\n got %s\nwant %s", got, want)
	}
	if !strings.Contains(screenShell(m), "all sessions, last 7 days") {
		t.Errorf("subtitle follows")
	}
	pressShell(m, "a")
	if !strings.Contains(sessionIDsShell(m), "Recently closed") {
		t.Errorf("a toggles back")
	}
}

func TestSessionsGroupCycleShell(t *testing.T) {
	m := sessionsModelShell(t)
	pressShell(m, "v")
	if m.sessPrefs.Group != sessByTicket || m.flash != "sessions by epic › ticket" {
		t.Fatalf("v cycles grouping: %d %q", m.sessPrefs.Group, m.flash)
	}
	if got, want := sessionIDsShell(m), "aaaa bbbb cccc #Recently closed dddd"; got != want {
		t.Errorf("by ticket:\n got %s\nwant %s", got, want)
	}
	if s := screenShell(m); !strings.Contains(s, "export csv  Payments export") {
		t.Errorf("a ticket of one session is named on its row:\n%s", s)
	}
	var saved sessionPrefs
	saved = loadSessionPrefs()
	if saved.Group != sessByTicket {
		t.Errorf("grouping is saved")
	}
	pressShell(m, "v")
	if got, want := sessionIDsShell(m), "aaaa bbbb cccc #Recently closed dddd"; got != want {
		t.Errorf("flat:\n got %s\nwant %s", got, want)
	}
	pressShell(m, "v")
	if m.sessPrefs.Group != sessByRepo {
		t.Errorf("wraps back to by repo")
	}
}

func TestSessionsFoldShell(t *testing.T) {
	m := sessionsModelShell(t)
	for _, id := range []string{"gggggggg-pay", "hhhhhhhh-bill"} {
		dir := "/code/payments"
		if strings.HasSuffix(id, "bill") {
			dir = "/code/billing"
		}
		m.sessions = append(m.sessions, row{Session: claude.Session{ID: id, Transcript: &claude.Transcript{Cwd: dir},
			Live: &claude.Live{Status: claude.StatusIdle}}})
	}
	m.reindex()
	cursorOnShell(t, m, "bbbbbbbb-busy")
	pressShell(m, " ")
	if got, want := sessionIDsShell(m), "#payments aaaa gggg #billing+ cccc #Recently closed dddd"; got != want {
		t.Fatalf("space on a session folds its group:\n got %s\nwant %s", got, want)
	}
	if r := m.vis.sessions[m.cur[vSessions].pos]; r.isSess || r.title != "billing" {
		t.Errorf("cursor moves to the folded header, on %+v", r.id())
	}
	pressShell(m, "enter")
	if strings.Contains(sessionIDsShell(m), "+") {
		t.Errorf("enter on a header unfolds it: %s", sessionIDsShell(m))
	}
	if loadSessionPrefs().Collapsed != nil && len(loadSessionPrefs().Collapsed) != 0 {
		t.Errorf("unfold is saved")
	}
	pressShell(m, "-")
	if got, want := sessionIDsShell(m), "#payments+ #billing+ cccc #Recently closed+"; got != want {
		t.Errorf("- folds all:\n got %s\nwant %s", got, want)
	}
	pressShell(m, "/")
	typeShell(m, "spike")
	if got := sessionIDsShell(m); got != "#Recently closed dddd" {
		t.Errorf("filtering shows matches inside folded groups: %s", got)
	}
	pressShell(m, "esc", "+")
	if strings.Contains(sessionIDsShell(m), "+") {
		t.Errorf("+ unfolds all: %s", sessionIDsShell(m))
	}
}

func TestSessionsCursorFollowsRowShell(t *testing.T) {
	m := sessionsModelShell(t)
	cursorOnShell(t, m, "cccccccc-idle")
	extra := row{Session: claude.Session{ID: "eeeeeeee-new", Transcript: &claude.Transcript{Cwd: "/code/zeta", AITitle: "fresh"},
		Live: &claude.Live{Status: claude.StatusBusy, StartedAt: time.Now().UnixMilli()}}}
	m.sessions = append([]row{extra}, m.sessions...)
	m.reindex()
	if r, ok := m.currentSession(); !ok || r.ID != "cccccccc-idle" {
		t.Errorf("a new session above must not move the cursor off its row, on %s", sessionIDsShell(m))
	}
}

func TestSessionsDetailShell(t *testing.T) {
	m := sessionsModelShell(t)
	m.cur[vSessions].pos = slices.IndexFunc(m.vis.sessions, func(r srow) bool { return !r.isSess })
	if s := plainShell(m.detailSession()); !strings.Contains(s, "—") {
		t.Errorf("a header has no detail: %q", s)
	}
	m.links.AddMRs("aaaaaaaa-wait", []string{m.gl.Review[0].WebURL, glShell + "g/other/-/merge_requests/99"})
	m.reindex()
	cursorOnShell(t, m, "aaaaaaaa-wait")
	s := plainShell(m.detailSession())
	containsAllShell(t, "detail", s,
		"export csv  ABC-1 Payments export · Dev in progress",
		"/code/payments", "feat/ABC-1/export", "work", "aaaaaaaa",
		"› please add csv",
		"!11 payments ✓ passed", "!99 other",
		"3 req · 12k out · 1.5M cache read", "models: Sonnet 4.5 → Opus 5.5")

	cursorOnShell(t, m, "cccccccc-idle")
	s = plainShell(m.detailSession())
	containsAllShell(t, "transcript-less detail", s, "ledger chat", "/code/ledger", "cccccccc")
	lacksShell(t, "transcript-less detail", s, "›", "req ·")
}

func TestSessionsMarkSplitShell(t *testing.T) {
	m := sessionsModelShell(t)
	cursorOnShell(t, m, "bbbbbbbb-busy")
	pressShell(m, "m")
	if m.flash != "only live sessions in tmux can be split" || len(m.marked) != 0 {
		t.Errorf("marking a session outside tmux: %q", m.flash)
	}
	pressShell(m, "S")
	if !strings.HasPrefix(m.flash, "mark sessions with m first") {
		t.Errorf("split without marks: %q", m.flash)
	}

	cursorOnShell(t, m, "aaaaaaaa-wait")
	pos := m.cur[vSessions].pos
	pressShell(m, "m")
	if !m.marked["aaaaaaaa-wait"] || m.cur[vSessions].pos != pos+1 {
		t.Fatalf("m marks and moves down")
	}
	if !strings.Contains(screenShell(m), "✓ marked") {
		t.Errorf("marked status shows")
	}
	m.cur[vSessions].pos = pos
	pressShell(m, "m")
	if m.marked["aaaaaaaa-wait"] {
		t.Errorf("m again unmarks")
	}

	cursorOnShell(t, m, "aaaaaaaa-wait")
	pressShell(m, "U")
	if m.flash != "not in a split" {
		t.Errorf("unsplit outside a split: %q", m.flash)
	}
}

func TestSessionsKeysShell(t *testing.T) {
	m := sessionsModelShell(t)

	cursorOnShell(t, m, "dddddddd-closed")
	if cmd := pressShell(m, "K"); cmd != nil || m.flash != "not running" {
		t.Errorf("K on a closed session: %q", m.flash)
	}
	pressShell(m, "o")
	if m.flash != "no MR linked to this session" || !m.flashErr {
		t.Errorf("o without an MR: %q", m.flash)
	}

	cursorOnShell(t, m, "aaaaaaaa-wait")
	pressShell(m, "R")
	if m.flash != "already running — resuming would fork the transcript" {
		t.Errorf("R on a live session: %q", m.flash)
	}
	m.links.AddMRs("aaaaaaaa-wait", []string{m.gl.Review[0].WebURL})
	m.reindex()
	if cmd := pressShell(m, "o"); cmd == nil {
		t.Errorf("o opens the linked MR")
	}
	if cmd := pressShell(m, "y"); cmd == nil {
		t.Errorf("y copies the id")
	}
	if cmd := pressShell(m, "enter"); !isQuitShell(cmd) || m.Result.Attach != "work:3.0" {
		t.Errorf("enter on a tmux session outside tmux attaches to its pane: %q", m.Result.Attach)
	}

	cursorOnShell(t, m, "bbbbbbbb-busy")
	if cmd := pressShell(m, "enter"); cmd == nil || m.flash != "runs in Ghostty, outside tmux — bringing it forward" {
		t.Errorf("enter on a session in another terminal: %q", m.flash)
	}
	cursorOnShell(t, m, "cccccccc-idle")
	if cmd := pressShell(m, "enter"); cmd != nil || m.flash != "runs outside tmux, in an unknown terminal" {
		t.Errorf("enter on a session nobody knows the terminal of: %q", m.flash)
	}

	cursorOnShell(t, m, "aaaaaaaa-wait")
	pressShell(m, "J")
	if m.view != vJira {
		t.Errorf("J opens the session's ticket")
	}
}

func TestSessionsResumeWithoutDirShell(t *testing.T) {
	m := newModelShell(t)
	_, cmd := m.resume(row{Session: claude.Session{ID: "gggggggg"}})
	if cmd != nil || m.flash != "no working directory recorded for this session" {
		t.Errorf("resume without a dir: %q", m.flash)
	}
}

func TestSessionsSetTicketShell(t *testing.T) {
	m := sessionsModelShell(t)
	cursorOnShell(t, m, "cccccccc-idle")
	pressShell(m, "t")
	if m.chooser == nil || m.chooser.title != "ticket for ledger chat" {
		t.Fatal("t opens the ticket chooser")
	}
	var labels []string
	for _, c := range m.chooser.all {
		labels = append(labels, plainShell(c.label))
	}
	if got := strings.Join(labels, "|"); got != "automatic|none|ABC-1 Payments export|ABC-2 Invoices|ABC-3 Old thing" {
		t.Errorf("choices: %s", got)
	}
	typeShell(m, "invoices")
	pressShell(m, "enter")
	if got := m.ix.sessionTicket["cccccccc-idle"]; got != "ABC-2" {
		t.Errorf("picked ticket must be linked: %q", got)
	}
	if k, _ := links.Load().Ticket("cccccccc-idle"); k != "ABC-2" {
		t.Errorf("link is saved: %q", k)
	}

	cursorOnShell(t, m, "aaaaaaaa-wait")
	pressShell(m, "t")
	typeShell(m, "none")
	pressShell(m, "enter")
	if got := m.ix.sessionTicket["aaaaaaaa-wait"]; got != "" {
		t.Errorf("none unlinks even a branch ticket: %q", got)
	}
}

func TestSessionStatusAndWhereShell(t *testing.T) {
	m := newModelShell(t)
	live := func(s claude.Status) row {
		return row{Session: claude.Session{ID: "hhhhhhhh", Live: &claude.Live{Status: s}}}
	}
	for _, c := range []struct {
		r    row
		want string
	}{
		{live(claude.StatusWaiting), "◆ needs you"},
		{live(claude.StatusBusy), "✶ thinking"},
		{live(claude.StatusShell), "⣾ running"},
		{live(claude.StatusIdle), "● your turn"},
		{row{Session: claude.Session{ID: "hhhhhhhh"}}, "○ closed"},
	} {
		if got := plainShell(m.status(c.r)); got != c.want {
			t.Errorf("%v: %q, want %q", c.r.Live, got, c.want)
		}
	}
	m.marked = map[string]bool{"hhhhhhhh": true}
	if got := plainShell(m.status(live(claude.StatusBusy))); got != "✓ marked" {
		t.Errorf("marked wins: %q", got)
	}

	for _, c := range []struct {
		r    row
		want string
	}{
		{row{pane: &tmux.Pane{Session: tmux.SplitSession}}, "split"},
		{row{pane: &tmux.Pane{Session: "work", Window: "2"}}, "tmux work:2"},
		{row{Session: claude.Session{Live: &claude.Live{}}, host: "iTerm2"}, "iterm2"},
		{row{Session: claude.Session{Live: &claude.Live{}}}, "external"},
		{row{}, ""},
	} {
		if got := plainShell(m.where(c.r)); got != c.want {
			t.Errorf("where: %q, want %q", got, c.want)
		}
	}
}

func TestModelNameShell(t *testing.T) {
	for _, c := range []struct{ id, fam, ver string }{
		{"claude-opus-5-5", "Opus", "5.5"},
		{"claude-sonnet-4-5-20250929", "Sonnet", "4.5"},
		{"claude-haiku-4-5", "Haiku", "4.5"},
		{"claude-fable", "Fable", ""},
		{"claude-3-5-haiku-20241022", "Haiku", "3.5"},
		{"claude-3-opus-20240229", "Opus", "3"},
		{"opus", "Opus", ""},
		{"", "", ""},
	} {
		if fam, ver := modelName(c.id); fam != c.fam || ver != c.ver {
			t.Errorf("%q: (%q, %q), want (%q, %q)", c.id, fam, ver, c.fam, c.ver)
		}
	}
	r := row{Session: claude.Session{Transcript: &claude.Transcript{Model: "claude-haiku-4-5"}}}
	if got := plainShell(modelChip(r)); got != "Haiku 4.5" {
		t.Errorf("chip: %q", got)
	}
	if modelChip(row{}) != "" || modelChip(row{Session: claude.Session{Transcript: &claude.Transcript{}}}) != "" {
		t.Errorf("no model, no chip")
	}
}

func TestSessionHelpersShell(t *testing.T) {
	for n, want := range map[int]string{0: "0", 999: "999", 1_000: "1k", 12_499: "12k", 999_499: "999k", 999_999: "1.0M", 1_000_000: "1.0M", 2_350_000: "2.4M"} {
		if got := tokens(n); got != want {
			t.Errorf("tokens(%d) = %q, want %q", n, got, want)
		}
	}
	if project("") != "" || project("/code/payments") != "payments" {
		t.Errorf("project")
	}
	if branch(row{}) != "" || branch(row{Session: claude.Session{Transcript: &claude.Transcript{Branch: "x"}}}) != "x" {
		t.Errorf("branch")
	}
	t.Setenv("HOME", "/home/me")
	if got := shortHome("/home/me/code"); got != "~/code" {
		t.Errorf("shortHome: %q", got)
	}
	if got := shortHome("/srv/code"); got != "/srv/code" {
		t.Errorf("shortHome outside home: %q", got)
	}
	personal := row{Session: claude.Session{ID: "iiiiiiii", Profile: claude.Profile{Name: "personal"}}}
	if got := plainShell(sessionTitle(personal, false)); got != "iiiiiiii ·personal" {
		t.Errorf("personal sessions are labelled: %q", got)
	}
}

func TestMRRefShell(t *testing.T) {
	m := newModelShell(t)
	merged := m.gl.Merged[0]
	if got := plainShell(m.mrRef(merged.WebURL)); got != "!22 ledger merged" {
		t.Errorf("known merged MR: %q", got)
	}
	if got := plainShell(m.mrRef(glShell + "g/sub/thing/-/merge_requests/5")); got != "!5 thing" {
		t.Errorf("unknown MR: %q", got)
	}
	if got := plainShell(m.mrRef("https://github.com/o/r/pull/7")); got != "#7 r" {
		t.Errorf("GitHub PR: %q", got)
	}
	closed := gitlab.MR{State: "closed"}
	draft := gitlab.MR{Draft: true}
	if plainShell(mrState(closed)) != "closed" || plainShell(mrState(draft)) != "draft" {
		t.Errorf("mrState")
	}
}

func TestSessionsSummaryLineShell(t *testing.T) {
	m := sessionsModelShell(t)
	for i := range m.sessions {
		m.sessions[i].summary = []string{"Needs you: OK to merge?", "Waiting: Michal to approve", "Done: PR #5 open", ""}[i%4]
	}
	m.reindex()
	s := plainShell(m.listSessions(30))
	for _, want := range []string{"OK to merge?", "Waiting: Michal to approve", "Done: PR #5 open"} {
		if !strings.Contains(s, want) {
			t.Errorf("summary %q missing:\n%s", want, s)
		}
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if strings.Contains(l, "OK to merge?") && !strings.Contains(lines[i-1], m.sessions[0].Name()) {
			t.Errorf("the summary sits under its session:\n%s\n%s", lines[i-1], l)
		}
	}
	if plainShell(summaryText(row{summary: "Needs you: x"}, true)) != "Needs you: x" || summaryText(row{}, true) != "" {
		t.Error("summaryText")
	}

	// Two-line rows still keep the cursor on screen.
	for range len(m.vis.sessions) {
		pressShell(m, "down")
	}
	short := plainShell(m.listSessions(5))
	if r, ok := m.currentSession(); ok && !strings.Contains(short, r.Name()) {
		t.Errorf("cursor row %q scrolled off:\n%s", r.Name(), short)
	}
	if n := strings.Count(short, "\n") + 1; n > 5 {
		t.Errorf("%d lines in a 5-line list", n)
	}

	m.switchView(vSessions)
	m.cur[vSessions].pos = slices.IndexFunc(m.vis.sessions, func(x srow) bool { return x.isSess && x.sess.ID == m.sessions[0].ID })
	if d := plainShell(m.detailSession()); !strings.Contains(d, "Needs you: OK to merge?") {
		t.Errorf("detail shows the summary:\n%s", d)
	}
}

func TestSessionsNeedsYouFromSummaryShell(t *testing.T) {
	m := sessionsModelShell(t)
	i := slices.IndexFunc(m.sessions, func(r row) bool { return r.live() && r.Transcript != nil })
	if i < 0 {
		t.Fatal("no live session in the fixture")
	}
	r := &m.sessions[i]
	at := time.Now().Add(-time.Minute)
	r.Live.Status, r.Transcript.Updated = claude.StatusIdle, at
	r.summary, r.summaryFor = "Needs you: OK to merge !260?", at
	m.reindex()
	if !r.needsYou() || plainShell(m.status(*r)) != "◆ needs you" {
		t.Fatalf("a summary asking you makes the session need you: %q", plainShell(m.status(*r)))
	}
	if got := plainShell(summaryText(*r, true)); got != "OK to merge !260?" {
		t.Errorf("the line says only what for: %q", got)
	}
	if !strings.Contains(plainShell(m.viewHeader()), "needs you") {
		t.Error("the header counts it")
	}
	if !slices.ContainsFunc(m.inbox(), func(it inboxItem) bool {
		return it.kind == kClaude && it.sess.ID == r.ID && strings.HasSuffix(it.text, " · OK to merge !260?")
	}) {
		t.Error("the inbox lists it, with what it asks")
	}

	r.summaryFor = at.Add(-time.Hour) // written for an earlier turn
	if r.needsYou() || plainShell(summaryText(*r, true)) != "Needs you: OK to merge !260?" {
		t.Error("an old summary doesn't make it need you")
	}
	r.summaryFor, r.Live.Status = at, claude.StatusBusy
	if r.needsYou() {
		t.Error("working again: not waiting on you")
	}
}

func TestNeedsYouShell(t *testing.T) {
	at := time.Now().Add(-time.Minute)
	tr := &claude.Transcript{Updated: at}
	live := func(s claude.Status) *claude.Live { return &claude.Live{Status: s} }
	for _, c := range []struct {
		name string
		r    row
		want bool
	}{
		{"closed", row{Session: claude.Session{Transcript: tr}, summary: "Needs you: x", summaryFor: at}, false},
		{"waiting on a prompt", row{Session: claude.Session{Live: live(claude.StatusWaiting)}}, true},
		{"busy, though the summary asks", row{Session: claude.Session{Transcript: tr, Live: live(claude.StatusBusy)}, summary: "Needs you: x", summaryFor: at}, false},
		{"in a shell command", row{Session: claude.Session{Transcript: tr, Live: live(claude.StatusShell)}, summary: "Needs you: x", summaryFor: at}, false},
		{"idle, the summary of this turn asks", row{Session: claude.Session{Transcript: tr, Live: live(claude.StatusIdle)}, summary: "Needs you: x", summaryFor: at}, true},
		{"idle, an earlier turn's summary", row{Session: claude.Session{Transcript: tr, Live: live(claude.StatusIdle)}, summary: "Needs you: x", summaryFor: at.Add(-time.Second)}, false},
		{"idle, waiting on someone else", row{Session: claude.Session{Transcript: tr, Live: live(claude.StatusIdle)}, summary: "Waiting: Michal", summaryFor: at}, false},
		{"idle, the prefix in another case", row{Session: claude.Session{Transcript: tr, Live: live(claude.StatusIdle)}, summary: "needs you: x", summaryFor: at}, false},
		{"idle without a transcript", row{Session: claude.Session{Live: live(claude.StatusIdle)}, summary: "Needs you: x"}, false},
	} {
		if got := c.r.needsYou(); got != c.want {
			t.Errorf("%s: needsYou %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSummaryTextShell(t *testing.T) {
	prev := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	lipgloss.SetColorProfile(0) // termenv.TrueColor
	at := time.Now()
	asking := row{Session: claude.Session{Transcript: &claude.Transcript{Updated: at}, Live: &claude.Live{Status: claude.StatusIdle}}, summary: "Needs you:  OK to merge?", summaryFor: at}
	stale := asking
	stale.summaryFor = at.Add(-time.Hour)
	yellowS, blueS := lipgloss.NewStyle().Foreground(yellow), lipgloss.NewStyle().Foreground(blue)
	for _, c := range []struct {
		name  string
		r     row
		under bool
		want  string
	}{
		{"nothing written", row{}, true, ""},
		{"under a needs-you status", asking, true, yellowS.Render("OK to merge?")},
		{"in the detail", asking, false, yellowS.Render("Needs you:  OK to merge?")},
		{"for an earlier turn", stale, true, yellowS.Render("Needs you:  OK to merge?")},
		{"waiting on someone", row{summary: "Waiting: Michal"}, true, blueS.Render("Waiting: Michal")},
		{"anything else", row{summary: "Done: PR open"}, true, sDim.Render("Done: PR open")},
	} {
		if got := summaryText(c.r, c.under); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestHeaderAndInboxCountNeedsYouShell(t *testing.T) {
	m := sessionsModelShell(t)
	claudeItems := func() []string {
		var out []string
		for _, it := range m.inbox() {
			if it.kind == kClaude {
				out = append(out, it.text)
			}
		}
		return out
	}
	if h := plainShell(m.viewHeader()); !strings.Contains(h, "1 needs you") {
		t.Fatalf("the waiting session counts: %s", h)
	}
	before := len(claudeItems())
	i := slices.IndexFunc(m.sessions, func(r row) bool {
		return r.live() && r.Transcript != nil && r.Live.Status != claude.StatusWaiting
	})
	if i < 0 {
		t.Fatal("no live session with a transcript in the fixture")
	}
	r := &m.sessions[i]
	r.Live.Status = claude.StatusIdle
	r.summary, r.summaryFor = "Needs you: pick a name", r.Updated
	if h := plainShell(m.viewHeader()); !strings.Contains(h, "2 needs you") {
		t.Errorf("an asking summary counts too: %s", h)
	}
	if got := claudeItems(); len(got) != before+1 || !slices.Contains(got, r.Name()+" · pick a name") {
		t.Errorf("the inbox adds it with what it asks: %q", got)
	}
	r.summary = "Needs you:"
	if got := claudeItems(); !slices.Contains(got, r.Name()) {
		t.Errorf("with nothing after the prefix only the name: %q", got)
	}
	w := slices.IndexFunc(m.sessions, func(r row) bool { return r.live() && r.Live.Status == claude.StatusWaiting })
	m.sessions[w].summary, m.sessions[w].summaryFor = "Needs you: stale", time.Time{}
	if got := claudeItems(); !slices.Contains(got, m.sessions[w].Name()) {
		t.Errorf("a session waiting on a prompt doesn't add its summary: %q", got)
	}
}

func TestSessionsDoneFromSummaryShell(t *testing.T) {
	m := sessionsModelShell(t)
	i := slices.IndexFunc(m.sessions, func(r row) bool { return r.live() && r.Transcript != nil })
	r := &m.sessions[i]
	at := time.Now().Add(-time.Minute)
	r.Live.Status, r.Transcript.Updated = claude.StatusIdle, at
	r.summary, r.summaryFor = "Done: PR #5 merged", at
	m.reindex()
	if !r.done() || r.needsYou() || plainShell(m.status(*r)) != "✓ done" {
		t.Fatalf("a finished session says so: %q", plainShell(m.status(*r)))
	}
	if got := plainShell(summaryText(*r, true)); got != "PR #5 merged" {
		t.Errorf("the line under it drops the prefix: %q", got)
	}
	if got := plainShell(summaryText(*r, false)); got != "Done: PR #5 merged" {
		t.Errorf("the detail keeps it: %q", got)
	}
	h := plainShell(m.viewHeader())
	if !strings.Contains(h, "1 done") {
		t.Errorf("the header counts it: %s", h)
	}
	m.allSessions = true
	m.refilter()
	if s := plainShell(m.listSessions(40)); !strings.Contains(s, "1 done") {
		t.Errorf("its group counts it:\n%s", s)
	}
	if slices.ContainsFunc(m.inbox(), func(it inboxItem) bool { return it.kind == kClaude && it.sess.ID == r.ID }) {
		t.Error("a done session isn't waiting on you")
	}

	for name, change := range map[string]func(){
		"older summary": func() { r.summaryFor = at.Add(-time.Hour) },
		"working again": func() { r.Live.Status = claude.StatusBusy },
		"waiting":       func() { r.Live.Status = claude.StatusWaiting },
		"closed":        func() { r.Live = nil },
		"not done":      func() { r.summary = "Waiting: CI" },
	} {
		saved, live := *r, *r.Live
		change()
		if r.done() {
			t.Errorf("%s: still done", name)
		}
		*r = saved
		*r.Live = live
	}
}

func TestSingleGroupLabelShell(t *testing.T) {
	m := sessionsModelShell(t)
	lacksShell(t, "wide", screenShell(m), "export csv  payments")
	m.width = 90
	m.refilter()
	containsAllShell(t, "narrow, without the REPO column", screenShell(m), "export csv  payments")
}

func TestOneTicketEpicHeaderShell(t *testing.T) {
	m := sessionsModelShell(t)
	m.sessPrefs.Group = sessByTicket
	m.jira.Sprint[0].Parent, m.jira.Sprint[0].ParentSummary = "ABC-9", "Money"
	m.sessions = append(m.sessions, row{Session: claude.Session{ID: "gggggggg-pay", Transcript: &claude.Transcript{Cwd: "/code/payments", Branch: "feat/ABC-1/more"},
		Live: &claude.Live{Status: claude.StatusIdle}}})
	m.reindex()
	containsAllShell(t, "by ticket", screenShell(m), "▾ Money › ABC-1 Payments export  2 session(s)")
}

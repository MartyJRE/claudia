package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

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
	if got, want := sessionIDsShell(m), "#payments aaaa #billing bbbb #ledger cccc #Recently closed dddd"; got != want {
		t.Fatalf("live tree:\n got %s\nwant %s", got, want)
	}
	pressShell(m, "a")
	if got, want := sessionIDsShell(m), "#payments aaaa dddd #billing bbbb #ledger cccc"; got != want {
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
	if got, want := sessionIDsShell(m), "#Payments export aaaa #Invoices bbbb #No ticket cccc #Recently closed dddd"; got != want {
		t.Errorf("by ticket:\n got %s\nwant %s", got, want)
	}
	if s := screenShell(m); !strings.Contains(s, "ABC-1 Payments export") {
		t.Errorf("ticket headers show the key")
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
	cursorOnShell(t, m, "bbbbbbbb-busy")
	pressShell(m, " ")
	if got, want := sessionIDsShell(m), "#payments aaaa #billing+ #ledger cccc #Recently closed dddd"; got != want {
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
	if got, want := sessionIDsShell(m), "#payments+ #billing+ #ledger+ #Recently closed+"; got != want {
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
		{live(claude.StatusBusy), "⠋ working"},
		{live(claude.StatusShell), "⠋ working"},
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
	for n, want := range map[int]string{0: "0", 999: "999", 1_000: "1k", 12_499: "12k", 1_000_000: "1.0M", 2_350_000: "2.4M"} {
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
	closed := gitlab.MR{State: "closed"}
	draft := gitlab.MR{Draft: true}
	if plainShell(mrState(closed)) != "closed" || plainShell(mrState(draft)) != "draft" {
		t.Errorf("mrState")
	}
}

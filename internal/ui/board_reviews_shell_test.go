package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/state"
)

func reviewSessShell(id, url string, status claude.Status, summary string) row {
	at := time.Now()
	return row{
		Session: claude.Session{ID: id, Transcript: &claude.Transcript{AITitle: "review " + id, Branch: "devel", Updated: at, PRs: []claude.PRLink{{URL: url}}}, Live: &claude.Live{SessionID: id, Status: status}},
		kind:    "review", summary: summary, summaryFor: at,
	}
}

// reviewBoardShell has a review in every stage: !11 requested, !31 being
// reviewed, !32 with findings to check, !33 waiting on its author, !13 approved.
func reviewBoardShell(t *testing.T) *Model {
	t.Helper()
	m := newModelShell(t)
	reviewing := mrShell("search", 31, "feat: faster search", "bob", "feat/search", 3*time.Hour)
	reviewing.Pipeline = &gitlab.Pipeline{Status: "running"}
	findings := mrShell("ledger", 32, "fix(ABC-2): ledger totals", "carol", "fix/totals", 5*time.Hour)
	findings.Pipeline = &gitlab.Pipeline{Status: "failed"}
	findings.ApprovalsLeft = 2
	waiting := mrShell("billing", 33, "chore: tidy", "dave", "chore/tidy", 26*time.Hour)
	waiting.MyReview = "reviewed"
	m.gl.Review = append(m.gl.Review, reviewing, findings, waiting)
	m.sessions = append(m.sessions,
		reviewSessShell("r-31", reviewing.WebURL, claude.StatusBusy, ""),
		reviewSessShell("r-32", findings.WebURL, claude.StatusIdle, "Done: two blockers in totals.go\nand more"))
	m.reindex()
	m.switchView(vBoard)
	return m
}

// screenSizedShell checks the screen fits the terminal, and writes it to
// $BOARD_DUMP to eyeball.
func screenSizedShell(t *testing.T, m *Model, name string) string {
	t.Helper()
	out := m.View()
	if h := lipgloss.Height(out); h != m.height {
		t.Errorf("%s: %d lines", name, h)
	}
	for i, l := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(l); w > m.width {
			t.Errorf("%s: line %d is %d wide", name, i, w)
		}
	}
	if dir := os.Getenv("BOARD_DUMP"); dir != "" {
		_ = os.WriteFile(filepath.Join(dir, name+".txt"), []byte(plainShell(out)), 0o644)
	}
	return plainShell(out)
}

func TestReviewStageShell(t *testing.T) {
	m := reviewBoardShell(t)
	got := map[int]boardCard{}
	for _, c := range m.reviewCards() {
		got[c.mr.IID] = c
	}
	if _, ok := got[12]; ok {
		t.Error("drafts get no card")
	}
	for iid, want := range map[int]rstage{11: rsRequested, 31: rsReviewing, 32: rsFindings, 33: rsWaiting, 13: rsDone} {
		if got[iid].rstage != want {
			t.Errorf("!%d: %s, want %s (%s)", iid, rstageNames[got[iid].rstage], rstageNames[want], got[iid].why)
		}
	}
	if c := got[32]; c.turn != turnNeedsYou || c.next != "two blockers in totals.go" || c.stage != stReview {
		t.Errorf("findings: %d %q %d", c.turn, c.next, c.stage)
	}
	if c := got[31]; c.turn != turnAgent || c.verb != "reviewing" {
		t.Errorf("reviewing: %d %q", c.turn, c.verb)
	}

	// A review from before the MR last changed is stale: review it again.
	mr := m.gl.Review[4]
	sess := got[32].sessions
	mr.UpdatedAt = time.Now().Add(time.Minute)
	if st, tn, next, _, why := reviewStage(mr, sess, nil, "me"); st != rsRequested || tn != turnYou || next != "review it again" || !strings.Contains(why, "changed after") {
		t.Errorf("stale review: %s %q %q", rstageNames[st], next, why)
	}
	// Of several reviews the latest one counts.
	older := reviewSessShell("r-old", mr.WebURL, claude.StatusIdle, "")
	older.Transcript.Updated = time.Now().Add(-48 * time.Hour)
	mr.UpdatedAt = time.Now().Add(-24 * time.Hour)
	if st, _, next, _, _ := reviewStage(mr, append([]row{older}, sess...), nil, "me"); st != rsFindings || next != "two blockers in totals.go" {
		t.Errorf("latest review: %s %q", rstageNames[st], next)
	}
	if st, _, next, _, _ := reviewStage(mr, []row{older}, nil, "me"); st != rsRequested || next != "review it again" {
		t.Errorf("an old review alone: %s %q", rstageNames[st], next)
	}
	older.Transcript.Updated = time.Now()
	if _, _, next, _, _ := reviewStage(mr, []row{older}, nil, "me"); next != "read the findings" {
		t.Errorf("no summary: %q", next)
	}
}

func TestBoardViewsShell(t *testing.T) {
	m := reviewBoardShell(t)

	// Tickets, the default: no review cards, and a review request is the
	// ticket's turn.
	s := screenSizedShell(t, m, "tickets-160x40")
	containsAllShell(t, "tickets", s, "2 Board 3", "[Tickets]· Reviews 1 · All ", "my tickets by stage", "r view", "Review 2")
	lacksShell(t, "tickets", s, "!31", "Check findings")
	if c := m.vis.board[3][1]; c.issue.Key != "ABC-2" || c.next != "review !32" {
		t.Errorf("ABC-2 in Tickets: %q", c.next)
	}

	// Reviews: the four review columns, and f has no sprints to show.
	pressShell(m, "r")
	if p := loadBoardPrefs(); p.View != "reviews" {
		t.Errorf("the view is saved: %+v", p)
	}
	s = screenSizedShell(t, m, "reviews-160x40")
	containsAllShell(t, "reviews", s, " Tickets ·[Reviews 1]· All ", "MRs waiting on your review by stage",
		"Requested 1", "Reviewing 1", "Check findings 1", "Waiting on author 1",
		"!11 payments", "!31 search", "reviewing", "!32 ledger  ◆ needs you", "two blockers…", "!33 billing  ⧗ waiting", "waits on dave",
		"✗ failed", "✓ passed", "… running")
	lacksShell(t, "reviews", s, "Up next", "Payments export", "!13 ", "!12 ", "!11 ✓", "!32 ✗")
	if !m.animates() {
		t.Error("a working review animates")
	}
	pressShell(m, "f")
	if !strings.Contains(m.flash, "sprints are for tickets") || m.boardSprint != 0 {
		t.Errorf("f on Reviews: %q", m.flash)
	}

	// Narrow, the badges shorten so the state still reads.
	m.width, m.height = 100, 30
	s = screenSizedShell(t, m, "reviews-100x30")
	containsAllShell(t, "narrow", s, "◆ check", "review…")

	// All: review cards sit in the ticket columns, after the tickets.
	m.width, m.height = 160, 40
	pressShell(m, "r")
	s = screenSizedShell(t, m, "all-160x40")
	containsAllShell(t, "all", s, " Tickets · Reviews 1 ·[All]", "my tickets and your reviews by stage",
		"Up next 1", "Building 1", "Review 3", "Merge 1", "!11 payments", "!33 billing")
	if c := m.vis.board[3][1]; c.issue.Key != "ABC-2" || c.next == "review !32" {
		t.Errorf("ABC-2 on All: %q", c.next)
	}
	m.width, m.height = 100, 30
	screenSizedShell(t, m, "all-100x30")

	// And round to Tickets.
	pressShell(m, "r")
	if m.boardPrefs.view() != bvTickets {
		t.Error("r cycles back to Tickets")
	}
	for name, want := range map[string]boardView{"": bvTickets, "tickets": bvTickets, "Reviews": bvReviews, "all": bvAll, "lanes": bvTickets} {
		if got := (boardPrefs{View: name}).view(); got != want {
			t.Errorf("view %q: %d", name, got)
		}
	}
}

func TestBoardViewSavedShell(t *testing.T) {
	m := reviewBoardShell(t)
	if err := state.Save(boardPrefsFile, boardPrefs{View: "all", Done: true}); err != nil {
		t.Fatal(err)
	}
	m.boardPrefs = loadBoardPrefs()
	m.refilter()
	s := screenSizedShell(t, m, "all-done")
	containsAllShell(t, "saved", s, "[All]", "QA 0 ›", "and your reviews incl. done")

	// A future sprint on All shows its tickets only.
	m.jira.Sprint[0].Sprint = &atlassian.Sprint{Name: "Sprint 9", State: "future"}
	m.reindex()
	pressShell(m, "f")
	if m.boardSprint != 1 {
		t.Fatal("f on All shows the next sprint")
	}
	for _, col := range m.vis.board {
		for _, c := range col {
			if c.review() {
				t.Errorf("!%d on a future sprint", c.mr.IID)
			}
		}
	}

	// Reviews with Done shows the approved ones; with nothing left, it says so.
	m.boardPrefs.View = "reviews"
	m.boardSprint = 0
	m.refilter()
	containsAllShell(t, "reviews done", screenShell(m), "Done 1", "!13 ledger")
	m.gl.Review = nil
	m.reindex()
	containsAllShell(t, "no reviews", screenShell(m), "no reviews waiting on you — d shows the approved ones")
}

func TestReviewCardKeysShell(t *testing.T) {
	m := reviewBoardShell(t)
	pressShell(m, "r", "down")
	if c, ok := m.currentCard(); !ok || !c.review() || c.mr.IID != 11 {
		t.Fatalf("stepped in on %+v", c)
	}
	containsAllShell(t, "hints", screenShell(m), "c review with claude", "o open MR", "y copy link")
	if m.currentTicket() != "ABC-1" {
		t.Errorf("a review's ticket: %q", m.currentTicket())
	}

	// No session yet: enter starts a review, as c and C do.
	for _, k := range []string{"enter", "c", "C"} {
		m.chooser = nil
		pressShell(m, k)
		if m.chooser == nil || m.chooser.title != "review payments in" {
			t.Errorf("%s on a requested review", k)
		}
	}
	m.chooser = nil
	for _, k := range []string{"o", "M", "y"} {
		if pressShell(m, k) == nil {
			t.Errorf("%s: no command", k)
		}
	}
	pressShell(m, "m")
	if !strings.Contains(m.flash, "!11 is a review: only tickets move") {
		t.Errorf("m on a review: %q", m.flash)
	}
	if pressShell(m, "z") != nil {
		t.Error("an unbound key does nothing")
	}
	pressShell(m, "t")
	if m.chooser == nil && m.flash == "" {
		t.Error("t shows the threads")
	}
	m.chooser = nil

	// With a session, enter offers it; the filter keeps the cursor on its card.
	pressShell(m, "l", "l")
	if c, _ := m.currentCard(); c.mr.IID != 32 {
		t.Fatalf("l l: %+v", c)
	}
	d := plainShell(m.detailBoard())
	containsAllShell(t, "detail", d, "!32 fix(ABC-2): ledger totals", "Check findings", "g/ledger", "by carol", "✗ failed",
		"2 approval(s) left", "ABC-2", "◆ needs you · two blockers", "review r-32", "Done: two blockers in totals.go and more")
	pressShell(m, "enter")
	if m.chooser == nil || m.chooser.title != "sessions for !32" {
		t.Error("enter offers the review's sessions")
	}
	m.chooser = nil
	m.refilter()
	if c, _ := m.currentCard(); c.mr.IID != 32 {
		t.Errorf("refilter moved the cursor to %+v", c)
	}
	m.cur[vBoard].pos = 0
	m.boardCol = 4 // Done isn't shown: clamped back
	m.refilter()
	containsAllShell(t, "approved", plainShell(m.detailReview(boardCard{mr: &m.gl.Review[2], rstage: rsDone, why: "you approved it"})), "approved by me")

	// Selected review cards are lit; others keep their tint.
	prev := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	lipgloss.SetColorProfile(0) // termenv.TrueColor
	const tinted = "48;2;52;46;6"
	for _, l := range strings.Split(m.View(), "\n") {
		if p := plainShell(l); strings.Contains(p, "faster search") && !strings.Contains(l, tinted) {
			t.Error("a review card without its tint")
		}
	}
	m.boardPrefs.View = "all"
	m.refilter()
	for _, l := range strings.Split(m.View(), "\n") {
		if p := plainShell(l); strings.Contains(p, "Invoices") && strings.Contains(l, tinted) {
			t.Error("a ticket card tinted")
		}
	}
}

func TestReviewBadgeShell(t *testing.T) {
	m := reviewBoardShell(t)
	for _, c := range []struct {
		card boardCard
		want string
	}{
		{boardCard{turn: turnNeedsYou}, "◆ check"},
		{boardCard{turn: turnAgent, verb: "reviewing"}, "review…"},
		{boardCard{turn: turnColleague}, "⧗ author"},
		{boardCard{turn: turnYou}, ""},
	} {
		if got := strings.TrimSpace(plainShell(m.reviewBadge(c.card, 3))); !strings.Contains(got, c.want) || (c.want == "" && got != "") {
			t.Errorf("turn %d: %q", c.card.turn, got)
		}
	}
	if pipelineChip(gitlab.MR{}) != "" {
		t.Error("no pipeline, no chip")
	}
	// Too narrow for the ref, the badge stays and the repo gives way.
	l := plainShell(m.reviewCardLines(boardCard{mr: &m.gl.Review[0], turn: turnNeedsYou}, 12, false)[0])
	if !strings.Contains(l, "◆ check") {
		t.Errorf("narrow: %q", l)
	}
}

func TestBoardViewsStayPutShell(t *testing.T) {
	m := boardModelShell(t)
	at := map[int]bool{}
	for range nBoardViews {
		at[strings.Index(plainShell(m.boardViews()), "Reviews")] = true
		pressShell(m, "r")
	}
	if len(at) != 1 {
		t.Errorf("the labels move as r cycles: %v", at)
	}
}

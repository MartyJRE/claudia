package ui

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/brief"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/state"
)

func TestInboxItemsShell(t *testing.T) {
	m := newModelShell(t)
	m.conf.Mentions = []atlassian.Page{{ID: "p1", Title: "Payments runbook", Space: "OPS", URL: "page-1", Excerpt: "@me please review", Modified: time.Now().Add(-10 * time.Hour)}}
	items := m.inbox()
	type got struct {
		kind             inboxKind
		where, who, text string
		ticket           string
	}
	want := []got{
		{kMention, "ABC-1", "Eve Smith", "can you check?", "ABC-1"},
		{kPage, "OPS", "", "Payments runbook", ""},
		{kThreads, "billing !21", "carol", "1 open: rename this", "ABC-2"},
		{kReview, "payments !11", "alice", "feat(ABC-1): add payments export", "ABC-1"},
		{kClaude, "payments", "claude", "export csv", "ABC-1"},
	}
	if len(items) != len(want) {
		t.Fatalf("got %d items, want %d: drafts, approved reviews, answered mentions and non-waiting sessions stay out", len(items), len(want))
	}
	for i, w := range want {
		it := items[i]
		if g := (got{it.kind, it.where, it.who, it.text, it.ticket}); g != w {
			t.Errorf("item %d (oldest first): got %+v, want %+v", i, g, w)
		}
	}
}

func TestInboxThreadsShell(t *testing.T) {
	m := newModelShell(t)
	mr := m.gl.Mine[0]
	now := time.Now()
	mr.Threads = append(mr.Threads,
		gitlab.Thread{Author: "erin", Body: "older", At: now.Add(-9 * time.Hour)},
		gitlab.Thread{Author: "carol", Body: "again", At: now.Add(-time.Hour)})
	m.gl.Mine = []gitlab.MR{mr}
	var thr inboxItem
	for _, it := range m.inbox() {
		if it.kind == kThreads {
			thr = it
		}
	}
	if thr.who != "carol, erin" || thr.text != "3 open: rename this" || !thr.since.Equal(now.Add(-9*time.Hour)) {
		t.Errorf("threads item: who %q text %q since %v", thr.who, thr.text, thr.since)
	}
	for i := range mr.Threads {
		mr.Threads[i].Resolved = true
	}
	m.gl.Mine = []gitlab.MR{mr}
	for _, it := range m.inbox() {
		if it.kind == kThreads {
			t.Errorf("an MR with every thread resolved isn't waiting on you")
		}
	}
}

func TestInboxSnoozeShell(t *testing.T) {
	m := newModelShell(t)
	m.switchView(vInbox)
	pressShell(m, "j")
	snoozed := m.vis.inbox[1].id
	pressShell(m, "x")
	if len(m.vis.inbox) != 3 || m.inboxCount() != 3 || m.badge(vInbox) != "3" {
		t.Fatalf("snoozed item still listed: %d", len(m.vis.inbox))
	}
	var onDisk dismissed
	if !state.Load(dismissedFile, &onDisk) || len(onDisk) != 1 {
		t.Errorf("snooze must be saved for the next start, got %v", onDisk)
	}
	if _, ok := onDisk[snoozed]; !ok {
		t.Errorf("saved the wrong id")
	}

	pressShell(m, "a")
	if !m.showAll || len(m.vis.inbox) != 4 {
		t.Fatalf("a shows snoozed items too")
	}
	if !strings.Contains(screenShell(m), "incl. snoozed") {
		t.Errorf("subtitle says snoozed are shown")
	}
	if m.vis.inbox[1].id != snoozed {
		t.Fatalf("cursor should be back on the snoozed item")
	}
	pressShell(m, "x")
	if len(m.dismissed) != 0 || m.inboxCount() != 4 {
		t.Errorf("x on a snoozed item brings it back")
	}
	pressShell(m, "a")
	if m.showAll {
		t.Errorf("a toggles back")
	}
}

func TestInboxChangeUnsnoozesShell(t *testing.T) {
	m := newModelShell(t)
	for _, it := range m.inbox() {
		if it.kind == kReview || it.kind == kThreads || it.kind == kClaude {
			m.dismissed[it.id] = time.Now()
		}
	}
	m.refilter()
	if len(m.vis.inbox) != 1 {
		t.Fatalf("only the mention should be left, got %d", len(m.vis.inbox))
	}
	m.gl.Review[0].SHA = "pushed-again"
	m.gl.Mine[0].Threads = append(m.gl.Mine[0].Threads, gitlab.Thread{Author: "dave", Body: "one more"})
	m.sessions[0].Live.UpdatedAt = time.Now().UnixMilli()
	m.refilter()
	if len(m.vis.inbox) != 4 {
		t.Errorf("new commits, a new thread and a new question bring each item back; got %d", len(m.vis.inbox))
	}
}

func TestInboxEmptyShell(t *testing.T) {
	m := newModelShell(t)
	m.gl, m.jira, m.sessions = gitlab.Snapshot{FetchedAt: time.Now()}, atlassian.JiraSnapshot{}, nil
	m.reindex()
	m.switchView(vInbox)
	s := screenShell(m)
	containsAllShell(t, "empty inbox", s, "nobody is waiting on you", "R asks Claude for a summary of what's waiting", "—")
	if m.badge(vInbox) != "" {
		t.Errorf("no badge on an empty inbox")
	}
	for _, k := range []string{"enter", "x", "r", "A", "B", "c", "J", "v"} {
		if cmd := pressShell(m, k); cmd != nil {
			t.Errorf("%s with nothing selected returned a command", k)
		}
	}
}

func TestInboxBriefShell(t *testing.T) {
	m := newModelShell(t)
	m.switchView(vInbox)

	m.brief = brief.Brief{Text: strings.Repeat("Two reviews wait on you. ", 40), At: time.Now().Add(-10 * time.Minute)}
	top := m.viewBrief()
	containsAllShell(t, "brief", plainShell(top), "written 10m ago", "R refresh", "Two reviews wait on you.")
	if h := lipgloss.Height(top); h > 9 {
		t.Errorf("a long brief is capped, got %d lines", h)
	}

	m.brief = brief.Brief{Text: "ok", At: time.Now(), Err: "claude:\nexit 1"}
	containsAllShell(t, "brief", plainShell(m.viewBrief()), "written just now", "last refresh failed: claude: exit 1")

	pressShell(m, "R")
	if m.briefPending.IsZero() {
		t.Fatal("R asks for a fresh brief")
	}
	if _, err := os.Stat(state.Path("brief-request.json")); err != nil {
		t.Errorf("the request is a file the watcher picks up: %v", err)
	}
	if !strings.Contains(screenShell(m), "writing a fresh one…") {
		t.Errorf("pending brief shows a spinner")
	}

	if err := state.Save("brief.json", brief.Brief{Text: "Fresh summary", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	m.briefPending = time.Now().Add(-time.Minute)
	m.reloadBrief()
	if m.brief.Text != "Fresh summary" || !m.briefPending.IsZero() {
		t.Errorf("a newer brief file is picked up and ends the wait: %q pending=%v", m.brief.Text, m.briefPending)
	}
	m.brief.Text = "kept"
	m.reloadBrief()
	if m.brief.Text != "kept" {
		t.Errorf("an unchanged file isn't reread")
	}
}

func TestInboxDetailShell(t *testing.T) {
	m := newModelShell(t)
	m.switchView(vInbox)
	for _, c := range []struct {
		kind  inboxKind
		wants []string
	}{
		{kMention, []string{"mention  ABC-1", "Payments export · Dev in progress", "Eve Smith", "can you check?"}},
		{kThreads, []string{"threads  billing !21", "ABC-2 Invoices · Code review", "● carol …/pay/export.go:7 rename this"}},
		{kReview, []string{"review  payments !11", "feat(ABC-1): add payments export", "✓ passed", "1 needed", "new"}},
		{kClaude, []string{"claude  payments", "› please add csv"}},
	} {
		for i, it := range m.vis.inbox {
			if it.kind == c.kind {
				m.cur[vInbox].pos = i
			}
		}
		s := plainShell(m.detailInbox())
		containsAllShell(t, kindLabel[c.kind], s, c.wants...)
		if c.kind == kThreads && strings.Contains(s, "done already") {
			t.Errorf("resolved threads aren't listed")
		}
	}
}

func TestInboxKeysShell(t *testing.T) {
	m := newModelShell(t)
	m.switchView(vInbox)
	sel := func(k inboxKind) {
		for i, it := range m.vis.inbox {
			if it.kind == k {
				m.cur[vInbox].pos = i
				return
			}
		}
		t.Fatalf("no %s item", kindLabel[k])
	}

	sel(kClaude)
	if cmd := pressShell(m, "enter"); !isQuitShell(cmd) || m.Result.Attach != "work:3.0" {
		t.Errorf("enter on a waiting session outside tmux hands its pane to main: %q", m.Result.Attach)
	}

	sel(kReview)
	if cmd := pressShell(m, "o"); cmd == nil {
		t.Errorf("o opens the MR")
	}
	if _, ok := m.seen[m.gl.Review[0].WebURL]; !ok {
		t.Errorf("opening a review marks it seen")
	}

	sel(kReview)
	if cmd := pressShell(m, "A"); cmd != nil || !strings.Contains(m.flash, "approve !11 payments — press again") {
		t.Errorf("approve asks first: %q", m.flash)
	}
	if cmd := pressShell(m, "A"); cmd == nil {
		t.Errorf("second A approves")
	}
	sel(kMention)
	if cmd := pressShell(m, "A"); cmd != nil {
		t.Errorf("A only approves reviews")
	}

	sel(kThreads)
	pressShell(m, "B")
	if !strings.Contains(m.flash, "rebase !21 billing onto devel") {
		t.Errorf("rebase asks first: %q", m.flash)
	}
	if cmd := pressShell(m, "B"); cmd == nil || m.flash != "rebasing !21…" {
		t.Errorf("second B rebases: %q", m.flash)
	}

	sel(kThreads)
	pressShell(m, "t")
	if m.chooser == nil || m.chooser.title != "threads on !21" || len(m.chooser.all) != 2 {
		t.Fatalf("t lists the MR's threads")
	}
	pressShell(m, "esc")

	sel(kMention)
	pressShell(m, "v")
	if m.chooser != nil {
		t.Errorf("v does nothing on a mention")
	}

	sel(kReview)
	pressShell(m, "J")
	if m.view != vJira {
		t.Errorf("J jumps to the item's ticket, view %s", viewNames[m.view])
	}
}

func TestInboxClaudeShell(t *testing.T) {
	m := newModelShell(t)
	m.switchView(vInbox)
	for _, c := range []struct {
		kind  inboxKind
		title string
	}{
		{kReview, "review payments in"},
		{kThreads, "fix billing in"},
		{kMention, "reply to Eve Smith in"},
	} {
		for i, it := range m.vis.inbox {
			if it.kind == c.kind {
				m.cur[vInbox].pos = i
			}
		}
		m.chooser = nil
		pressShell(m, "c")
		if m.chooser == nil || m.chooser.title != c.title {
			t.Errorf("%s: c should open %q", kindLabel[c.kind], c.title)
		}
	}
	m.chooser = nil
	for i, it := range m.vis.inbox {
		if it.kind == kReview {
			m.cur[vInbox].pos = i
		}
	}
	pressShell(m, "r")
	if m.chooser == nil || m.chooser.title != "review payments in" {
		t.Errorf("r reviews with claude")
	}
}

func TestWaitingShell(t *testing.T) {
	for _, d := range []time.Duration{time.Hour, 5 * time.Hour, 30 * time.Hour, 100 * time.Hour} {
		at := time.Now().Add(-d)
		if got := plainShell(waiting(at)); got != age(at) {
			t.Errorf("%v: %q", d, got)
		}
	}
	for _, k := range []inboxKind{kReview, kThreads, kMention, kPage, kClaude} {
		if plainShell(kindStyle(k).Render("x")) != "x" || kindLabel[k] == "" {
			t.Errorf("kind %d has no label", k)
		}
	}
}

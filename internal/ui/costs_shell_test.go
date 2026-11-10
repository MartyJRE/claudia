package ui

import (
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/claude"
)

func TestUsageByTicketShell(t *testing.T) {
	m := newModelShell(t)
	m.jira.Sprint = append(m.jira.Sprint, atlassian.Issue{Key: "ABC-5", Summary: "Refunds", Parent: "ABC-100", ParentSummary: "Payments epic"})
	m.jira.Sprint[0].Parent = "ABC-100"
	m.reindex()
	at := time.Date(2026, 11, 3, 10, 0, 0, 0, time.Local)
	m.ledger = claude.OpenLedger()
	m.ledger.Merge([]claude.SessionCost{
		{ID: "aaaaaaaa-wait", Ticket: "ABC-1", Profile: "work", Last: at, Models: map[string]claude.Tokens{"claude-opus-5": {Output: 1_000_000}, "claude-haiku-4-5": {Output: 1_000_000}}},
		{ID: "old-session-1", Ticket: "ABC-5", Profile: "personal", Last: at, Models: map[string]claude.Tokens{"claude-opus-5": {Output: 200_000}}},
		{ID: "old-session-2", Ticket: "ABC-2", Last: at, Models: map[string]claude.Tokens{"mystery": {Output: 9}}},
		{ID: "loose", Models: map[string]claude.Tokens{"claude-haiku-4-5": {Output: 100_000}}},
	})
	m.usageLoaded = true
	m.switchView(vUsage)
	containsAllShell(t, "by day", screenShell(m), "last 31 days", "v layout")

	pressShell(m, "v")
	if !m.usageByTicket() || m.flash != "layout: by ticket" {
		t.Fatalf("v: %v %q", m.usageByTicket(), m.flash)
	}
	s := screenShell(m)
	containsAllShell(t, "by ticket", s, "cost per ticket under its epic", "TICKET", "SESSIONS",
		"ABC-100", "Payments epic", "$35.00", "ABC-1", "Payments export", "$30.00", "ABC-5", "Refunds", "$5.00",
		"no epic", "ABC-2", "Invoices", "$0.00+?", "no ticket", "$0.50", "03 Nov", "━")
	if m.count() != 6 {
		t.Fatalf("rows %d", m.count())
	}
	containsAllShell(t, "epic detail", plainShell(m.detailUsage()), "ABC-100 Payments epic", "2 sessions", "$35.00", "Opus 5", "Haiku 4.5", "tokens")
	pressShell(m, "j")
	containsAllShell(t, "ticket detail", plainShell(m.detailUsage()), "ABC-1 Payments export", "1 sessions", "03 Nov 10:00", "export csv", "work", "Opus 5, Haiku 4.5")
	pressShell(m, "j")
	containsAllShell(t, "a gone session", plainShell(m.detailUsage()), "old-sess", "personal")
	pressShell(m, "j")
	containsAllShell(t, "no epic", plainShell(m.detailUsage()), "no epic", "no price")
	pressShell(m, "G")
	containsAllShell(t, "no ticket", plainShell(m.detailUsage()), "sessions on no ticket", "loose")

	if again := loadListPrefs(); again.Layout[usageLayoutKey] != 1 {
		t.Error("the layout isn't kept")
	}
	pressShell(m, "x")
	pressShell(m, "v")
	if m.usageByTicket() || m.flash != "layout: by day" {
		t.Errorf("back to days: %q", m.flash)
	}
	m.cur[vUsage].pos = 99
	pressShell(m, "v")
	m.cur[vUsage].pos = 99
	if plainShell(m.detailCost()) == "" {
		t.Error("detail off the list")
	}
	m.ledger, m.usageLoaded = nil, false
	m.refilter()
	containsAllShell(t, "empty", screenShell(m), "reading transcripts…")
	m.usageLoaded = true
	containsAllShell(t, "empty", screenShell(m), "no sessions in the cost ledger yet")
}

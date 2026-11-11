package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/runs"
	"github.com/MartyJRE/claudia/internal/standup"
	"github.com/MartyJRE/claudia/internal/teams"
)

func TestStandupShell(t *testing.T) {
	m, db := todoBoardShell(t, "ABC")
	now := time.Now()
	since := now.Add(-20 * time.Hour)
	_ = db.Start(runs.Run{ID: "r1", Subject: "ABC-1", Step: "implement", Session: "s1", Started: now.Add(-3 * time.Hour), Outcome: runs.OK, Reason: "done"})
	_ = db.Start(runs.Run{ID: "r2", Subject: "ABC-2", Step: "plan", Session: "s2", Started: now.Add(-time.Hour), Outcome: runs.Running})
	done, _ := db.AddTodo(runs.Todo{Title: "rotate certs", Created: now.Add(-30 * time.Hour)})
	_ = db.DoneTodo(done.ID, now.Add(-2*time.Hour))
	_, _ = db.AddTodo(runs.Todo{Title: "bump node", Created: now.Add(-time.Hour)})
	_ = db.SetAgreed([]string{"ABC-2"}, "", 0, now)
	merged := mrShell("ledger", 40, "fix: totals", "me", "fix/totals", time.Hour)
	merged.State, merged.MergedAt = "merged", now.Add(-5*time.Hour)
	m.gl.Merged = []gitlab.MR{merged}
	m.gl.Mine[0].CreatedAt = now.Add(-4 * time.Hour)
	m.gl.Review[0].ApprovedBy, m.gl.Review[0].UpdatedAt = []string{m.gl.Me.Username}, now.Add(-time.Hour)
	_ = db.Start(runs.Run{ID: "r3", Subject: m.gl.Review[0].WebURL, Step: "review", Session: "s3", Started: now.Add(-2 * time.Hour), Outcome: runs.NeedsYou, Reason: "2 findings"})
	_ = db.Start(runs.Run{ID: "r4", Subject: done.Subject(), Step: "ticket", Session: "s4", Started: now.Add(-2 * time.Hour), Outcome: runs.OK})
	_ = db.Start(runs.Run{ID: "r5", Subject: "XYZ-9", Step: "qa", Session: "s5", Started: now.Add(-2 * time.Hour), Outcome: runs.Failed})
	waiting := mrShell("search", 50, "feat: search", "bob", "feat/s", 30*time.Hour)
	waiting.MyReview = "reviewed"
	m.gl.Review = append(m.gl.Review, waiting)
	m.teams.Events = []teams.Event{{Subject: "Grooming", Start: now.Add(2 * time.Hour), End: now.Add(3 * time.Hour), Response: "accepted"}}
	m.reindex()

	f := m.StandupData(since)
	titles := map[string]string{}
	for _, r := range f.Yesterday.Runs {
		titles[r.Card] = r.Title
	}
	if titles["ABC-1"] != "Payments export" || titles["payments!11"] == "" || titles[done.Subject()] != "rotate certs" || titles["XYZ-9"] != "" ||
		len(f.Blocked) == 0 || f.Blocked[0].Since.IsZero() ||
		len(f.Yesterday.TodosDone) != 1 || len(f.Today.Todos) != 1 || len(f.Yesterday.Merged) != 1 || len(f.Yesterday.Opened) == 0 ||
		len(f.Today.Agreed) != 1 || len(f.Today.InProgress) == 0 || len(f.Today.Meetings) != 1 || len(f.Yesterday.Worked) == 0 {
		t.Errorf("facts %+v", f)
	}

	// Opening the tab writes the day's points: since from the calendar, then the run.
	fake(t, &writeStandup, func(facts any, lang, dir string) (string, error) {
		if lang != "English" || facts.(StandupFacts).Since.IsZero() {
			t.Errorf("write %v %q", facts, lang)
		}
		return "Yesterday\n- shipped ABC-1\n\nToday\n- ABC-2\n\nBlocked\n- nothing", nil
	})
	m.tm = &teams.Client{}
	fake(t, &calendarPast, func(*teams.Client, time.Time, time.Time) ([]teams.Event, error) {
		return []teams.Event{{Subject: "Daily standup", Start: since, Response: "accepted"}}, nil
	})
	m.switchView(vStandup)
	cmd := pressShell(m, "j")
	if cmd == nil || !m.standupBusy {
		t.Fatal("entering the tab starts the points")
	}
	containsAllShell(t, "busy", screenShell(m), "writing the talking points")
	if pressShell(m, "r"); m.startStandup() != nil {
		t.Error("one at a time")
	}
	sinceMsg := findMsg[standupSinceMsg](cmd)
	if !sinceMsg.since.Equal(since.Local()) {
		t.Errorf("since %v", sinceMsg.since)
	}
	_, next := m.Update(sinceMsg)
	m.Update(next())
	if m.standupBusy || !strings.HasPrefix(m.standup.Text, "Yesterday") {
		t.Fatalf("written %+v", m.standup)
	}
	if p, ok := standup.Load(); !ok || p.Text != m.standup.Text {
		t.Error("saved")
	}
	s := screenShell(m)
	containsAllShell(t, "tab", s, "Standup", "Yesterday", "- shipped ABC-1", "what it was written from", "ABC-1 implement: ok done",
		"merged ledger!40 fix: totals", "ticked off rotate certs", "① ABC-2", "todo: bump node", "Grooming", "r rewrite", "y copy", "since ")
	if m.standupDue() {
		t.Error("written today")
	}
	if pressShell(m, "y") == nil {
		t.Error("y copies")
	}
	if pressShell(m, "z") != nil {
		t.Error("an unbound key")
	}

	// A failed rewrite keeps the last good points.
	fake(t, &writeStandup, func(any, string, string) (string, error) { return "", errors.New("haiku down") })
	m.Update(m.standupSince(standupSinceMsg{since})())
	if m.standup.Text == "" || m.standup.Err != "haiku down" {
		t.Errorf("failed %+v", m.standup)
	}
	containsAllShell(t, "error", screenShell(m), "last write failed: haiku down")

	m.cur[vStandup].pos = 1000
	if l := m.listStandup(5); strings.Count(l, "\n") != 4 {
		t.Errorf("scrolled past the end: %q", l)
	}
}

func TestStandupEmptyShell(t *testing.T) {
	m := newModelShell(t)
	m.standup = standup.Points{}
	if pressShell(m, "y"); m.view != vStandup {
		m.view = vStandup
	}
	m.keyStandup("y")
	if !strings.Contains(m.flash, "no talking points yet") {
		t.Errorf("copy with none: %q", m.flash)
	}
	m.standupBusy = true
	m.refilter()
	if !strings.Contains(plainShell(strings.Join(m.vis.standup, "\n")), "writing") {
		t.Error("busy")
	}
	m.standupBusy = false
	m.refilter()
	if !strings.Contains(plainShell(strings.Join(m.vis.standup, "\n")), "no talking points yet: r writes them") {
		t.Errorf("none: %v", m.vis.standup)
	}
	if !strings.Contains(plainShell(m.standupSubtitle()), "talking points for standup") {
		t.Error("subtitle without points")
	}
}

// findMsg runs cmd, and the commands of a batch, until one gives a T.
func findMsg[T any](cmd tea.Cmd) T {
	var zero T
	if cmd == nil {
		return zero
	}
	switch msg := cmd().(type) {
	case T:
		return msg
	case tea.BatchMsg:
		for _, c := range msg {
			if c == nil {
				continue
			}
			if got := findMsg[T](c); any(got) != any(zero) {
				return got
			}
		}
	}
	return zero
}

func TestWriteStandupShell(t *testing.T) {
	m := newModelShell(t)
	fake(t, &writeStandup, func(any, string, string) (string, error) { return "Yesterday\n- x", nil })
	if p, err := m.WriteStandup(); err != nil || p.Text != "Yesterday\n- x" || p.Since.IsZero() {
		t.Errorf("%+v %v", p, err)
	}
	m.tm = &teams.Client{}
	fake(t, &calendarPast, func(*teams.Client, time.Time, time.Time) ([]teams.Event, error) { return nil, errors.New("offline") })
	fake(t, &writeStandup, func(any, string, string) (string, error) { return "", errors.New("haiku down") })
	if _, err := m.WriteStandup(); err == nil || err.Error() != "haiku down" {
		t.Errorf("failed: %v", err)
	}
}

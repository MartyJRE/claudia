package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/autopilot"
	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/repos"
	"github.com/MartyJRE/claudia/internal/runs"
)

// s on a ticket step runs where the autopilot would, and asks only when
// that's unclear; the pick is kept for the ticket's later steps.
func TestStepRepoShell(t *testing.T) {
	m, db := autoBoardShell(t, nil)
	plan := boardCard{stage: stPlan, issue: atlassian.Issue{Key: "ABC-4", Summary: "Fresh idea"}}
	m.repos = []repos.Repo{{Dir: "/code/a", Branch: "devel"}, {Dir: "/code/b", Branch: "main"}}
	if _, cmd := m.runNext(plan); cmd != nil || m.chooser == nil || m.chooser.title != "implement ABC-4 in" {
		t.Fatalf("unclear: the picker %+v", m.chooser)
	}
	m.chooser.setQuery("code/b")
	cmd := pressShell(m, "enter")
	if dir, _ := db.Get(autopilot.RepoKey("ABC-4")); dir != "/code/b" || cmd == nil {
		t.Errorf("pick kept: %q", dir)
	}
	msg := cmd().(startMsg)
	if msg.sp.Dir != "/code/b" || msg.sp.Kind != "implementation" {
		t.Errorf("start %+v", msg.sp)
	}
	m.chooser = nil
	_ = db.Settle(msg.sp.ID, runs.Failed, "x", time.Now())
	m.runCache = nil
	if _, cmd := m.runNext(plan); cmd == nil || m.chooser != nil {
		t.Fatal("picked before: no question")
	} else if again := cmd().(startMsg); again.sp.Dir != "/code/b" {
		t.Errorf("again in %s", again.sp.Dir)
	}
	m.db = nil
	// Without the log the pick is unknown, and two repos are a question.
	if _, ok := m.stepDir("plan", plan); ok {
		t.Error("no log, yet no question")
	}
}

func TestReviewAutopilotShell(t *testing.T) {
	m, db := autoBoardShell(t, nil)
	review := mrShell("search", 41, "feat: y", "bob", "feat/y", time.Hour)
	_ = db.SetOverride(review.WebURL, "review", config.ModeOff, time.Now())
	c := boardCard{mr: &review, auto: true, over: map[string]string{"review": "off"}, ctx: 0.9, next: "review it"}
	if s := plainShell(reviewChips(c)); !strings.Contains(s, "ctx 90%") || !strings.Contains(s, "⚙ custom") {
		t.Errorf("chips %q", s)
	}
	if s := plainShell(m.detailReview(c)); !strings.Contains(s, "autopilot  review ⚙off") {
		t.Errorf("detail %q", s)
	}
	m.stepChooser(c)
	if len(m.chooser.all) != 1 || m.chooser.all[0].hint != "this card" {
		t.Errorf("steps %+v", m.chooser.all)
	}
}

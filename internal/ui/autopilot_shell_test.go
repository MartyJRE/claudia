package ui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/autopilot"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/runs"
)

// autoBoardShell is the Board with the autopilot on and a run log.
func autoBoardShell(t *testing.T, edit func(*config.Autopilot)) (*Model, *runs.Store) {
	t.Helper()
	m := boardModelShell(t)
	db := withRunsShell(t, m)
	withConfig(t, func(c *config.Config) {
		c.Autopilot.Enabled = true
		if edit != nil {
			edit(&c.Autopilot)
		}
	})
	m.refilter()
	return m, db
}

func cardShell(t *testing.T, m *Model, id string) boardCard {
	t.Helper()
	for _, col := range m.vis.board {
		for _, c := range col {
			if c.id() == id {
				return c
			}
		}
	}
	t.Fatalf("no card %s", id)
	return boardCard{}
}

func TestAutopilotHintsShell(t *testing.T) {
	m, db := autoBoardShell(t, nil)
	containsAllShell(t, "queued", wideCard(t, m, "ABC-4"), "ABC-4  ◷ queued", "◷ readiness queued")
	if c := cardShell(t, m, "ABC-4"); c.turn != turnAgent || c.next != "readiness queued" || !c.hinted {
		t.Errorf("queued: %d %q", c.turn, c.next)
	}

	_ = db.Set(autopilot.KeyBlocked+"ABC-4", "pick a repo")
	m.refilter()
	containsAllShell(t, "blocked", wideCard(t, m, "ABC-4"), "◆ pick a repo · s")

	_ = db.Set(autopilot.KeyBlocked+"ABC-4", "")
	_ = db.SetOverride("ABC-4", runs.AnyStep, config.ModeAsk, time.Now())
	m.refilter()
	containsAllShell(t, "ask", wideCard(t, m, "ABC-4"), "ABC-4  ◆ needs you", "◆ go ahead with readiness? · s", "⚙ ask")
	containsAllShell(t, "modes", plainShell(m.detailBoard()), "autopilot  readiness ⚙ask  plan ⚙ask")

	_ = db.SetOverride("ABC-4", runs.AnyStep, config.ModeOff, time.Now())
	m.refilter()
	if c := cardShell(t, m, "ABC-4"); c.next != "" || c.turn != turnYou || c.hinted {
		t.Errorf("off: %d %q", c.turn, c.next)
	}
}

func TestStripHintsShell(t *testing.T) {
	m := newModelShell(t)
	review := mrShell("payments", 11, "x", "alice", "feat/x", time.Hour)
	hold := func(outcome, reason string) []runs.Run {
		return []runs.Run{{Step: "implement", Outcome: outcome, Reason: reason}}
	}
	for _, c := range []struct {
		name string
		card boardCard
		want string
	}{
		{"context full", boardCard{stage: stPlan, runs: hold(runs.NeedsYou, runs.ContextFull)}, "◆ context full · enter"},
		{"Claude asks", boardCard{stage: stPlan, runs: hold(runs.NeedsYou, "which table?")}, "◆ answer Claude · enter"},
		{"running", boardCard{stage: stPlan, runs: hold(runs.Running, "")}, ""},
		{"findings", boardCard{mr: &review, rstage: rsFindings}, "◆ check findings · enter"},
		{"autopilot off", boardCard{stage: stPlan}, ""},
		{"plan OK", boardCard{stage: stPlan, auto: true, gate: autopilot.PlanOK}, "◆ plan OK? · p"},
		{"backed off", boardCard{stage: stPlan, auto: true, gate: autopilot.Backoff}, "◆ implement failed twice · s"},
		{"dirty", boardCard{stage: stPlan, auto: true, gate: autopilot.Queued, block: "commit or stash in pay"}, "◆ commit or stash in pay · s"},
		{"off", boardCard{stage: stPlan, auto: true, gate: autopilot.Off}, ""},
	} {
		next, h := nextStep(c.card)
		got, ok := m.stripHint(c.card, next, h)
		if plainShell(got) != c.want || ok != (c.want != "") {
			t.Errorf("%s: %q", c.name, plainShell(got))
		}
	}
	for _, c := range []struct {
		card boardCard
		turn turn
		next string
	}{
		{boardCard{stage: stPlan, auto: true, gate: autopilot.PlanOK}, turnNeedsYou, "plan OK?"},
		{boardCard{stage: stPlan, auto: true, gate: autopilot.Backoff}, turnNeedsYou, "implement failed twice"},
		{boardCard{stage: stPlan, runs: hold(runs.NeedsYou, runs.ContextFull)}, turnNeedsYou, "context full"},
	} {
		if tn, next, _ := cardTurn(c.card, nil); tn != c.turn || next != c.next {
			t.Errorf("%+v: %d %q", c.card.gate, tn, next)
		}
	}
	if overrideChip(map[string]string{"plan": "off"}) == "" || !strings.Contains(plainShell(overrideChip(map[string]string{"plan": "off"})), "custom") {
		t.Error("custom chip")
	}
	if ctxChip(0) != "" || plainShell(ctxChip(0.82)) != "ctx 82%" || plainShell(ctxChip(0.5)) != "ctx 50%" {
		t.Error("ctx chip")
	}
}

func TestContextChipShell(t *testing.T) {
	m, db := autoBoardShell(t, nil)
	_ = db.Start(runs.Run{ID: "r1", Kind: "ticket", Subject: "ABC-4", Step: "readiness", Session: "live-run", Started: time.Now()})
	m.sessions = append(m.sessions, row{Session: claude.Session{ID: "live-run", Live: &claude.Live{Status: claude.StatusBusy},
		Transcript: &claude.Transcript{Model: "claude-opus-5", Context: 170_000, Updated: time.Now()}}})
	m.runCache = nil
	m.reindex()
	containsAllShell(t, "chip", screenShell(m), "ctx 85%")
	containsAllShell(t, "now", plainShell(m.detailBoard()), "now readiness running", "ctx 85%")
}

func TestAutopilotPillShell(t *testing.T) {
	m, db := autoBoardShell(t, nil)
	if p := plainShell(m.autopilotPill()); !strings.Contains(p, "⏸ autopilot: the watcher isn't running") {
		t.Errorf("no status: %q", p)
	}
	b, _ := json.Marshal(autopilot.Status{At: time.Now(), Running: 1, Max: 2, Unknown: true})
	_ = db.Set(autopilot.KeyStatus, string(b))
	m.refilter()
	if p := plainShell(m.autopilotPill()); !strings.Contains(p, "▶ autopilot 1/2 · usage unknown") {
		t.Errorf("running: %q", p)
	}
	b, _ = json.Marshal(autopilot.Status{At: time.Now(), Max: 1, Reason: "5h window 64%"})
	_ = db.Set(autopilot.KeyStatus, string(b))
	m.refilter()
	containsAllShell(t, "header", screenShell(m), "⏸ autopilot: 5h window 64%", "P pause")

	// P pauses and resumes, without touching the config.
	pressShell(m, "P")
	if _, ok := db.Get(autopilot.KeyPaused); !ok || !strings.Contains(m.flash, "autopilot paused") {
		t.Errorf("paused: %q", m.flash)
	}
	containsAllShell(t, "paused", screenShell(m), "⏸ autopilot: paused", "P resume")
	pressShell(m, "P")
	if _, ok := db.Get(autopilot.KeyPaused); ok || m.flash != "autopilot resumed" {
		t.Errorf("resumed: %q", m.flash)
	}

	withConfig(t, func(c *config.Config) { c.Autopilot.Enabled = false })
	m.refilter()
	if m.autopilotPill() != "" {
		t.Error("a pill while off")
	}
	pressShell(m, "P")
	if !strings.Contains(m.flash, "the autopilot is off") {
		t.Errorf("off: %q", m.flash)
	}
	m.db = nil
	m.togglePause()
	if !strings.Contains(m.flash, "run log isn't open") {
		t.Errorf("no log: %q", m.flash)
	}
}

func TestPlanOKShell(t *testing.T) {
	m, db := autoBoardShell(t, nil)
	pressShell(m, "p")
	if _, ok := db.Get(autopilot.KeyPlanOK + "ABC-4"); !ok || !strings.Contains(m.flash, "plan of ABC-4 OK") {
		t.Errorf("ok: %q", m.flash)
	}
	pressShell(m, "p")
	if _, ok := db.Get(autopilot.KeyPlanOK + "ABC-4"); ok || !strings.Contains(m.flash, "taken back") {
		t.Errorf("taken back: %q", m.flash)
	}
	review := mrShell("payments", 11, "x", "alice", "feat/x", time.Hour)
	m.planOK(boardCard{mr: &review})
	if !strings.Contains(m.flash, "only a ticket's plan") {
		t.Errorf("review: %q", m.flash)
	}
}

func TestOverrideChooserShell(t *testing.T) {
	m, db := autoBoardShell(t, nil)
	pressShell(m, "A")
	if m.chooser == nil || m.chooser.title != "autopilot for ABC-4" {
		t.Fatalf("chooser %+v", m.chooser)
	}
	m.chooser.setQuery("ask before")
	pressShell(m, "enter")
	if got := db.Overrides()["ABC-4"]; got[runs.AnyStep] != config.ModeAsk || m.flash != "autopilot for ABC-4 set" {
		t.Errorf("ask: %v %q", got, m.flash)
	}
	pressShell(m, "A")
	m.chooser.setQuery("use my settings")
	pressShell(m, "enter")
	if len(db.Overrides()) != 0 {
		t.Error("cleared")
	}
	// Per step: the step, then its mode.
	pressShell(m, "A")
	m.chooser.setQuery("per step")
	pressShell(m, "enter")
	if m.chooser == nil || !strings.Contains(m.chooser.title, "by step") {
		t.Fatalf("steps %+v", m.chooser)
	}
	m.chooser.setQuery("implement")
	pressShell(m, "enter")
	if m.chooser == nil || m.chooser.title != "implement on ABC-4" || len(m.chooser.all) != 5 {
		t.Fatalf("modes %+v", m.chooser)
	}
	m.chooser.setQuery("off")
	pressShell(m, "enter")
	if got := db.Overrides()["ABC-4"]; got["implement"] != config.ModeOff {
		t.Errorf("implement off: %v", got)
	}
	m.modeChooser(cardShell(t, m, "ABC-4"), "merge")
	m.chooser.setQuery("auto")
	pressShell(m, "enter")
	if !strings.Contains(m.flash, "merge still asks") {
		t.Errorf("merge: %q", m.flash)
	}
	containsAllShell(t, "chip", screenShell(m), "⚙ custom")
	m.overrideSet("x", errNoProcess)
	if !m.flashErr {
		t.Error("error")
	}
	m.db = nil
	m.overrideChooser(boardCard{issue: atlassian.Issue{Key: "ABC-4"}})
	if !strings.Contains(m.flash, "run log isn't open") {
		t.Errorf("no log: %q", m.flash)
	}
	m.planOK(boardCard{issue: atlassian.Issue{Key: "ABC-4"}})
}

func TestReviewQueuedShell(t *testing.T) {
	m, _ := autoBoardShell(t, nil)
	m.gl.Review = append(m.gl.Review, mrShell("search", 41, "feat: y", "bob", "feat/y", time.Hour))
	m.boardPrefs.View = "reviews"
	m.refilter()
	var c boardCard
	for _, col := range m.vis.board {
		for _, x := range col {
			if x.review() && x.mr.IID == 41 {
				c = x
			}
		}
	}
	if c.mr == nil || c.turn != turnAgent || c.next != "review queued" {
		t.Errorf("review %d %q", c.turn, c.next)
	}
	_ = gitlab.MR{}
}

func wideCard(t *testing.T, m *Model, id string) string {
	t.Helper()
	return plainShell(strings.Join(m.cardLines(cardShell(t, m, id), 90, false), "\n"))
}

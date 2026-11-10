package autopilot

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/board"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/launch"
	"github.com/MartyJRE/claudia/internal/runs"
)

func TestWakeReadiness(t *testing.T) {
	r := newRig(t)
	asked := now.Add(-time.Hour)
	_ = r.db.Start(runs.Run{ID: "w1", Kind: "ticket", Subject: "ABC-1", Step: "readiness", Session: "sess", Started: asked.Add(-10 * time.Minute),
		Finished: asked, Outcome: runs.Waiting, Reason: "asked 2 questions", StartedBy: runs.ByAutopilot})
	is := issue("ABC-1", "To Do")
	is.Updated = asked.Add(30 * time.Second)
	r.in.Jira.Sprint = append(r.in.Jira.Sprint, is)
	closed := board.Session{Session: claude.Session{ID: "sess", Transcript: &claude.Transcript{Cwd: "/c/anywhere", Updated: asked}}}
	r.in.Sessions = []board.Session{closed}
	_ = r.in.Links.SetTicket("sess", "ABC-1")

	// Only the run's own comment changed the ticket: it keeps waiting.
	if r.tick(); len(r.started) != 0 {
		t.Fatalf("woke on its own comment: %v", r.steps())
	}
	r.in.Jira.Sprint[0].Updated = now.Add(-5 * time.Minute)
	live := closed
	live.Live = &claude.Live{Status: claude.StatusIdle}
	r.in.Sessions = []board.Session{live}
	if r.tick(); len(r.started) != 0 {
		t.Fatalf("resumed a live session: %v", r.steps())
	}
	r.cfg.Autopilot.Steps = map[string]string{"readiness": config.ModeAsk}
	r.in.Sessions = []board.Session{closed}
	if r.tick(); len(r.started) != 0 {
		t.Fatalf("readiness on ask woke by itself: %v", r.steps())
	}
	r.cfg.Autopilot.Steps = nil
	r.tick()
	if len(r.started) != 1 {
		t.Fatalf("answered: %v", r.steps())
	}
	sp := r.started[0]
	if sp.ID != "sess" || sp.Dir != "/c/anywhere" || sp.Color != "" || !slices.Contains(sp.Args, "--resume") || !slices.Contains(sp.Args, wakePrompt) {
		t.Errorf("spec %+v", sp)
	}
	rs := r.db.For("ABC-1")
	if len(rs) != 2 || rs[0].ID == "w1" || rs[0].Session != "sess" || rs[0].Outcome != runs.Running || rs[0].StartedBy != runs.ByAutopilot || rs[1].Outcome != runs.Waiting {
		t.Errorf("runs %+v", rs)
	}
	if !slices.Contains(sp.Env, "CLAUDIA_RUN="+rs[0].ID) {
		t.Errorf("env %q", sp.Env)
	}
	// It runs now: the card holds on it.
	r.started = nil
	if r.tick(); len(r.started) != 0 {
		t.Errorf("woke twice: %v", r.steps())
	}
}

func TestWakeFails(t *testing.T) {
	r := newRig(t)
	_ = r.db.Start(runs.Run{ID: "w1", Kind: "ticket", Subject: "ABC-1", Step: "readiness", Session: "sess", Started: now.Add(-2 * time.Hour),
		Finished: now.Add(-time.Hour), Outcome: runs.NeedsYou, Reason: "asked 4 questions"})
	is := issue("ABC-1", "To Do")
	is.Updated = now
	r.in.Jira.Sprint = append(r.in.Jira.Sprint, is)
	r.in.Sessions = []board.Session{{Session: claude.Session{ID: "sess", Transcript: &claude.Transcript{Cwd: "/c/x"}}}}
	fake(t, &startSession, func(launch.Spec) (string, error) { return "", errors.New("no tmux") })
	r.tick()
	rs := r.db.For("ABC-1")
	if len(rs) != 2 || rs[0].Outcome != runs.Failed || rs[1].Outcome != runs.NeedsYou {
		t.Errorf("a legacy needs-you run wakes too, and a failed start fails only the new run: %+v", rs)
	}
	if _, ok := answered(board.Card{}, nil, nil); ok {
		t.Error("nothing held")
	}
	c := board.Card{Stage: board.UpNext, Issue: is, Runs: rs[1:]}
	if _, ok := answered(c, &rs[1], nil); ok {
		t.Error("no session to resume")
	}
	if _, ok := answered(c, &rs[1], []board.Session{{Session: claude.Session{ID: "sess"}}}); ok {
		t.Error("a session without a dir")
	}
}

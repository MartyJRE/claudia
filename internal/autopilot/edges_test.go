package autopilot

import (
	"testing"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/board"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/runs"
)

func TestEdges(t *testing.T) {
	r := newRig(t)
	r.in.Atl = &atlassian.Client{}
	var exported []string
	fake(t, &exportBrief, func(_ *atlassian.Client, key, _ string) (string, error) {
		exported = append(exported, key)
		return "/b.md", nil
	})
	r.in.Jira.Sprint = []atlassian.Issue{issue("ABC-1", "To Do")}
	// A red session nearby: the run gets another colour.
	r.in.Sessions = []board.Session{{Session: claude.Session{ID: "x", Live: &claude.Live{Status: claude.StatusIdle}, Transcript: &claude.Transcript{Color: "red"}}}}
	// A failed review run is the back-off's business, not a note's.
	m := mr(1, nil)
	r.in.GL.Review = []gitlab.MR{m}
	reviewRun(t, r, "a", m, runs.Failed, "crashed", "head")
	r.tick()
	if len(exported) != 1 || r.started[0].Color == "red" || r.started[0].Color == "" {
		t.Errorf("exported %v colour %q", exported, r.started[0].Color)
	}
	for _, n := range r.notes {
		if n.Title == "Review of !1 found something" {
			t.Error("noted a failed review")
		}
	}
	// The run's own live session doesn't keep the autopilot off the card.
	c := board.Card{Sessions: []board.Session{{Session: claude.Session{ID: "s", Live: &claude.Live{Status: claude.StatusBusy}}}},
		Runs: []runs.Run{{Session: "s"}}}
	if handsOn(c, now) {
		t.Error("its own run")
	}
	r.db.Close()
	if err := r.p.merge(now, board.Card{MRs: []gitlab.MR{mr(2, func(m *gitlab.MR) { m.Pipeline = &gitlab.Pipeline{Status: "success"} })}}, runs.Run{}); err == nil {
		t.Error("recorded a merge in a closed log")
	}
	if err := r.p.start(now, r.cfg, r.in, board.Index{}, candidate{card: board.Card{Issue: issue("ABC-1", "")}, step: "readiness"}); err == nil {
		t.Error("started a run a closed log can't record")
	}
}

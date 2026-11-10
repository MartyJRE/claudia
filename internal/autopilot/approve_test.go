package autopilot

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/runs"
	"github.com/MartyJRE/claudia/internal/teams"
)

type teamsFake struct {
	newErr, fetchErr, meErr error
	posts                   []teams.Post
	reacted                 []string
}

func (f *teamsFake) install(t *testing.T) {
	fake(t, &newTeams, func() (*teams.Client, error) { return &teams.Client{}, f.newErr })
	fake(t, &fetchReview, func(*teams.Client) ([]teams.Post, error) { return f.posts, f.fetchErr })
	fake(t, &teamsMe, func(*teams.Client) (string, error) { return "me-id", f.meErr })
	fake(t, &react, func(_ *teams.Client, id, emoji string) error { f.reacted = append(f.reacted, id+" "+emoji); return nil })
}

func reviewRun(t *testing.T, r *rig, id string, m gitlab.MR, outcome, reason, sha string) {
	t.Helper()
	if err := r.db.Start(runs.Run{ID: id, Kind: "review", Subject: m.WebURL, Step: "review", Session: "s-" + id, SHA: sha,
		Started: now.Add(-time.Hour), Finished: now, Outcome: outcome, Reason: reason}); err != nil {
		t.Fatal(err)
	}
}

func TestAutoApprove(t *testing.T) {
	green := func(m *gitlab.MR) { m.Pipeline = &gitlab.Pipeline{Status: "success"} }
	r := newRig(t)
	clean := mr(1, green)
	findings := mr(2, green)
	moved := mr(3, green)
	running := mr(4, func(m *gitlab.MR) { m.Pipeline = &gitlab.Pipeline{Status: "running"} })
	failed := mr(5, func(m *gitlab.MR) { m.Pipeline = &gitlab.Pipeline{Status: "failed"} })
	r.in.GL.Review = []gitlab.MR{clean, findings, moved, running, failed}
	reviewRun(t, r, "a", clean, runs.OK, "no findings", "head")
	reviewRun(t, r, "b", findings, runs.NeedsYou, "3 findings", "head")
	reviewRun(t, r, "c", moved, runs.OK, "no findings", "older")
	reviewRun(t, r, "d", running, runs.OK, "no findings", "head")
	reviewRun(t, r, "e", failed, runs.OK, "no findings", "head")
	reviewRun(t, r, "f", mr(9, nil), runs.OK, "no findings", "head") // gone from my reviews
	var approved []int
	fake(t, &approveMR, func(m gitlab.MR) error { approved = append(approved, m.IID); return nil })
	tf := &teamsFake{posts: []teams.Post{{ID: "p1", MRURLs: []string{clean.WebURL}}}}
	tf.install(t)
	_ = r.db.Set(KeyPaused, "1")
	r.tick()
	if len(approved) != 0 {
		t.Fatal("approved while paused")
	}
	_ = r.db.Set(KeyPaused, "")
	r.tick()
	if len(approved) != 1 || approved[0] != 1 || len(tf.reacted) != 1 || tf.reacted[0] != "p1 ✅" {
		t.Errorf("approved %v, reacted %v", approved, tf.reacted)
	}
	titles := map[string]string{}
	for _, n := range r.notes {
		titles[n.Title] = n.Body
	}
	if _, ok := titles["Auto-approved !1"]; !ok || titles["Review of !2 found something"] != "3 findings" {
		t.Errorf("notes %v", titles)
	}
	// Acted on once; the running pipeline is looked at again.
	handled := r.db.Prefixed(keyHandled)
	if len(handled) != 4 || handled["d"] != "" {
		t.Errorf("handled %v", handled)
	}
	running.Pipeline.Status = "success"
	r.in.GL.Review[3] = running
	r.notes = nil
	r.tick()
	if len(approved) != 2 || approved[1] != 4 {
		t.Errorf("green now: %v", approved)
	}
	// Handled marks fade after two days.
	r.p.Tick(now.Add(72*time.Hour), r.cfg, r.in)
	if len(r.db.Prefixed(keyHandled)) != 0 {
		t.Error("old marks stay")
	}
}

func TestApproveEdges(t *testing.T) {
	r := newRig(t)
	m := mr(1, nil)
	fake(t, &approveMR, func(gitlab.MR) error { return errors.New("401") })
	r.p.approve(r.cfg, m, "!1")
	if n := r.notes[0]; n.Title != "Couldn't approve !1" || n.Body != "401" {
		t.Errorf("note %+v", n)
	}
	fake(t, &approveMR, func(gitlab.MR) error { return nil })
	for _, c := range []struct {
		name string
		tf   teamsFake
		body string
		n    int
	}{
		{"teams not set up", teamsFake{newErr: teams.ErrUnconfigured}, "", 0},
		{"broken client", teamsFake{newErr: errors.New("bad")}, "bad", 0},
		{"no channel", teamsFake{fetchErr: teams.ErrNoChannel}, "", 0},
		{"signed out", teamsFake{fetchErr: teams.ErrSignedOut}, "not signed in", 0},
		{"no post", teamsFake{}, "", 0},
		{"who am I fails", teamsFake{posts: []teams.Post{{ID: "p", MRURLs: []string{m.WebURL}}}, meErr: errors.New("403")}, "403", 0},
		{"reacted already", teamsFake{posts: []teams.Post{{ID: "p", MRURLs: []string{m.WebURL}, ReactedBy: []string{"me-id"}}}}, "", 0},
	} {
		r.notes = nil
		c.tf.install(t)
		r.p.approve(r.cfg, m, "!1")
		body := r.notes[0].Body
		if c.body == "" && strings.Contains(body, "Teams") || c.body != "" && !strings.Contains(body, c.body) || len(c.tf.reacted) != c.n {
			t.Errorf("%s: %q %v", c.name, body, c.tf.reacted)
		}
	}
	none := ""
	r.cfg.Autopilot.ReviewReactRaw = &none
	fake(t, &newTeams, func() (*teams.Client, error) { panic("no reaction wanted") })
	r.p.approve(r.cfg, m, "!1")
}

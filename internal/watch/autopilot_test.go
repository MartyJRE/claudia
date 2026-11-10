package watch

import (
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/autopilot"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/repos"
	"github.com/MartyJRE/claudia/internal/runs"
	"github.com/MartyJRE/claudia/internal/summary"
	"github.com/MartyJRE/claudia/internal/tmux"
)

func fakeSeam[T any](t *testing.T, seam *T, v T) {
	t.Helper()
	old := *seam
	*seam = v
	t.Cleanup(func() { *seam = old })
}

func pilotWatcher(t *testing.T) *watcher {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	st, err := runs.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	w := newWatcher()
	w.runs = st
	w.summaries = map[string]summary.Summary{}
	w.startPilot(nil)
	t.Cleanup(func() { w.pilot.ledger.Close() })
	return w
}

func TestAutopilotTicks(t *testing.T) {
	w := pilotWatcher(t)
	notes := catchNotes(t)
	scans, finds := 0, 0
	fakeSeam(t, &scanUsage, func(*claude.UsageScanner) []claude.DayModel { scans++; return nil })
	fakeSeam(t, &findRepos, func(string) []repos.Repo { finds++; return []repos.Repo{{Dir: "/c/pay"}} })
	var got []autopilot.Inputs
	fakeSeam(t, &tickPilot, func(p *autopilot.Pilot, _ time.Time, _ config.Config, in autopilot.Inputs) autopilot.Status {
		got = append(got, in)
		p.Notify(autopilot.Note{Title: "Autopilot started plan", ID: "autopilot:r1", Target: "pay:2.0"})
		return autopilot.Status{}
	})
	cfg := config.Get()
	fakeSeam(t, &getConfig, func() config.Config { return cfg })
	at := time.Now()
	w.gitlab(gitlab.Snapshot{Mine: []gitlab.MR{mr(1, "x")}})
	w.jira(atlassian.JiraSnapshot{Sprint: []atlassian.Issue{{Key: "ABC-1"}}})

	// Off: the ledger is still kept, but nothing ticks.
	w.autopilot(at)
	if scans != 1 || len(got) != 0 {
		t.Fatalf("off: %d scans, %d ticks", scans, len(got))
	}
	cfg.Autopilot.Enabled = true
	w.autopilot(at.Add(30 * time.Second))
	if len(got) != 0 {
		t.Error("ticked twice in a minute")
	}
	w.autopilot(at.Add(time.Minute))
	if len(got) != 1 || len(got[0].GL.Mine) != 1 || len(got[0].Jira.Sprint) != 1 || len(got[0].Repos) != 1 || got[0].Ledger == nil || scans != 1 {
		t.Fatalf("inputs %+v, %d scans", got, scans)
	}
	if n := notes.take(); len(n) != 1 || n[0].thread != "autopilot" || n[0].target != "pay:2.0" {
		t.Errorf("notes %+v", n)
	}
	w.autopilot(at.Add(2 * time.Minute))
	w.autopilot(at.Add(6 * time.Minute))
	if finds != 2 || scans != 2 {
		t.Errorf("repos found %d times, usage scanned %d", finds, scans)
	}
	(&watcher{}).autopilot(at)
}

func TestContextNotes(t *testing.T) {
	w := pilotWatcher(t)
	notes := catchNotes(t)
	_ = w.runs.Start(runs.Run{ID: "r1", Kind: "ticket", Subject: "ABC-1", Step: "implement", Session: "s1", Started: time.Now()})
	_ = w.runs.Start(runs.Run{ID: "r2", Kind: "ticket", Subject: "ABC-2", Step: "plan", Session: "s2", Started: time.Now()})
	all := []claude.Session{
		{ID: "s1", Transcript: &claude.Transcript{Model: "claude-opus-5", Context: 182_000}, Live: &claude.Live{PID: 1}},
		{ID: "s2", Transcript: &claude.Transcript{Model: "claude-opus-5", Context: 100_000}},
	}
	loc := &tmux.Locator{}
	w.contextNotes(all, loc)
	w.contextNotes(all, loc)
	n := notes.take()
	if len(n) != 1 || n[0].id != "ctx:r1" || n[0].thread != "autopilot" || n[0].title != "Context 90% · implement on ABC-1" || n[0].body == "" {
		t.Errorf("notes %+v", n)
	}
	(&watcher{}).contextNotes(all, loc)
}

package autopilot

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/board"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/launch"
	"github.com/MartyJRE/claudia/internal/links"
	"github.com/MartyJRE/claudia/internal/repos"
	"github.com/MartyJRE/claudia/internal/runs"
	"github.com/MartyJRE/claudia/internal/teams"
)

func TestMain(m *testing.M) {
	dir, _ := os.MkdirTemp("", "claudia-test")
	os.Setenv("XDG_CONFIG_HOME", dir)
	os.Setenv("XDG_STATE_HOME", dir)
	os.Setenv("HOME", dir)
	os.Setenv("CLAUDIA_JIRA_KEYS", "ABC")
	os.Setenv("CLAUDIA_GITLAB_HOST", "gitlab.example.com")
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// Tuesday morning.
var now = time.Date(2026, 11, 10, 10, 0, 0, 0, time.Local)

func fake[T any](t *testing.T, seam *T, v T) {
	t.Helper()
	old := *seam
	*seam = v
	t.Cleanup(func() { *seam = old })
}

type rig struct {
	p       *Pilot
	db      *runs.Store
	cfg     config.Config
	in      Inputs
	notes   []Note
	started []launch.Spec
	merged  []int
}

func fresh(pct float64) claude.Limits {
	return claude.Limits{At: now.Add(-time.Minute),
		FiveHour: &claude.Window{UsedPercentage: pct, ResetsAt: float64(now.Add(2 * time.Hour).Unix())},
		SevenDay: &claude.Window{UsedPercentage: pct, ResetsAt: float64(now.Add(72 * time.Hour).Unix())}}
}

func newRig(t *testing.T) *rig {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	db, err := runs.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := &rig{db: db, cfg: config.Get()}
	r.cfg.Autopilot = config.Autopilot{Enabled: true}
	r.p = &Pilot{DB: db, Notify: func(n Note) { r.notes = append(r.notes, n) }}
	r.in = Inputs{Links: links.Load(), Profiles: []claude.Profile{{Name: "work", Dir: "/h/.claude-work"}}}
	r.in.GL.Me.Username = "me"
	fake(t, &startSession, func(sp launch.Spec) (string, error) {
		r.started = append(r.started, sp)
		return "repo:3", nil
	})
	fake(t, &mergeMR, func(mr gitlab.MR) error { r.merged = append(r.merged, mr.IID); return nil })
	fake(t, &approveMR, func(gitlab.MR) error { panic("approve not faked") })
	fake(t, &exportBrief, func(*atlassian.Client, string, string) (string, error) { return "/b.md", nil })
	fake(t, &dirty, func(string) bool { return false })
	fake(t, &readLimits, func(string) (claude.Limits, bool) { return fresh(10), true })
	fake(t, &newTeams, func() (*teams.Client, error) { return nil, teams.ErrUnconfigured })
	return r
}

func (r *rig) tick() Status { return r.p.Tick(now, r.cfg, r.in) }

func (r *rig) steps() []string {
	var out []string
	for _, sp := range r.started {
		out = append(out, sp.Kind+" "+sp.Name)
	}
	return out
}

func issue(key, status string) atlassian.Issue {
	return atlassian.Issue{Key: key, Summary: "do " + key, Status: status, Category: "new"}
}

func mr(iid int, edit func(*gitlab.MR)) gitlab.MR {
	m := gitlab.MR{IID: iid, ProjectID: 1, State: "opened", Title: "feat: x", SourceBranch: "feat/x", SHA: "head",
		WebURL: "https://gitlab.example.com/g/pay/-/merge_requests/" + string(rune('0'+iid)), CreatedAt: now.Add(-time.Duration(iid) * time.Hour), UpdatedAt: now.Add(-time.Duration(iid) * time.Hour)}
	m.Author.Username = "alice"
	if edit != nil {
		edit(&m)
	}
	return m
}

var pay = repos.Repo{Dir: "/c/pay", Project: "g/pay", Branch: "feat/x"}

func TestQueueOrderAndMaxRunning(t *testing.T) {
	r := newRig(t)
	r.in.Jira.Sprint = []atlassian.Issue{issue("ABC-1", "To Do"), issue("ABC-2", "In Test")}
	r.in.GL.Review = []gitlab.MR{mr(1, nil)}
	r.in.Repos = []repos.Repo{pay, {Dir: "/c/qa", Branch: "feat/ABC-2/y"}}
	st := r.tick()
	// One at a time by default: the review is the most blocking.
	if len(r.started) != 1 || r.started[0].Kind != "review" || st.Running != 1 || st.Queued != 2 || st.Reason != "" {
		t.Fatalf("started %v, status %+v", r.steps(), st)
	}
	sp := r.started[0]
	got := r.db.For(r.in.GL.Review[0].WebURL)
	if len(got) != 1 || got[0].StartedBy != runs.ByAutopilot || got[0].SHA != "head" || got[0].Session != sp.ID || sp.Dir != "/c/pay" {
		t.Errorf("run %+v in %s", got, sp.Dir)
	}
	if !slices.Contains(sp.Args, "--permission-mode") || !slices.Contains(sp.Env, "CLAUDIA_RUN="+got[0].ID) || sp.Color == "" ||
		!slices.ContainsFunc(sp.Args, func(a string) bool { return strings.Contains(a, "> "+r.cfg.Autopilot.CommentNote()) }) {
		t.Errorf("spec %q %q", sp.Args, sp.Env)
	}
	if n := r.notes[0]; n.Title != "Autopilot started review" || n.Target != "repo:3.0" || !strings.HasPrefix(n.Subtitle, "!1") {
		t.Errorf("note %+v", n)
	}
	if ms := r.in.Links.MRs(sp.ID); len(ms) != 1 {
		t.Errorf("links %v", ms)
	}
	// It runs: that card holds, and the slot is taken.
	r.started = nil
	if st := r.tick(); len(r.started) != 0 || st.Running != 1 {
		t.Errorf("full: %v %+v", r.steps(), st)
	}
	three := 3
	r.cfg.Autopilot.MaxRunningRaw = &three
	r.tick()
	if len(r.started) != 2 || r.started[0].Kind != "qa" || r.started[1].Kind != "readiness" {
		t.Errorf("then qa and readiness: %v", r.steps())
	}
	if r.started[1].Dir != r.cfg.CodeRoot || slices.Contains(r.started[0].Args, "R ABC-1") {
		t.Errorf("readiness runs anywhere: %s", r.started[1].Dir)
	}
	if st, ok := ReadStatus(r.db); !ok || st.Running != 3 || st.Max != 3 {
		t.Errorf("status %+v", st)
	}
}

func TestLimits(t *testing.T) {
	stale := fresh(10)
	stale.At = now.Add(-20 * time.Minute)
	late := fresh(64)
	late.FiveHour.ResetsAt = float64(now.Add(9 * time.Hour).Unix())
	for _, c := range []struct {
		name   string
		lim    claude.Limits
		ok     bool
		reason string
		known  bool
	}{
		{"fine", fresh(10), true, "", true},
		{"5h", fresh(64), true, "5h window 64%", true},
		{"5h resetting after hours", late, true, "", true},
		{"7d", claude.Limits{At: now, FiveHour: fresh(10).FiveHour, SevenDay: fresh(85).SevenDay}, true, "7d window 85%", true},
		{"older than 15 minutes", stale, true, "", false},
		{"missing", claude.Limits{}, false, "", false},
	} {
		if reason, known := usage(config.Autopilot{}, c.lim, c.ok, now); reason != c.reason || known != c.known {
			t.Errorf("%s: %q %v", c.name, reason, known)
		}
	}
	for at, want := range map[time.Time]string{now: "", now.Add(10 * time.Hour): "outside working hours", now.Add(4 * 24 * time.Hour): "outside working hours"} {
		if got := limit(config.Autopilot{}, at, 0); got != want {
			t.Errorf("%v: %q, want %q", at, got, want)
		}
	}
	if reason := limit(config.Autopilot{}, now, 31); reason != "spent $31 of $30 today" {
		t.Errorf("day budget %q", reason)
	}
}

func TestLimitStopsStarts(t *testing.T) {
	r := newRig(t)
	r.in.Jira.Sprint = []atlassian.Issue{issue("ABC-1", "To Do")}
	fake(t, &readLimits, func(string) (claude.Limits, bool) { return fresh(70), true })
	if st := r.tick(); len(r.started) != 0 || st.Reason != "5h window 70%" || st.Queued != 1 {
		t.Errorf("%v %+v", r.steps(), st)
	}
	fake(t, &readLimits, func(string) (claude.Limits, bool) { return claude.Limits{}, false })
	if st := r.tick(); len(r.started) != 1 || !st.Unknown || st.Max != 1 {
		t.Errorf("unknown usage still allows one: %v %+v", r.steps(), st)
	}
	if r.notes[0].Title != "Autopilot resumed" {
		t.Errorf("notes %+v", r.notes)
	}
	// The day's budget counts the autopilot's sessions only.
	db, _ := r.db, 0
	l := claude.OpenLedger()
	t.Cleanup(func() { l.Close() })
	r.in.Ledger = l
	r.in.Ledger.Merge([]claude.SessionCost{{ID: r.started[0].ID, Models: map[string]claude.Tokens{"claude-opus-5": {Output: 3_000_000}}}})
	r.in.Ledger.Save()
	if usd := db.AutopilotUSD(startOfDay(now)); usd < 30 {
		t.Fatalf("spent %v", usd)
	}
	if st := r.tick(); !strings.HasPrefix(st.Reason, "spent $") {
		t.Errorf("budget %+v", st)
	}
}

func TestPausedAndNotes(t *testing.T) {
	r := newRig(t)
	r.in.Jira.Sprint = []atlassian.Issue{issue("ABC-1", "To Do")}
	_ = r.db.Set(KeyPaused, "1")
	if st := r.tick(); len(r.started) != 0 || st.Reason != "paused" || len(r.notes) != 0 {
		t.Errorf("paused: %v %+v %+v", r.steps(), st, r.notes)
	}
	_ = r.db.Set(KeyPaused, "")
	r.tick()
	if len(r.started) != 1 || len(r.notes) != 2 || r.notes[0].Title != "Autopilot resumed" || r.notes[0].ID != "autopilot:state" {
		t.Errorf("resumed %+v", r.notes)
	}
	_ = r.db.Set(KeyPaused, "1")
	r.tick()
	if n := r.notes[len(r.notes)-1]; n.Title != "Autopilot paused" || n.Body != "paused" {
		t.Errorf("paused note %+v", n)
	}
}

func TestBlocked(t *testing.T) {
	r := newRig(t)
	r.in.Jira.Sprint = []atlassian.Issue{issue("ABC-1", "Analysis"), issue("ABC-2", "Analysis"), issue("ABC-3", "Analysis")}
	r.cfg.Autopilot.Steps = map[string]string{"implement": "auto"}
	r.in.Repos = []repos.Repo{{Dir: "/c/a", Branch: "feat/ABC-1/a"}, {Dir: "/c/b", Branch: "feat/ABC-1/b"}, {Dir: "/c/dirty", Branch: "feat/ABC-2/x"}, {Dir: "/c/three", Branch: "feat/ABC-3/x"}}
	fake(t, &dirty, func(dir string) bool { return dir == "/c/dirty" })
	l := claude.OpenLedger()
	t.Cleanup(func() { l.Close() })
	r.in.Ledger = l
	r.in.Ledger.Merge([]claude.SessionCost{{ID: "old", Ticket: "ABC-3", Models: map[string]claude.Tokens{"claude-opus-5": {Output: 3_000_000}}}})
	r.tick()
	got := r.db.Prefixed(KeyBlocked)
	if len(r.started) != 0 || got["ABC-1"] != "pick a repo" || got["ABC-2"] != "commit or stash in dirty" || !strings.HasPrefix(got["ABC-3"], "spent $") {
		t.Errorf("blocked %v, started %v", got, r.steps())
	}
	// Picked once, the repo is remembered; a clean clone clears the block.
	_ = r.db.Set(RepoKey("ABC-1"), "/c/b")
	fake(t, &dirty, func(string) bool { return false })
	r.in.Ledger = nil
	three := 3
	r.cfg.Autopilot.MaxRunningRaw = &three
	r.tick()
	if len(r.db.Prefixed(KeyBlocked)) != 0 || len(r.started) != 3 || r.started[0].Dir != "/c/b" {
		t.Errorf("unblocked %v %v", r.db.Prefixed(KeyBlocked), r.steps())
	}
}

func TestReviewPacingAndClones(t *testing.T) {
	r := newRig(t)
	r.in.GL.Review = []gitlab.MR{mr(1, nil), mr(2, nil)}
	r.in.Repos = []repos.Repo{pay}
	three := 3
	r.cfg.Autopilot.MaxRunningRaw = &three
	r.tick()
	// The MR with the most recent activity goes first, and only one review per hour.
	if len(r.started) != 1 || !strings.Contains(r.started[0].Name, "review") || r.db.For(r.in.GL.Review[0].WebURL) == nil {
		t.Fatalf("one review: %v", r.steps())
	}
	r.in.Repos = []repos.Repo{{Dir: "/c/1", Project: "g/pay", Branch: "devel"}, {Dir: "/c/2", Project: "g/pay", Branch: "main"}}
	r.p.Tick(now.Add(2*time.Hour), r.cfg, r.in)
	if len(r.started) != 1 || r.db.Prefixed(KeyBlocked)[r.in.GL.Review[1].WebURL] != "pick a repo" {
		t.Errorf("two clones on other branches: %v", r.db.Prefixed(KeyBlocked))
	}
}

func TestModesAndBackoff(t *testing.T) {
	r := newRig(t)
	r.in.Jira.Sprint = []atlassian.Issue{issue("ABC-1", "Analysis")}
	r.in.Repos = []repos.Repo{{Dir: "/c/a", Branch: "feat/ABC-1/a"}}
	// implement waits for the plan OK by default.
	if r.tick(); len(r.started) != 0 {
		t.Fatal("started without a plan OK")
	}
	_ = r.db.Set(KeyPlanOK+"ABC-1", "1")
	_ = r.db.Set(KeyPaused, "")
	if r.tick(); len(r.started) != 1 || r.started[0].Kind != "implementation" {
		t.Fatalf("after p: %v", r.steps())
	}
	run := r.db.For("ABC-1")[0]
	_ = r.db.Settle(run.Session, runs.Failed, "x", now)
	_ = r.db.Start(runs.Run{ID: "f2", Subject: "ABC-1", Step: "implement", Session: "s2", Started: now.Add(time.Minute), Outcome: runs.Failed, StartedBy: runs.ByAutopilot})
	r.started = nil
	if r.tick(); len(r.started) != 0 {
		t.Error("two failures: backed off")
	}
	_ = r.db.Start(runs.Run{ID: "f3", Subject: "ABC-1", Step: "implement", Session: "s3", Started: now.Add(2 * time.Minute), Outcome: runs.Failed})
	if r.tick(); len(r.started) != 1 {
		t.Error("my own start resets the back-off")
	}
}

func TestGate(t *testing.T) {
	a := config.Autopilot{Steps: map[string]string{"qa": "off", "mr": "ask"}}
	plan := func(reason string) board.Card {
		return board.Card{Runs: []runs.Run{{Step: "plan", Outcome: runs.OK, Reason: reason}}}
	}
	for _, c := range []struct {
		name   string
		over   map[string]string
		card   board.Card
		step   string
		planOK bool
		want   Gate
	}{
		{"auto", nil, board.Card{}, "readiness", false, Queued},
		{"off", nil, board.Card{}, "qa", false, Off},
		{"ask", nil, board.Card{}, "mr", false, Ask},
		{"merge asks by default", nil, board.Card{}, "merge", false, Ask},
		{"plan OK wanted", nil, plan("size: medium"), "implement", false, PlanOK},
		{"a simple plan needs none", nil, plan("posted · size: simple"), "implement", false, Queued},
		{"p pressed", nil, board.Card{}, "implement", true, Queued},
		{"card asks for everything", map[string]string{"*": "ask"}, board.Card{}, "readiness", false, Ask},
		{"card's own step wins over *", map[string]string{"*": "ask", "qa": "auto"}, board.Card{}, "qa", false, Queued},
		{"card turns everything off", map[string]string{"*": "off"}, board.Card{}, "review", false, Off},
		{"card can't auto-merge", map[string]string{"merge": "auto"}, board.Card{}, "merge", false, Ask},
		{"a bad override is ignored", map[string]string{"plan": "after-plan-ok"}, board.Card{}, "plan", false, Queued},
	} {
		if got := GateOf(a, c.over, c.card, c.step, c.planOK); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
	a.AllowAutoMerge = true
	if m, own := Mode(a, map[string]string{"merge": "auto"}, "merge"); m != "auto" || !own {
		t.Errorf("allowed auto merge: %s %v", m, own)
	}
	if _, own := Mode(a, nil, "plan"); own {
		t.Error("no override")
	}
}

func TestMergeAndHandsOn(t *testing.T) {
	r := newRig(t)
	ready := mr(5, func(m *gitlab.MR) {
		m.SourceBranch, m.Title = "feat/ABC-1/x", "feat(ABC-1): x"
		m.Pipeline = &gitlab.Pipeline{Status: "success"}
	})
	r.in.GL.Mine = []gitlab.MR{ready}
	r.in.Jira.Sprint = []atlassian.Issue{issue("ABC-1", "Code review")}
	r.cfg.Autopilot.AllowAutoMerge = true
	r.cfg.Autopilot.Steps = map[string]string{"merge": "auto"}
	// Someone's session on the card keeps the autopilot off it.
	r.in.Sessions = []board.Session{{Session: claude.Session{ID: "mine", Live: &claude.Live{Status: claude.StatusBusy},
		Transcript: &claude.Transcript{Branch: "feat/ABC-1/x", Updated: now}}}}
	if r.tick(); len(r.merged) != 0 {
		t.Fatal("merged under someone's hands")
	}
	r.in.Sessions = nil
	r.tick()
	got := r.db.For("ABC-1")
	if len(r.merged) != 1 || len(got) != 1 || got[0].Outcome != runs.OK || got[0].Step != "merge" || r.notes[0].Title != "Autopilot merged !5" {
		t.Fatalf("merged %v %+v %+v", r.merged, got, r.notes)
	}
	fake(t, &mergeMR, func(gitlab.MR) error { return errors.New("405") })
	_ = r.db.Settle("", runs.Failed, "", now) // the run is settled already
	r.in.Jira.Sprint = []atlassian.Issue{issue("ABC-1", "Code review")}
	if err := r.p.merge(now, board.Card{Issue: issue("ABC-1", ""), MRs: []gitlab.MR{ready}}, runs.Run{ID: "m2", Subject: "ABC-1", Step: "merge"}); err != nil {
		t.Fatal(err)
	}
	if n := r.notes[len(r.notes)-1]; n.Title != "Autopilot couldn't merge !5" || n.Body != "405" {
		t.Errorf("failed merge %+v", n)
	}
	if err := r.p.merge(now, board.Card{Issue: issue("ABC-1", "")}, runs.Run{}); err == nil {
		t.Error("nothing to merge")
	}
}

func TestStartFails(t *testing.T) {
	r := newRig(t)
	r.in.Jira.Sprint = []atlassian.Issue{issue("ABC-1", "To Do")}
	fake(t, &startSession, func(launch.Spec) (string, error) { return "", errors.New("no tmux") })
	r.tick()
	got := r.db.For("ABC-1")
	if len(got) != 1 || got[0].Outcome != runs.Failed || got[0].Reason != "no tmux" {
		t.Errorf("run %+v", got)
	}
	if n := r.notes[0]; n.Title != "Autopilot couldn't start readiness" || n.Body != "no tmux" {
		t.Errorf("note %+v", n)
	}
	// Feedback: threads on my MR, in its clone.
	r = newRig(t)
	threads := mr(7, func(m *gitlab.MR) {
		m.SourceBranch, m.Title = "feat/ABC-2/x", "feat(ABC-2): x"
		m.Threads = []gitlab.Thread{{}}
	})
	r.in.GL.Mine = []gitlab.MR{threads}
	r.in.Jira.Sprint = []atlassian.Issue{issue("ABC-2", "Code review")}
	r.in.Repos = []repos.Repo{pay}
	r.tick()
	if len(r.started) != 1 || r.started[0].Kind != "fix" || r.db.For("ABC-2")[0].SHA != "head" {
		t.Errorf("feedback %v", r.steps())
	}
}

func TestWorkingHoursDontNotify(t *testing.T) {
	p := &Pilot{}
	var notes []Note
	p.Notify = func(n Note) { notes = append(notes, n) }
	for _, reason := range []string{"", offHours, "", offHours, "5h window 64%", offHours, ""} {
		p.tell(reason)
	}
	var got []string
	for _, n := range notes {
		got = append(got, n.Title+": "+n.Body)
	}
	want := []string{"Autopilot paused: 5h window 64%"}
	if !slices.Equal(got, want) {
		t.Errorf("notes %q, want %q", got, want)
	}
}

func TestStaleReviewsSkipped(t *testing.T) {
	r := newRig(t)
	old := mr(2, func(m *gitlab.MR) { m.UpdatedAt = now.Add(-31 * 24 * time.Hour) })
	r.in.GL.Review = []gitlab.MR{old}
	r.in.Repos = []repos.Repo{pay}
	r.tick()
	if len(r.started) != 0 || r.db.Prefixed(KeyBlocked)[old.WebURL] != "stale review request" {
		t.Fatalf("a month-old request: %v %v", r.steps(), r.db.Prefixed(KeyBlocked))
	}
	r.cfg.Autopilot.ReviewStaleAfterRaw = "off"
	r.tick()
	if len(r.started) != 1 {
		t.Errorf("stale off reviews it: %v", r.steps())
	}
}

func TestProfilesAndTheirUsage(t *testing.T) {
	r := newRig(t)
	r.in.Profiles = []claude.Profile{{Name: "work", Dir: "/h/.claude"}, {Name: "personal", Dir: "/h/.claude-personal"}}
	three := 3
	r.cfg.Autopilot.MaxRunningRaw = &three
	// A ticket's step runs on the work profile even outside the work dir; a
	// review of a personal repo runs on the personal profile, and each is held
	// to its own profile's plan usage.
	r.in.Jira.Sprint = []atlassian.Issue{issue("ABC-1", "To Do")}
	r.in.GL.Review = []gitlab.MR{mr(1, nil)}
	r.in.Repos = []repos.Repo{pay}
	personalBusy := func(path string) (claude.Limits, bool) {
		if strings.Contains(path, "claude-personal") {
			return fresh(70), true
		}
		return fresh(10), true
	}
	fake(t, &readLimits, personalBusy)
	st := r.tick()
	if len(r.started) != 1 || r.started[0].Profile.Name != "work" || !strings.Contains(r.started[0].Name, "ABC-1") {
		t.Fatalf("the ticket runs on work, the review waits: %v %+v", r.steps(), st)
	}
	if st.Reason != "" || st.Queued != 1 {
		t.Errorf("status %+v", st)
	}
	// With only the held review left, the status says why.
	r.in.Jira.Sprint = nil
	if st := r.tick(); len(r.started) != 1 || st.Reason != "5h window 70%" {
		t.Errorf("held by the personal window: %v %+v", r.steps(), st)
	}
	// Unknown usage lets one run go, not a second.
	fake(t, &readLimits, func(string) (claude.Limits, bool) { return claude.Limits{}, false })
	r.in.Jira.Sprint = []atlassian.Issue{issue("ABC-2", "To Do")}
	if st := r.tick(); len(r.started) != 1 || !st.Unknown {
		t.Errorf("unknown with a run going: %v %+v", r.steps(), st)
	}
}

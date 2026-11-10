package ui

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/launch"
	"github.com/MartyJRE/claudia/internal/repos"
	"github.com/MartyJRE/claudia/internal/runs"
	"github.com/MartyJRE/claudia/internal/steps"
	"github.com/MartyJRE/claudia/internal/tmux"
)

// withRunsShell gives the model a run log in the test's state dir.
func withRunsShell(t *testing.T, m *Model) *runs.Store {
	t.Helper()
	db, err := runs.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	m.db, m.runCache = db, nil
	return db
}

func runShell(step, outcome string, edit func(*runs.Run)) runs.Run {
	r := runs.Run{ID: "r-" + step + "-" + outcome, Step: step, Session: "s-" + step, Outcome: outcome, Started: time.Now().Add(-time.Hour)}
	if outcome != runs.Running {
		r.Finished = time.Now().Add(-10 * time.Minute)
	}
	if edit != nil {
		edit(&r)
	}
	return r
}

func TestNextStepShell(t *testing.T) {
	ready := func(mr *gitlab.MR) { mr.Pipeline = &gitlab.Pipeline{Status: "success"} }
	threads := boardMR(7, "opened", func(mr *gitlab.MR) { mr.Threads = []gitlab.Thread{{}} })
	review := mrShell("payments", 11, "x", "alice", "feat/x", time.Hour)
	sha := func(s string) func(*runs.Run) { return func(r *runs.Run) { r.SHA = s } }
	for _, c := range []struct {
		name      string
		card      boardCard
		next      string
		holdsOn   string
		holdState string
	}{
		{"up next: readiness first", boardCard{stage: stUpNext}, "readiness", "", ""},
		{"readiness ok: plan", boardCard{stage: stUpNext, runs: []runs.Run{runShell("readiness", runs.OK, nil)}}, "plan", "", ""},
		{"readiness failed: again", boardCard{stage: stUpNext, runs: []runs.Run{runShell("readiness", runs.Failed, nil)}}, "readiness", "", ""},
		{"readiness asked", boardCard{stage: stUpNext, runs: []runs.Run{runShell("readiness", runs.NeedsYou, nil)}}, "readiness", "readiness", runs.NeedsYou},
		{"both done", boardCard{stage: stUpNext, runs: []runs.Run{runShell("plan", runs.OK, nil), runShell("readiness", runs.OK, nil)}}, "", "", ""},
		{"plan: implement", boardCard{stage: stPlan}, "implement", "", ""},
		{"anything running holds", boardCard{stage: stPlan, runs: []runs.Run{runShell("plan", runs.Running, nil)}}, "plan", "plan", runs.Running},
		{"building without an MR: open one", boardCard{stage: stBuilding}, "mr", "", ""},
		{"building with a draft", boardCard{stage: stBuilding, mrs: []gitlab.MR{boardMR(4, "opened", func(mr *gitlab.MR) { mr.Draft = true })}}, "", "", ""},
		{"review without threads waits", boardCard{stage: stReview, mrs: []gitlab.MR{boardMR(5, "opened", nil)}}, "", "", ""},
		{"threads: feedback", boardCard{stage: stReview, mrs: []gitlab.MR{threads}}, "feedback", "", ""},
		{"feedback done on this head", boardCard{stage: stReview, mrs: []gitlab.MR{threads}, runs: []runs.Run{runShell("feedback", runs.OK, sha(threads.SHA))}}, "", "", ""},
		{"a push since the feedback", boardCard{stage: stReview, mrs: []gitlab.MR{threads}, runs: []runs.Run{runShell("feedback", runs.OK, sha("old"))}}, "feedback", "", ""},
		{"merge", boardCard{stage: stMerge, mrs: []gitlab.MR{boardMR(6, "opened", ready)}}, "merge", "", ""},
		{"merged already", boardCard{stage: stMerge, runs: []runs.Run{runShell("merge", runs.OK, nil)}}, "", "", ""},
		{"qa", boardCard{stage: stQA}, "qa", "", ""},
		{"qa written", boardCard{stage: stQA, runs: []runs.Run{runShell("qa", runs.OK, nil)}}, "", "", ""},
		{"done", boardCard{stage: stDone}, "", "", ""},
		{"a requested review", boardCard{mr: &review, rstage: rsRequested}, "review", "", ""},
		{"reviewed this head", boardCard{mr: &review, rstage: rsRequested, runs: []runs.Run{runShell("review", runs.OK, sha(review.SHA))}}, "", "", ""},
		{"a push since the review", boardCard{mr: &review, rstage: rsRequested, runs: []runs.Run{runShell("review", runs.OK, sha("old"))}}, "review", "", ""},
		{"findings: nothing to run", boardCard{mr: &review, rstage: rsFindings}, "", "", ""},
		{"a review running", boardCard{mr: &review, rstage: rsReviewing, runs: []runs.Run{runShell("review", runs.Running, nil)}}, "review", "review", runs.Running},
	} {
		t.Run(c.name, func(t *testing.T) {
			next, hold := nextStep(c.card)
			if next != c.next {
				t.Errorf("next %q, want %q", next, c.next)
			}
			switch {
			case c.holdsOn == "" && hold != nil:
				t.Errorf("holds on %+v", hold)
			case c.holdsOn != "" && (hold == nil || hold.Step != c.holdsOn || hold.Outcome != c.holdState):
				t.Errorf("holds on %+v, want %s %s", hold, c.holdsOn, c.holdState)
			}
		})
	}
	if stepLabel("mr") != "open MR" || stepLabel("findings") != "findings" || stepLabel("other") != "other" {
		t.Error("step labels")
	}
}

func TestReviewStageBySHAShell(t *testing.T) {
	mr := mrShell("payments", 11, "x", "alice", "feat/x", time.Hour)
	last := reviewSessShell("r-1", mr.WebURL, claude.StatusIdle, "Done: one nit")
	// The MR moved after the review; with a run that read this head, only a push counts.
	last.Updated = mr.UpdatedAt.Add(-time.Hour)
	last.summaryFor = last.Updated
	rs := []runs.Run{{Step: "review", Session: "r-1", SHA: mr.SHA}}
	if st, tn, next, _, why := reviewStage(mr, []row{last}, rs, "me"); st != rsFindings || tn != turnNeedsYou || next != "one nit" || !strings.Contains(why, "finished its review") {
		t.Errorf("same head: %d %d %q %q", st, tn, next, why)
	}
	rs[0].SHA = "older"
	if st, _, next, _, why := reviewStage(mr, []row{last}, rs, "me"); st != rsRequested || next != "review it again" || !strings.Contains(why, "new commits since") {
		t.Errorf("a push: %d %q %q", st, next, why)
	}
	// Without a run row the old rule holds.
	if st, _, _, _, why := reviewStage(mr, []row{last}, []runs.Run{{Session: "other"}}, "me"); st != rsRequested || !strings.Contains(why, "changed after") {
		t.Errorf("no run: %d %q", st, why)
	}
}

func TestWaitingForAnswersShell(t *testing.T) {
	asked := []runs.Run{runShell("readiness", runs.NeedsYou, func(r *runs.Run) { r.Reason = "asked 2 questions" })}
	c := boardCard{stage: stUpNext, runs: asked, sessions: []row{boardSess("readiness", claude.StatusIdle, func(r *row) { r.summary = "Needs you: answers" })}}
	if tn, next, _ := cardTurn(c, nil); tn != turnColleague || next != "waiting for answers" {
		t.Errorf("asked: %d %q", tn, next)
	}
	if tn, _, _ := cardTurn(c, []inboxItem{{kind: kMention, who: "Eve"}}); tn != turnNeedsYou {
		t.Errorf("a reply came: %d", tn)
	}
	c.stage = stPlan
	if tn, _, _ := cardTurn(c, nil); tn == turnColleague {
		t.Error("past Up next the questions don't hold the card")
	}
}

func TestStepStripShell(t *testing.T) {
	prev := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	lipgloss.SetColorProfile(0) // termenv.TrueColor
	m := newModelShell(t)
	const (
		greenFG  = "38;2;152;195;121"
		yellowFG = "38;2;229;192;123"
		redFG    = "38;2;224;108;117"
		blueFG   = "38;2;97;175;239"
		ruleFG   = "38;2;62;68;81"
		nextFG   = "1;38;2;198;120;221"
		passedFG = "38;2;89;115;72"
	)
	c := boardCard{stage: stUpNext, issue: atlassian.Issue{Key: "ABC-9"}, runs: []runs.Run{
		runShell("readiness", runs.OK, nil), runShell("implement", runs.Failed, nil), runShell("qa", runs.NeedsYou, nil),
	}}
	segs := strings.Split(m.stepStrip(c), " ")
	for i, want := range []string{greenFG, nextFG, redFG, ruleFG, ruleFG, ruleFG, yellowFG} {
		if !strings.Contains(segs[i], want) {
			t.Errorf("segment %d %q, want %s", i, segs[i], want)
		}
	}
	if s := plainShell(m.stepStrip(c)); s != "━ ━ ━ ─ ─ ─ ━  plan" {
		t.Errorf("strip %q", s)
	}

	// A running step animates and names itself.
	c.runs = append([]runs.Run{runShell("plan", runs.Running, nil)}, c.runs...)
	first := m.stepStrip(c)
	if !strings.Contains(strings.Split(first, " ")[1], blueFG) || !strings.HasSuffix(plainShell(first), "  plan") {
		t.Errorf("running %q", plainShell(first))
	}
	m.frame += 2
	if plainShell(m.stepStrip(c)) == plainShell(first) {
		t.Error("the running segment is still")
	}

	review := mrShell("payments", 11, "x", "alice", "feat/x", time.Hour)
	rc := boardCard{mr: &review, rstage: rsRequested, runs: []runs.Run{runShell("review", runs.OK, func(r *runs.Run) { r.SHA = "old" })}}
	if s := plainShell(m.stepStrip(rc)); s != "━ ─  review" {
		t.Errorf("a stale review is next again: %q", s)
	}
	for rs, want := range map[rstage]string{rsFindings: yellowFG, rsWaiting: greenFG, rsDone: greenFG, rsReviewing: ruleFG} {
		rc.rstage = rs
		if seg := strings.Split(m.stepStrip(rc), " ")[1]; !strings.Contains(seg, want) {
			t.Errorf("findings in %s: %q", rstageNames[rs], seg)
		}
	}
}

func TestRunLineShell(t *testing.T) {
	m := newModelShell(t)
	plan := runShell("plan", runs.Running, func(r *runs.Run) { r.Session = "aaaaaaaa-wait" })
	ready := runShell("readiness", runs.OK, func(r *runs.Run) { r.Reason = "spec is clear" })
	c := boardCard{stage: stUpNext, runs: []runs.Run{plan, ready}}
	if s := plainShell(m.runLine(c)); s != "now plan running Opus 5.5 · 1h   before readiness ok · spec is clear · 10m   next s jumps to it" {
		t.Errorf("running %q", s)
	}
	c.runs[0].Session = "gone"
	if s := plainShell(m.runLine(c)); !strings.HasPrefix(s, "now plan running · 1h") {
		t.Errorf("no session row %q", s)
	}
	c.runs = []runs.Run{runShell("readiness", runs.Failed, nil)}
	if s := plainShell(m.runLine(c)); s != "before readiness failed · 10m   next readiness · s starts it" {
		t.Errorf("failed %q", s)
	}
	c = boardCard{stage: stMerge, mrs: []gitlab.MR{boardMR(6, "opened", nil)}}
	if s := plainShell(m.runLine(c)); s != "next merge · s twice merges it" {
		t.Errorf("merge %q", s)
	}
	if s := plainShell(m.runLine(boardCard{stage: stDone})); s != "next —" {
		t.Errorf("done %q", s)
	}
	for card, want := range map[*boardCard]string{
		{stage: stUpNext}:                       "run readiness",
		{stage: stDone}:                         "nothing to run",
		{stage: stPlan, runs: []runs.Run{plan}}: "jump to plan",
	} {
		if got := runHint(*card); got != want {
			t.Errorf("hint %q, want %q", got, want)
		}
	}
}

func TestBoardShowsRunsShell(t *testing.T) {
	m := boardModelShell(t)
	db := withRunsShell(t, m)
	_ = db.Start(runs.Run{ID: "r1", Kind: "ticket", Subject: "ABC-4", Step: "readiness", Session: "gone", Started: time.Now().Add(-time.Hour), Finished: time.Now(), Outcome: runs.NeedsYou, Reason: "asked 2 questions"})
	m.reindex()
	s := screenSizedShell(t, m, "runs")
	containsAllShell(t, "card", s, "ABC-4  ⧗ waiting", "waiting for answers", "━ ─ ─ ─ ─ ─ ─  ⧗ waitin…", "s jump to readiness", "S in the background")
	containsAllShell(t, "detail", plainShell(m.detailBoard()), "now readiness needs-you · 1h   next s jumps to it")
	if len(m.runCache) == 0 {
		t.Error("the runs aren't cached")
	}
	m.runCache["sentinel"] = nil
	m.Update(sessionsMsg(sessionsShell()))
	if _, ok := m.runCache["sentinel"]; ok {
		t.Error("a refresh doesn't reread the runs")
	}
	if m.animates() {
		t.Error("nothing running, nothing moves")
	}
	_ = db.Start(runs.Run{ID: "r2", Kind: "ticket", Subject: "ABC-4", Step: "plan", Session: "s", Started: time.Now(), Outcome: runs.Running})
	m.runCache = nil
	m.reindex()
	if !m.animates() {
		t.Error("a running step animates")
	}
	// Card lines are four now; a column still fits the screen.
	if got := len(m.cardLines(m.vis.board[0][0], 30, false)); got != cardHeight || cardHeight != 4 {
		t.Errorf("%d card lines", got)
	}
	m.runCache = map[string][]runs.Run{"X": nil}
	if m.runsFor("X") != nil || len(m.runCache) != 1 {
		t.Error("cached")
	}
	m.db = nil
	if m.runsFor("ABC-4") != nil {
		t.Error("runs without a log")
	}
}

func TestRunKeyStartsStepsShell(t *testing.T) {
	m := boardModelShell(t)
	db := withRunsShell(t, m)
	m.repos = []repos.Repo{{Dir: "/code/payments", Project: "g/payments"}}
	withConfig(t, func(c *config.Config) { c.ReadinessPrompt = "R {key} {brief}" })
	m.refilter()
	if c, _ := m.currentCard(); c.issue.Key != "ABC-4" {
		t.Fatalf("on %+v", c)
	}
	containsAllShell(t, "hint", screenShell(m), "s run readiness")

	// X: readiness in the background, recorded as a run once the session starts.
	// Readiness needs no particular repo: it starts in the work dir without asking.
	cmd := pressShell(m, "S")
	if m.chooser != nil || m.step != nil || cmd == nil {
		t.Fatalf("x asks for a repo: %+v", m.chooser)
	}
	msg := cmd().(startMsg)
	if !msg.background || msg.err != nil || msg.sp.Kind != "readiness" || msg.sp.Dir != steps.Anywhere(getConfig()) || promptOf(msg.sp) != "R ABC-4 Jira (use the Atlassian MCP)" {
		t.Errorf("start %+v", msg)
	}
	got := db.For("ABC-4")
	if len(got) != 1 || got[0].Step != "readiness" || got[0].Kind != "ticket" || got[0].Session != msg.sp.Args[1] || got[0].Outcome != runs.Running {
		t.Fatalf("run row %+v", got)
	}
	if !slices.Contains(msg.sp.Env, "CLAUDIA_RUN="+got[0].ID) || !strings.Contains(msg.sp.Args[5], "This is claudia run "+got[0].ID+", step readiness for ABC-4.") {
		t.Errorf("the session doesn't know it's a run: %q %q", msg.sp.Env, msg.sp.Args[5])
	}
	started := fakeStart(t, "payments:1", nil)
	m.Update(msg)
	if len(*started) != 1 || m.runCache != nil {
		t.Errorf("started %d, cache %v", len(*started), m.runCache)
	}

	// A plain c later isn't recorded as a run.
	m.claudeForTicket("ABC-4", "")
	m.chooser.setQuery("General")
	pressShell(m, "enter")
	if plain := pressShell(m, "enter")().(startMsg); slices.ContainsFunc(plain.sp.Env, func(e string) bool { return strings.HasPrefix(e, "CLAUDIA_RUN") }) || len(db.For("ABC-4")) != 1 {
		t.Errorf("a plain session became a run: %q", plain.sp.Env)
	}

	// The run is under way: x jumps to its session, resuming it when closed.
	m.sessions = append(m.sessions, row{Session: claude.Session{ID: got[0].Session, Transcript: &claude.Transcript{Cwd: "/code/payments", AITitle: "check"}}})
	m.reindex()
	pressShell(m, "s")
	if len(*started) != 2 || (*started)[1].Args[1] != got[0].Session || !slices.Contains((*started)[1].Env, "CLAUDIA_RUN="+got[0].ID) {
		t.Fatalf("resumed as the run: %+v", (*started)[1:])
	}
	m.sessions[len(m.sessions)-1].Live = &claude.Live{Status: claude.StatusIdle}
	m.sessions[len(m.sessions)-1].pane = &tmux.Pane{ID: "%9", Session: "payments", Window: "1", Index: "0"}
	m.reindex()
	pressShell(m, "s")
	if m.Result.Attach != "payments:1.0" {
		t.Errorf("x on a live run jumps: %q", m.Result.Attach)
	}
	m.sessions = m.sessions[:len(m.sessions)-1]
	m.reindex()
	pressShell(m, "s")
	if !strings.Contains(m.flash, "readiness is running, but its session isn't around") {
		t.Errorf("flash %q", m.flash)
	}

	// Asked questions with the session gone: x runs readiness again.
	_ = db.Settle(got[0].Session, runs.NeedsYou, "asked", time.Now())
	m.runCache = nil
	m.reindex()
	m.chooser = nil
	if cmd := pressShell(m, "s"); cmd == nil {
		t.Error("x reruns a step whose session is gone")
	} else if again := cmd().(startMsg); again.sp.Kind != "readiness" {
		t.Errorf("rerun %+v", again.sp)
	}
}

func TestRunKeyOnOtherStagesShell(t *testing.T) {
	m := boardModelShell(t)
	db := withRunsShell(t, m)
	started := fakeStart(t, "billing:1", nil)
	m.repos = []repos.Repo{{Dir: "/code/billing", Project: "g/billing", Branch: "feat/ABC-2/invoices"}}
	m.refilter()
	// ABC-2 is in review with a thread on my MR: x addresses it, the run keeps the head.
	m.boardCol = slices.IndexFunc(m.vis.boardCols, func(c boardCol) bool { return c.st == stReview })
	m.cur[vBoard].pos = slices.IndexFunc(m.vis.board[m.boardCol], func(c boardCard) bool { return c.issue.Key == "ABC-2" })
	pressShell(m, "s")
	if len(*started) != 1 || (*started)[0].Kind != "fix" {
		t.Fatalf("feedback started %+v", *started)
	}
	if r := db.For("ABC-2"); len(r) != 1 || r[0].Step != "feedback" || r[0].SHA != "sha-21" {
		t.Errorf("feedback run %+v", r)
	}

	// Nothing to run on a card that waits on others.
	m.cur[vBoard].pos = slices.IndexFunc(m.vis.board[m.boardCol], func(c boardCard) bool { return c.issue.Key == "ABC-1" })
	pressShell(m, "s")
	if !strings.Contains(m.flash, "nothing for s to run on ABC-1") {
		t.Errorf("flash %q", m.flash)
	}

	// Without a run log x says why.
	m.db, m.dbErr = nil, errors.New("disk full")
	pressShell(m, "s")
	if m.flash != "the run log isn't open: disk full" {
		t.Errorf("flash %q", m.flash)
	}
	m.db = db

	// A closed log still starts the session and says so.
	db.Close()
	m.step = &runs.Run{Step: "review"}
	m.reviewWithClaude(m.gl.Review[0])
	m.chooser.setQuery("")
	pressShell(m, "enter")
	if len(*started) != 2 || !strings.HasPrefix(m.flash, "run log: ") {
		t.Errorf("started %d, flash %q", len(*started), m.flash)
	}
	m.Update(startMsg{sp: launch.Spec{Name: "x"}, background: true, err: errors.New("locked")})
	if m.flash != "run log: locked" {
		t.Errorf("flash %q", m.flash)
	}
	if sp, err := startRun(nil, launch.Spec{Name: "x"}, "id", &runs.Run{}); err == nil || sp.Env != nil {
		t.Errorf("no log: %v %+v", err, sp)
	}
}

func TestRunKeyOnReviewShell(t *testing.T) {
	m := reviewBoardShell(t)
	db := withRunsShell(t, m)
	started := fakeStart(t, "payments:1", nil)
	m.repos = []repos.Repo{{Dir: "/code/payments", Project: "g/payments"}}
	pressShell(m, "r", "down")
	containsAllShell(t, "hint", screenShell(m), "s run review")
	pressShell(m, "s")
	if len(*started) != 1 || (*started)[0].Kind != "review" {
		t.Fatalf("review started %+v", *started)
	}
	r := db.For(m.gl.Review[0].WebURL)
	if len(r) != 1 || r[0].Kind != "review" || r[0].SHA != "sha-11" || r[0].Session != (*started)[0].Args[1] {
		t.Errorf("review run %+v", r)
	}
	// Findings are mine to read: x has nothing to start.
	pressShell(m, "l", "l")
	pressShell(m, "s")
	if !strings.Contains(m.flash, "nothing for s to run on !32") {
		t.Errorf("flash %q", m.flash)
	}
}

func TestMergeStepShell(t *testing.T) {
	m := boardModelShell(t)
	db := withRunsShell(t, m)
	ready := boardMR(30, "opened", func(mr *gitlab.MR) { mr.Pipeline = &gitlab.Pipeline{Status: "success"}; mr.SHA = "head" })
	c := boardCard{stage: stMerge, issue: atlassian.Issue{Key: "ABC-9"}, mrs: []gitlab.MR{ready}}
	var merged []int
	fake(t, &mergeMR, func(mr gitlab.MR) error {
		merged = append(merged, mr.IID)
		return nil
	})
	if _, cmd := m.runNext(c); cmd != nil || !strings.Contains(m.flash, "merge !30 payments — press again") {
		t.Fatalf("first press asks: %q", m.flash)
	}
	_, cmd := m.runNext(c)
	msg := cmd().(mergedMsg)
	if len(merged) != 1 || msg.err != nil || msg.log != nil {
		t.Fatalf("merged %v %+v", merged, msg)
	}
	if r := db.For("ABC-9"); len(r) != 1 || r[0].Step != "merge" || r[0].Outcome != runs.OK || r[0].SHA != "head" || r[0].Finished.IsZero() {
		t.Errorf("merge run %+v", r)
	}
	m.Update(msg)
	if m.flash != "merged !30 payments" || !m.busy[srcGitlab] {
		t.Errorf("flash %q, GitLab asked again %v", m.flash, m.busy[srcGitlab])
	}

	fake(t, &mergeMR, func(gitlab.MR) error { return errors.New("405 not mergeable") })
	m.runNext(c)
	_, cmd = m.runNext(c)
	msg = cmd().(mergedMsg)
	// Both runs may start within the same millisecond.
	if r := db.For("ABC-9"); !slices.ContainsFunc(r, func(r runs.Run) bool { return r.Outcome == runs.Failed && r.Reason == "405 not mergeable" }) {
		t.Errorf("failed merge run %+v", r)
	}
	m.Update(msg)
	if m.flash != "merge !30 payments: 405 not mergeable" {
		t.Errorf("flash %q", m.flash)
	}
	m.merged(mergedMsg{ref: "!1", log: errors.New("locked")})
	if m.flash != "merged !1, but the run log: locked" {
		t.Errorf("flash %q", m.flash)
	}

	// The stage can say Merge before an MR is ready: nothing to merge then.
	c.mrs = []gitlab.MR{boardMR(31, "opened", nil)}
	m.runNext(c)
	if m.flash != "no MR of ABC-9 is ready to merge" {
		t.Errorf("flash %q", m.flash)
	}
}

func TestLoadSessionsSettlesRunsShell(t *testing.T) {
	m := newModelShell(t)
	db := withRunsShell(t, m)
	upd := time.Now().Add(-time.Minute)
	tr := &claude.Transcript{Updated: upd, Tail: []claude.Turn{{Role: "assistant", Text: "Done.\nCLAUDIA_RESULT: ok — MR !40 open"}}}
	fake(t, &listSessions, func(*claude.Store) []claude.Session {
		return []claude.Session{{ID: "s1", Transcript: tr, Live: &claude.Live{Status: claude.StatusIdle}}}
	})
	_ = db.Start(runs.Run{ID: "r1", Kind: "ticket", Subject: "ABC-1", Step: "mr", Session: "s1", Started: upd})
	if _, ok := m.loadSessions()().(sessionsMsg); !ok {
		t.Fatal("no sessions")
	}
	if r := db.For("ABC-1"); r[0].Outcome != runs.OK || r[0].Reason != "MR !40 open" {
		t.Errorf("settled %+v", r[0])
	}
}

func TestStepStripPassedShell(t *testing.T) {
	prev := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	lipgloss.SetColorProfile(0) // termenv.TrueColor
	m := newModelShell(t)
	const passedFG, greenFG, nextFG = "38;2;89;115;72", "38;2;152;195;121", "1;38;2;198;120;221"
	// A QA card got past every step before QA notes without claudia running them;
	// the one it did run stays bright green.
	c := boardCard{stage: stQA, issue: atlassian.Issue{Key: "ABC-9"}, runs: []runs.Run{runShell("implement", runs.OK, nil)}}
	segs := strings.Split(m.stepStrip(c), " ")
	for i, want := range []string{passedFG, passedFG, greenFG, passedFG, passedFG, passedFG, nextFG} {
		if !strings.Contains(segs[i], want) {
			t.Errorf("segment %d %q, want %s", i, segs[i], want)
		}
	}
	if s := plainShell(m.stepStrip(c)); s != "━ ━ ━ ━ ━ ━ ━  QA notes" {
		t.Errorf("strip %q", s)
	}
	review := mrShell("payments", 11, "x", "alice", "feat/x", time.Hour)
	rc := boardCard{mr: &review, rstage: rsWaiting}
	if seg := strings.Split(m.stepStrip(rc), " ")[0]; !strings.Contains(seg, passedFG) {
		t.Errorf("a review past Requested: %q", seg)
	}
}

func TestStepStripTurnShell(t *testing.T) {
	prev := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	lipgloss.SetColorProfile(0) // termenv.TrueColor
	m := newModelShell(t)
	const yellowFG, blueFG, dimFG, nextFG = "38;2;229;192;123", "38;2;97;175;239", "38;2;92;99;112", "1;38;2;198;120;221"
	for tn, want := range map[turn]string{turnYou: nextFG, turnNeedsYou: yellowFG, turnAgent: blueFG, turnColleague: dimFG} {
		c := boardCard{stage: stPlan, issue: atlassian.Issue{Key: "ABC-9"}, turn: tn}
		strip := m.stepStrip(c)
		if seg := strings.Split(strip, " ")[2]; !strings.Contains(seg, want) {
			t.Errorf("turn %d: segment %q, want %s", tn, seg, want)
		}
		if tn == turnColleague && !strings.HasSuffix(plainShell(strip), "  ⧗ implement") {
			t.Errorf("waiting on someone: %q", plainShell(strip))
		}
	}
	// In review with no step for x, the card still marks where it is: here,
	// waiting on reviewers at feedback.
	c := boardCard{stage: stReview, issue: atlassian.Issue{Key: "ABC-9"}, turn: turnColleague}
	if s := plainShell(m.stepStrip(c)); s != "━ ━ ━ ━ ━ ─ ─  ⧗ feedback" {
		t.Errorf("waiting in review: %q", s)
	}
	if s := plainShell(m.stepStrip(boardCard{stage: stDone, issue: atlassian.Issue{Key: "ABC-9"}})); strings.Contains(s, "  ") {
		t.Errorf("a done card names no step: %q", s)
	}
}

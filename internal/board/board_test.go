package board

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/links"
	"github.com/MartyJRE/claudia/internal/runs"
)

// Tests must not read the real ~/.config/claudia.
func TestMain(m *testing.M) {
	dir, _ := os.MkdirTemp("", "claudia-test")
	os.Setenv("XDG_CONFIG_HOME", dir)
	os.Setenv("XDG_STATE_HOME", dir)
	os.Setenv("CLAUDIA_JIRA_KEYS", "ABC")
	os.Setenv("CLAUDIA_GITLAB_HOST", "gitlab.example.com")
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func mr(iid int, state string, edit func(*gitlab.MR)) gitlab.MR {
	m := gitlab.MR{IID: iid, State: state, Title: "feat(ABC-9): x", SourceBranch: "feat/ABC-9/x", SHA: fmt.Sprintf("sha%d", iid),
		WebURL: fmt.Sprintf("https://gitlab.example.com/g/payments/-/merge_requests/%d", iid), UpdatedAt: time.Now().Add(-time.Hour)}
	m.Author.Username = "alice"
	if edit != nil {
		edit(&m)
	}
	return m
}

func sess(kind string, status claude.Status, edit func(*Session)) Session {
	at := time.Now()
	s := Session{Session: claude.Session{ID: "s-" + kind, Transcript: &claude.Transcript{AITitle: "t-" + kind, Branch: "devel", Updated: at}}, Kind: kind, SummaryFor: at}
	if status != "" {
		s.Live = &claude.Live{Status: status}
	}
	if edit != nil {
		edit(&s)
	}
	return s
}

func run(step, outcome string, edit func(*runs.Run)) runs.Run {
	r := runs.Run{ID: "r-" + step, Step: step, Session: "s-" + step, Outcome: outcome}
	if edit != nil {
		edit(&r)
	}
	return r
}

func TestSession(t *testing.T) {
	at := time.Now()
	tr := &claude.Transcript{Updated: at}
	live := func(st claude.Status) *claude.Live { return &claude.Live{Status: st} }
	for _, c := range []struct {
		name           string
		s              Session
		needsYou, done bool
	}{
		{"closed", Session{Session: claude.Session{Transcript: tr}, Summary: "Needs you: x", SummaryFor: at}, false, false},
		{"waiting on a prompt", Session{Session: claude.Session{Live: live(claude.StatusWaiting)}}, true, false},
		{"busy", Session{Session: claude.Session{Transcript: tr, Live: live(claude.StatusBusy)}, Summary: "Needs you: x", SummaryFor: at}, false, false},
		{"idle, asks", Session{Session: claude.Session{Transcript: tr, Live: live(claude.StatusIdle)}, Summary: "Needs you: x", SummaryFor: at}, true, false},
		{"idle, an older summary", Session{Session: claude.Session{Transcript: tr, Live: live(claude.StatusIdle)}, Summary: "Needs you: x", SummaryFor: at.Add(-time.Second)}, false, false},
		{"idle, done", Session{Session: claude.Session{Transcript: tr, Live: live(claude.StatusIdle)}, Summary: "Done: x", SummaryFor: at}, false, true},
	} {
		if c.s.NeedsYou() != c.needsYou || c.s.Done() != c.done {
			t.Errorf("%s: needs you %v done %v", c.name, c.s.NeedsYou(), c.s.Done())
		}
	}
}

func TestActivity(t *testing.T) {
	if v, _ := Activity(sess("", claude.StatusShell, nil)); v != "running" {
		t.Errorf("shell %q", v)
	}
	if v, d := Activity(sess("review", claude.StatusBusy, nil)); v != "reviewing" || d != "" {
		t.Errorf("review %q", v)
	}
	s := sess("", claude.StatusBusy, func(s *Session) { s.Transcript.Activity = claude.Activity{Verb: "editing", Detail: "a.go"} })
	if v, d := Activity(s); v != "editing" || d != "a.go" {
		t.Errorf("editing %q %q", v, d)
	}
	if v, _ := Activity(Session{Session: claude.Session{Live: &claude.Live{Status: claude.StatusBusy}}}); v != "thinking" {
		t.Errorf("no transcript %q", v)
	}
}

func TestCardStage(t *testing.T) {
	passed := func(m *gitlab.MR) { m.Pipeline = &gitlab.Pipeline{Status: "success"} }
	issue := atlassian.Issue{Key: "ABC-9", Status: "To Do", Category: "new"}
	statuses := map[string]string{"Ready for QA": "qa", "Weird": "nowhere"}
	for _, c := range []struct {
		name string
		card Card
		want Stage
		why  string
	}{
		{"nothing yet", Card{}, UpNext, "no plan"},
		{"set in the config", Card{Issue: atlassian.Issue{Key: "ABC-9", Status: "Ready for QA"}}, QA, "board.statuses"},
		{"an unknown column", Card{Issue: atlassian.Issue{Key: "ABC-9", Status: "Weird"}}, UpNext, "no plan"},
		{"done in Jira", Card{Issue: atlassian.Issue{Key: "ABC-9", Category: "done"}}, Done, "done in Jira"},
		{"merged", Card{MRs: []gitlab.MR{mr(1, "merged", nil), mr(3, "merged", nil)}}, QA, "!1 is merged"},
		{"ready to merge", Card{MRs: []gitlab.MR{mr(1, "opened", nil), mr(2, "opened", passed)}}, Merge, "!2 is approved"},
		{"draft", Card{MRs: []gitlab.MR{mr(4, "opened", func(m *gitlab.MR) { m.Draft = true })}}, Building, "!4 is a draft"},
		{"implementation", Card{Sessions: []Session{sess("analysis", "", nil), sess("implementation", "", nil)}}, Building, "implementation session"},
		{"working on the branch", Card{Sessions: []Session{sess("", claude.StatusBusy, func(s *Session) { s.Transcript.Branch = "feat/ABC-9/x" })}}, Building, "is working on"},
		{"analysis", Card{Sessions: []Session{sess("", "", nil), sess("analysis", "", nil)}}, Plan, "analysis session"},
		{"a plan", Card{Sessions: []Session{sess("", "", func(s *Session) { s.Transcript.PlanPath = "/p.md" })}}, Plan, "has a plan"},
		{"status says testing", Card{Issue: atlassian.Issue{Key: "ABC-9", Status: "Test - Preprod"}}, QA, "Jira status"},
		{"status says review", Card{Issue: atlassian.Issue{Key: "ABC-9", Status: "Code review"}}, Review, "Jira status"},
		{"status says progress", Card{Issue: atlassian.Issue{Key: "ABC-9", Status: "Dev in progress"}}, Building, "Jira status"},
		{"status says analysis", Card{Issue: atlassian.Issue{Key: "ABC-9", Status: "Analysis"}}, Plan, "Jira status"},
		{"ready for analysis", Card{Issue: atlassian.Issue{Key: "ABC-9", Status: "Ready for analysis"}}, UpNext, "no plan"},
		{"unknown status", Card{Issue: atlassian.Issue{Key: "ABC-9", Status: "Proposal"}}, UpNext, "no plan"},
	} {
		if c.card.Issue.Key == "" {
			c.card.Issue = issue
		}
		got, why := CardStage(c.card, statuses)
		if got != c.want || !strings.Contains(why, c.why) {
			t.Errorf("%s: %s (%q), want %s", c.name, StageNames[got], why, StageNames[c.want])
		}
	}
	if !OnHold("Dev On Hold") || OnHold("To Do") {
		t.Error("on hold")
	}
}

func TestNextStep(t *testing.T) {
	ready := func(m *gitlab.MR) { m.Pipeline = &gitlab.Pipeline{Status: "success"} }
	threads := mr(7, "opened", func(m *gitlab.MR) { m.Threads = []gitlab.Thread{{}} })
	review := mr(11, "opened", nil)
	sha := func(s string) func(*runs.Run) { return func(r *runs.Run) { r.SHA = s } }
	for _, c := range []struct {
		name, next, hold string
		card             Card
	}{
		{"up next", "readiness", "", Card{Stage: UpNext}},
		{"readiness ok", "plan", "", Card{Stage: UpNext, Runs: []runs.Run{run("readiness", runs.OK, nil)}}},
		{"readiness asked", "readiness", "readiness", Card{Stage: UpNext, Runs: []runs.Run{run("readiness", runs.NeedsYou, nil)}}},
		{"in Plan by status alone", "plan", "", Card{Stage: Plan}},
		{"planned", "implement", "", Card{Stage: Plan, Runs: []runs.Run{run("plan", runs.OK, nil)}}},
		{"implement started", "implement", "", Card{Stage: Plan, Runs: []runs.Run{run("implement", runs.Failed, nil)}}},
		{"planned in plan mode", "implement", "", Card{Stage: Plan, Sessions: []Session{{Session: claude.Session{Transcript: &claude.Transcript{PlanPath: "/p.md"}}}}}},
		{"running holds", "plan", "plan", Card{Stage: Plan, Runs: []runs.Run{run("plan", runs.Running, nil)}}},
		{"building", "mr", "", Card{Stage: Building}},
		{"building with a draft", "", "", Card{Stage: Building, MRs: []gitlab.MR{mr(4, "opened", nil)}}},
		{"threads", "feedback", "", Card{Stage: Review, MRs: []gitlab.MR{threads}}},
		{"feedback on this head", "", "", Card{Stage: Review, MRs: []gitlab.MR{threads}, Runs: []runs.Run{run("feedback", runs.OK, sha(threads.SHA))}}},
		{"a push since", "feedback", "", Card{Stage: Review, MRs: []gitlab.MR{threads}, Runs: []runs.Run{run("feedback", runs.OK, sha("old"))}}},
		{"no threads", "", "", Card{Stage: Review}},
		{"merge", "merge", "", Card{Stage: Merge, MRs: []gitlab.MR{mr(6, "opened", ready)}}},
		{"qa", "qa", "", Card{Stage: QA}},
		{"qa failed", "qa", "", Card{Stage: QA, Runs: []runs.Run{run("qa", runs.Failed, nil)}}},
		{"done", "", "", Card{Stage: Done}},
		{"requested review", "review", "", Card{MR: &review, RStage: Requested}},
		{"reviewed this head", "", "", Card{MR: &review, RStage: Requested, Runs: []runs.Run{run("review", runs.OK, sha(review.SHA))}}},
		{"pushed since the review", "review", "", Card{MR: &review, RStage: Requested, Runs: []runs.Run{run("review", runs.OK, sha("old"))}}},
		{"findings", "", "", Card{MR: &review, RStage: Findings}},
		{"a todo", "ticket", "", Card{Todo: &runs.Todo{ID: 3}}},
		{"a ticketed todo", "", "", Card{Todo: &runs.Todo{ID: 3, Ticket: "OPS-1"}}},
		{"a todo ticked off", "", "", Card{Todo: &runs.Todo{ID: 3, Done: time.Now()}}},
	} {
		next, hold := NextStep(c.card)
		if next != c.next || (hold == nil) != (c.hold == "") || hold != nil && hold.Step != c.hold {
			t.Errorf("%s: %q %+v", c.name, next, hold)
		}
	}
	asked := func(outcome, reason string) Card {
		return Card{Stage: UpNext, Runs: []runs.Run{run("readiness", outcome, func(r *runs.Run) { r.Reason = reason })}}
	}
	if !WaitingForAnswers(asked(runs.Waiting, "")) || !WaitingForAnswers(asked(runs.NeedsYou, "asked 4 questions")) ||
		WaitingForAnswers(asked(runs.NeedsYou, "which table?")) || WaitingForAnswers(Card{Stage: Plan}) {
		t.Error("waiting for answers")
	}
	if next, hold := NextStep(asked(runs.Waiting, "asked 2 questions")); next != "readiness" || hold == nil {
		t.Errorf("waiting holds: %q %+v", next, hold)
	}
	at := time.Now()
	s := Session{Session: claude.Session{ID: "a", Transcript: &claude.Transcript{Updated: at}, Live: &claude.Live{Status: claude.StatusIdle}}, Summary: "Needs you: x"}
	s.WaitOnRun(map[string]runs.Run{"a": {Outcome: runs.Waiting, Reason: "asked Bob"}})
	if s.NeedsYou() || !s.Summarized(WaitingPrefix) || s.Summary != "Waiting: asked Bob" {
		t.Errorf("waiting session %+v", s)
	}
	if StepLabel("mr") != "open MR" || StepLabel("findings") != "findings" || StepLabel("x") != "x" {
		t.Error("labels")
	}
	if (Card{Stage: Plan}).Passed("plan") || !(Card{Stage: Plan, Runs: []runs.Run{run("plan", runs.OK, nil)}}).Passed("plan") {
		t.Error("plan passed only with a plan")
	}
	blocked := Card{Issue: atlassian.Issue{BlockedBy: []string{"ABC-1"}}}
	if blocked.WaitsOn("plan") != nil || len(blocked.WaitsOn("implement")) != 1 || (Card{MR: &review}).WaitsOn("review") != nil {
		t.Error("waits on")
	}
	if !(Card{Stage: Review}).Passed("mr") || (Card{Stage: Review}).Passed("feedback") {
		t.Error("ticket passed")
	}
	if !(Card{MR: &review, RStage: Findings}).Passed("review") || (Card{MR: &review, RStage: Reviewing}).Passed("review") {
		t.Error("review passed")
	}
	sprint := func(id int, state string) atlassian.Issue {
		return atlassian.Issue{Sprint: &atlassian.Sprint{ID: id, State: state}}
	}
	if got := ActiveSprint([]atlassian.Issue{sprint(9, "future"), {}, sprint(4, "active"), sprint(3, "active"), sprint(4, "active")}); got != 4 {
		t.Errorf("active sprint %d", got)
	}
	if ActiveSprint([]atlassian.Issue{sprint(9, "future")}) != 0 || ActiveSprint([]atlassian.Issue{sprint(5, "active"), sprint(3, "active")}) != 3 {
		t.Error("no active sprint, or a tie")
	}
	todo := Card{Todo: &runs.Todo{ID: 3}}
	if todo.ID() != "todo:3" || !todo.IsTodo() || todo.Passed("ticket") || !(Card{Todo: &runs.Todo{Ticket: "OPS-1"}}).Passed("ticket") ||
		todo.WaitsOn("ticket") != nil || StepLabel("ticket") != "ticket" {
		t.Error("todo card")
	}
	if (Card{MR: &review}).ID() != review.WebURL || (Card{Issue: atlassian.Issue{Key: "ABC-1"}}).ID() != "ABC-1" {
		t.Error("ids")
	}
}

func TestReviewStage(t *testing.T) {
	m := mr(11, "opened", nil)
	working := sess("review", claude.StatusBusy, nil)
	done := sess("review", claude.StatusIdle, func(s *Session) { s.Summary = "Done: two nits\nmore" })
	approved := m
	approved.ApprovedBy = []string{"me"}
	reviewed := m
	reviewed.MyReview = "reviewed"
	moved := m
	moved.UpdatedAt = time.Now().Add(time.Hour)
	for _, c := range []struct {
		name     string
		mr       gitlab.MR
		sessions []Session
		runs     []runs.Run
		want     RStage
		next     string
	}{
		{"approved", approved, nil, nil, Reviewed, ""},
		{"reviewing", m, []Session{sess("other", "", nil), working}, nil, Reviewing, ""},
		{"waiting on the author", reviewed, nil, nil, Waiting, "waits on alice"},
		{"requested", m, nil, nil, Requested, "review it"},
		{"findings", m, []Session{done}, nil, Findings, "two nits"},
		{"the latest review counts", m, []Session{sess("review", "", func(s *Session) { s.Transcript.Updated = time.Now().Add(-48 * time.Hour) }), done}, nil, Findings, "two nits"},
		{"the MR moved on", moved, []Session{done}, nil, Requested, "review it again"},
		{"the run read this head", moved, []Session{done}, []runs.Run{{Session: done.ID, SHA: m.SHA}}, Findings, "two nits"},
		{"the run read an older head", m, []Session{done}, []runs.Run{{Session: done.ID, SHA: "old"}}, Requested, "review it again"},
	} {
		st, _, next, _, why := ReviewStage(c.mr, c.sessions, c.runs, "me")
		if st != c.want || next != c.next {
			t.Errorf("%s: %s %q (%s)", c.name, RStageNames[st], next, why)
		}
	}
	if FindingHint(Session{}) != "read the findings" {
		t.Error("empty hint")
	}
}

func TestCardsAndIndex(t *testing.T) {
	own := mr(5, "opened", func(m *gitlab.MR) { m.SourceBranch = "feat/ABC-1/x"; m.Title = "x" })
	other := mr(6, "opened", func(m *gitlab.MR) { m.SourceBranch = "feat/ABC-1/x"; m.ProjectID = 2; m.Title = "y" })
	asked := mr(11, "opened", func(m *gitlab.MR) { m.SourceBranch = "feat/ABC-3/y"; m.Title = "feat: y" })
	draft := mr(12, "opened", func(m *gitlab.MR) { m.Draft = true })
	gl := gitlab.Snapshot{Mine: []gitlab.MR{own, other, own}, Review: []gitlab.MR{asked, draft}}
	gl.Me.Username = "me"
	js := atlassian.JiraSnapshot{Sprint: []atlassian.Issue{{Key: "ABC-1"}, {Key: "ABC-0", Level: 1}}}
	l := &links.Links{SessionMRs: map[string][]string{"rev": {asked.WebURL, "https://gitlab.example.com/g/gone/-/merge_requests/1"}}, SessionTickets: map[string]string{"manual": "ABC-1", "none": "-"}}
	l.SessionMRs["mine"] = []string{own.WebURL}
	sessions := []Session{
		{Session: claude.Session{ID: "rev", Transcript: &claude.Transcript{Updated: time.Now(), Branch: "feat/ABC-1/x", PRs: []claude.PRLink{{URL: asked.WebURL}}}}, Kind: "review"},
		{Session: claude.Session{ID: "manual", Transcript: &claude.Transcript{Branch: "devel"}}},
		{Session: claude.Session{ID: "mine", Transcript: &claude.Transcript{Branch: "feat/ABC-1/x"}}},
		{Session: claude.Session{ID: "none", Transcript: &claude.Transcript{Branch: "feat/ABC-1/x"}}},
	}
	cs := make([]claude.Session, len(sessions))
	for i, s := range sessions {
		cs[i] = s.Session
	}
	ix := BuildIndex(cs, gl, js, l)
	if ix.SessionTicket["rev"] != "ABC-3" || ix.SessionTicket["manual"] != "ABC-1" || ix.SessionTicket["none"] != "" || ix.SessionTicket["mine"] != "ABC-1" || len(ix.TicketMRs["ABC-1"]) != 2 {
		t.Errorf("index %+v", ix)
	}
	runsFor := func(string) []runs.Run { return nil }
	cards := Cards(js.Sprint, ix, sessions, runsFor, nil)
	if len(cards) != 1 || cards[0].Issue.Key != "ABC-1" || len(cards[0].Sessions) != 2 || cards[0].Stage != Review {
		t.Errorf("cards %+v", cards)
	}
	reviews := ReviewCards(gl, ix, sessions, runsFor)
	if len(reviews) != 1 || reviews[0].MR.IID != 11 || reviews[0].RStage != Findings || reviews[0].Stage != Review {
		t.Errorf("review cards %+v", reviews)
	}
	if rel := Related(own, gl); len(rel) != 2 || rel[1].IID != 6 {
		t.Errorf("related %+v", rel)
	}
	if MRTicket(gitlab.MR{Title: "ABC-4: x"}) != "ABC-4" {
		t.Error("ticket from the title")
	}
}

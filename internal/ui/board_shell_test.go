package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/gitlab"
)

func boardMR(iid int, state string, edit func(*gitlab.MR)) gitlab.MR {
	mr := mrShell("payments", iid, "feat(ABC-9): x", "me", "feat/ABC-9/x", time.Hour)
	mr.State = state
	if edit != nil {
		edit(&mr)
	}
	return mr
}

func boardSess(kind string, status claude.Status, edit func(*row)) row {
	at := time.Now()
	r := row{Session: claude.Session{ID: "s-" + kind, Transcript: &claude.Transcript{AITitle: "t-" + kind, Branch: "devel", Updated: at}}, kind: kind, summaryFor: at}
	if status != "" {
		r.Live = &claude.Live{Status: status}
	}
	if edit != nil {
		edit(&r)
	}
	return r
}

func TestCardStageShell(t *testing.T) {
	passed := func(mr *gitlab.MR) { mr.Pipeline = &gitlab.Pipeline{Status: "success"} }
	issue := atlassian.Issue{Key: "ABC-9", Status: "To Do", Category: "new"}
	for _, c := range []struct {
		name string
		card boardCard
		want stage
		why  string
	}{
		{"nothing yet", boardCard{}, stUpNext, "no plan"},
		{"done in Jira", boardCard{issue: atlassian.Issue{Key: "ABC-9", Category: "done"}, mrs: []gitlab.MR{boardMR(1, "opened", nil)}}, stDone, "done in Jira"},
		{"merged", boardCard{mrs: []gitlab.MR{boardMR(1, "merged", nil), boardMR(2, "closed", nil), boardMR(3, "merged", nil)}}, stQA, "!1 is merged"},
		{"merged, another still open", boardCard{mrs: []gitlab.MR{boardMR(1, "merged", nil), boardMR(2, "opened", nil)}}, stReview, "!2 is open"},
		{"ready to merge", boardCard{mrs: []gitlab.MR{boardMR(1, "opened", nil), boardMR(2, "opened", passed)}}, stMerge, "!2 is approved"},
		{"open threads keep it in review", boardCard{mrs: []gitlab.MR{boardMR(1, "opened", func(mr *gitlab.MR) {
			passed(mr)
			mr.Threads = []gitlab.Thread{{}}
		})}}, stReview, "!1 is open"},
		{"approvals left", boardCard{mrs: []gitlab.MR{boardMR(1, "opened", func(mr *gitlab.MR) { passed(mr); mr.ApprovalsLeft = 1 })}}, stReview, ""},
		{"draft", boardCard{mrs: []gitlab.MR{boardMR(4, "opened", func(mr *gitlab.MR) { mr.Draft = true })}}, stBuilding, "!4 is a draft"},
		{"implementation session", boardCard{sessions: []row{boardSess("analysis", "", nil), boardSess("implementation", "", nil)}}, stBuilding, "implementation session"},
		{"fix session", boardCard{sessions: []row{boardSess("fix", "", nil)}}, stBuilding, "fix session"},
		{"working on the branch", boardCard{sessions: []row{boardSess("", claude.StatusBusy, func(r *row) { r.Branch = "feat/ABC-9/x" })}}, stBuilding, "is working on feat/ABC-9/x"},
		{"working elsewhere", boardCard{sessions: []row{boardSess("", claude.StatusBusy, nil)}}, stUpNext, ""},
		{"analysis", boardCard{sessions: []row{boardSess("", "", nil), boardSess("analysis", "", nil)}}, stPlan, "analysis session"},
		{"a plan", boardCard{sessions: []row{boardSess("", "", func(r *row) { r.PlanPath = "/p.md" })}}, stPlan, "has a plan"},
		{"a plan text", boardCard{sessions: []row{boardSess("", "", func(r *row) { r.Plan = "do it" })}}, stPlan, "has a plan"},
		{"status says testing", boardCard{issue: atlassian.Issue{Key: "ABC-9", Status: "Test - Preprod", Category: "indeterminate"}}, stQA, `Jira status "Test - Preprod"`},
		{"status says review", boardCard{issue: atlassian.Issue{Key: "ABC-9", Status: "Code review", Category: "indeterminate"}}, stReview, "Jira status"},
		{"status says in progress", boardCard{issue: atlassian.Issue{Key: "ABC-9", Status: "Dev in progress", Category: "indeterminate"}}, stBuilding, "Jira status"},
		{"status says analysis", boardCard{issue: atlassian.Issue{Key: "ABC-9", Status: "Analysis in progress", Category: "indeterminate"}}, stPlan, "Jira status"},
		{"status not started", boardCard{issue: atlassian.Issue{Key: "ABC-9", Status: "Ready for analysis", Category: "indeterminate"}}, stUpNext, "no plan"},
		{"evidence beyond the status", boardCard{issue: atlassian.Issue{Key: "ABC-9", Status: "Dev in progress", Category: "indeterminate"}, mrs: []gitlab.MR{boardMR(1, "opened", nil)}}, stReview, "!1 is open"},
		{"unknown status", boardCard{issue: atlassian.Issue{Key: "ABC-9", Status: "Proposal", Category: "indeterminate"}}, stUpNext, "no plan"},
		{"no transcript", boardCard{sessions: []row{{Session: claude.Session{ID: "x", Live: &claude.Live{Status: claude.StatusBusy}}}}}, stUpNext, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.card.issue.Key == "" {
				c.card.issue = issue
			}
			got, why := cardStage(c.card)
			if got != c.want || !strings.Contains(why, c.why) {
				t.Errorf("%s (%q), want %s (%q)", stageNames[got], why, stageNames[c.want], c.why)
			}
		})
	}
}

func TestCardStageOverrideShell(t *testing.T) {
	withConfig(t, func(c *config.Config) {
		c.Board.Statuses = map[string]string{"Ready for QA": "qa", "Blocked": "up-next", "Weird": "nowhere"}
	})
	for status, want := range map[string]stage{"Ready for QA": stQA, "Blocked": stUpNext, "Weird": stReview} {
		c := boardCard{issue: atlassian.Issue{Key: "ABC-9", Status: status}, mrs: []gitlab.MR{boardMR(1, "opened", nil)}}
		if got, why := cardStage(c); got != want {
			t.Errorf("%s: %s (%s)", status, stageNames[got], why)
		}
	}
	if _, ok := stageNamed("Merge"); !ok {
		t.Error("a column by its name")
	}
}

func TestCardTurnShell(t *testing.T) {
	failed := func(mr *gitlab.MR) { mr.Pipeline = &gitlab.Pipeline{Status: "failed"} }
	waitingOn := func(r *row) { r.summary = "Waiting: Michal to approve" }
	threadMR := boardMR(7, "opened", func(mr *gitlab.MR) { mr.Threads = []gitlab.Thread{{}, {}} })
	for _, c := range []struct {
		name    string
		card    boardCard
		waiting []inboxItem
		want    turn
		next    string
	}{
		{"a session asks", boardCard{sessions: []row{boardSess("", claude.StatusIdle, nil), boardSess("x", claude.StatusWaiting, nil)}}, nil, turnNeedsYou, "answer t-x"},
		{"threads on my MR", boardCard{}, []inboxItem{{kind: kThreads, mr: threadMR}}, turnNeedsYou, "address 2 threads"},
		{"a review for me", boardCard{}, []inboxItem{{kind: kReview, mr: boardMR(8, "opened", nil)}}, turnNeedsYou, "review !8"},
		{"a mention", boardCard{}, []inboxItem{{kind: kMention, who: "Eve"}}, turnNeedsYou, "answer Eve"},
		{"Claude works", boardCard{sessions: []row{boardSess("", claude.StatusBusy, func(r *row) {
			r.Activity = claude.Activity{Verb: "editing", Detail: "a.go"}
		})}}, nil, turnAgent, "a.go"},
		{"waiting on someone", boardCard{sessions: []row{boardSess("", claude.StatusIdle, waitingOn)}}, nil, turnColleague, "Michal to approve"},
		{"a closed session waiting doesn't count", boardCard{sessions: []row{boardSess("", "", waitingOn)}}, nil, turnYou, "readiness"},
		{"reviewers", boardCard{stage: stReview, mrs: []gitlab.MR{boardMR(9, "opened", func(mr *gitlab.MR) { mr.ApprovalsLeft = 2 })}}, nil, turnColleague, "!9 waits on 2 approval(s)"},
		{"threads to address", boardCard{stage: stReview, mrs: []gitlab.MR{boardMR(1, "merged", nil), threadMR}}, nil, turnYou, "address 2 threads"},
		{"pipeline", boardCard{stage: stReview, mrs: []gitlab.MR{boardMR(9, "opened", func(mr *gitlab.MR) { failed(mr); mr.ApprovalsLeft = 1 })}}, nil, turnYou, "fix the pipeline of !9"},
		{"merge it", boardCard{stage: stMerge, mrs: []gitlab.MR{boardMR(9, "opened", nil)}}, nil, turnYou, "merge"},
		{"QA", boardCard{stage: stQA}, nil, turnYou, "QA notes"},
		{"on hold", boardCard{stage: stUpNext, issue: atlassian.Issue{Status: "Dev On Hold"}}, nil, turnColleague, "on hold: Dev On Hold"},
		{"waiting on a story", boardCard{stage: stQA, issue: atlassian.Issue{Status: "Waiting on another user story"}}, nil, turnColleague, "on hold: Waiting on another user story"},
		{"blocked", boardCard{issue: atlassian.Issue{Status: "Blocked"}}, nil, turnColleague, "on hold: Blocked"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, next, _ := cardTurn(c.card, c.waiting)
			if got != c.want || next != c.next {
				t.Errorf("turn %d %q, want %d %q", got, next, c.want, c.next)
			}
		})
	}
}

func boardModelShell(t *testing.T) *Model {
	t.Helper()
	m := newModelShell(t)
	m.jira.Sprint = append(m.jira.Sprint,
		atlassian.Issue{Key: "ABC-4", Summary: "Fresh idea", Status: "To Do", Category: "new", Parent: "ABC-100", ParentSummary: "Payments epic", Assignee: "Ann"},
		atlassian.Issue{Key: "ABC-100", Summary: "Payments epic", Level: 1, Category: "indeterminate"},
		atlassian.Issue{Key: "ABC-5", Summary: "Next sprint idea", Status: "To Do", Category: "new", Sprint: &atlassian.Sprint{Name: "Sprint 9", State: "future"}},
	)
	m.jira.Team = []atlassian.Issue{{Key: "ABC-7", Summary: "Team story", Status: "To Do", Category: "new"}}
	m.boardCol = 0
	m.reindex()
	m.switchView(vBoard)
	m.boardFocus = true
	return m
}

func TestBoardRendersShell(t *testing.T) {
	m := boardModelShell(t)
	m.ledger = claude.OpenLedger()
	m.ledger.Merge([]claude.SessionCost{
		{ID: "aaaaaaaa-wait", Ticket: "ABC-1", Models: map[string]claude.Tokens{"claude-opus-5": {Output: 1_000_000}, "mystery": {Output: 1}}},
		{ID: "gone-session", Ticket: "ABC-1", Models: map[string]claude.Tokens{"claude-haiku-4-5": {Output: 1_000_000}}},
	})
	m.costs = ticketCosts(m.ledger)
	m.refilter()
	out := m.View()
	s := plainShell(out)
	containsAllShell(t, "board", s, "2 Board 2", "my tickets by stage", "Up next 1", "Plan 0", "Review 2", "QA 0",
		"ABC-1  ◆ needs you", "Payments export", "$30.00+? · ✻1 · !11", "ABC-4", "→ readiness", "+ 1 in future sprints  f", "h/l columns")
	lacksShell(t, "board", s, "Done ", "ABC-3", "‹", "›", "ABC-4  ● you", "ABC-5")
	if h := lipgloss.Height(out); h != m.height {
		t.Errorf("%d lines", h)
	}
	for i, l := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(l); w > m.width {
			t.Errorf("line %d is %d wide", i, w)
		}
	}
	// The cursor starts on the first card, in the first column with cards.
	if c, ok := m.currentCard(); !ok || c.issue.Key != "ABC-4" {
		t.Fatalf("cursor on %+v", c)
	}
	containsAllShell(t, "detail", plainShell(m.detailBoard()), "ABC-4 Fresh idea", "Up next", "To Do", "Ann", "ABC-100 Payments epic", "● you · readiness", "no plan, session or MR yet")

	pressShell(m, "l")
	d := plainShell(m.detailBoard())
	containsAllShell(t, "detail", d, "ABC-1 Payments export", "Review", "◆ needs you · answer export csv", "◆ needs you export csv",
		"!11 payments", "cost $30.00+?", "Opus 5 $25.00", "Haiku 4.5 $5.00", "export csv $25.00", "gone-ses $5.00")

	// Narrow: the board scrolls sideways to keep the cursor's column on screen.
	m.width = 80
	s = screenShell(m)
	containsAllShell(t, "narrow", s, "‹ Plan 0", "Building 0", "Review 2 ›")
	lacksShell(t, "narrow", s, "Up next")
	for i, l := range strings.Split(m.View(), "\n") {
		if w := ansi.StringWidth(l); w > m.width {
			t.Errorf("narrow line %d is %d wide", i, w)
		}
	}
	pressShell(m, "h")
	containsAllShell(t, "back left", screenShell(m), "Up next 1", "Building 0 ›")

	// A column taller than the screen scrolls on its own and says what's below.
	for i := range 12 {
		m.jira.Sprint = append(m.jira.Sprint, atlassian.Issue{Key: fmt.Sprintf("ABC-%d", 50+i), Summary: "more", Category: "new"})
	}
	m.reindex()
	m.height = 30
	s = screenShell(m)
	containsAllShell(t, "tall", s, "Up next 13", "↓ ")
	pressShell(m, "G")
	containsAllShell(t, "bottom", screenShell(m), "ABC-61")
	lacksShell(t, "bottom", screenShell(m), "ABC-4 ")
	pressShell(m, "g")
	containsAllShell(t, "top again", screenShell(m), "ABC-4 ")
	if pressShell(m, "z") != nil {
		t.Error("an unbound key does nothing")
	}
}

func TestBoardEmptyShell(t *testing.T) {
	m := newModelShell(t)
	m.jira = atlassian.JiraSnapshot{}
	m.reindex()
	m.switchView(vBoard)
	containsAllShell(t, "no Atlassian", screenShell(m), "ATLASSIAN_TOKEN not set")
	if m.viewList(0) != "" || plainShell(m.detailBoard()) == "" {
		t.Error("no room")
	}
	m.atlErr = nil
	if !m.animates() {
		t.Error("asking Jira animates")
	}
	containsAllShell(t, "loading", screenShell(m), "asking Jira…")
	m.jira.FetchedAt = time.Now()
	containsAllShell(t, "empty", screenShell(m), "nothing on the board — a shows the team, d what's done")
	pressShell(m, "a")
	containsAllShell(t, "team", screenShell(m), "no team sprint: set jira_project", "team sprint by stage")
	for _, k := range []string{"enter", "l", "o"} {
		if cmd := pressShell(m, k); cmd != nil {
			t.Errorf("%s on an empty board: a command", k)
		}
	}
}

func TestBoardKeysShell(t *testing.T) {
	m := boardModelShell(t)
	// tab still switches views; h/l move between columns.
	pressShell(m, "1", "2", "down")
	if m.view != vBoard {
		t.Fatalf("2 → %s", viewNames[m.view])
	}
	pressShell(m, "l")
	if c, _ := m.currentCard(); m.view != vBoard || c.issue.Key != "ABC-1" {
		t.Fatalf("l moved to %s %v", viewNames[m.view], c.issue.Key)
	}
	pressShell(m, "j")
	if c, _ := m.currentCard(); c.issue.Key != "ABC-2" {
		t.Errorf("j: %s", c.issue.Key)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.boardCol != 3 {
		t.Errorf("past the last column with cards: %d", m.boardCol)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	pressShell(m, "l")
	if c, _ := m.currentCard(); c.issue.Key != "ABC-2" {
		t.Errorf("each column keeps its cursor: %s", c.issue.Key)
	}
	pressShell(m, "h", "h")
	if c, _ := m.currentCard(); c.issue.Key != "ABC-4" {
		t.Errorf("h: %s", c.issue.Key)
	}
	if m.currentTicket() != "ABC-4" {
		t.Errorf("ticket notes see %q", m.currentTicket())
	}
	pressShell(m, "tab")
	if m.view != vSessions {
		t.Errorf("tab → %s", viewNames[m.view])
	}
	pressShell(m, "shift+tab")
	if m.boardFocus {
		t.Error("coming back to the Board starts outside it")
	}
	pressShell(m, "down")

	// No sessions: enter offers to start one; with sessions it lists them.
	pressShell(m, "enter")
	if m.chooser == nil || !strings.Contains(m.chooser.title, "ABC-4") {
		t.Fatalf("enter without sessions: %v", m.chooser != nil)
	}
	m.chooser = nil
	pressShell(m, "l", "k", "enter")
	if m.chooser == nil || m.chooser.title != "sessions for ABC-1" {
		t.Fatalf("enter with sessions: %v", m.chooser != nil)
	}
	m.chooser = nil
	for _, k := range []string{"c", "C"} {
		pressShell(m, k)
		if m.chooser == nil || m.background != (k == "C") {
			t.Errorf("%s: chooser %v background %v", k, m.chooser, m.background)
		}
		m.chooser = nil
	}
	m.background = false
	if pressShell(m, "y") == nil || pressShell(m, "M") == nil {
		t.Error("copy and open MR give commands")
	}
	pressShell(m, "t")
	containsAllShell(t, "no threads", m.flash, "no open MR of yours for ABC-1")
	pressShell(m, "j", "t")
	if m.chooser == nil || !strings.Contains(m.chooser.title, "!21") {
		t.Errorf("threads of !21: %v", m.chooser != nil)
	}
	m.chooser = nil
	for _, k := range []string{"o", "m"} {
		m.flash = ""
		if pressShell(m, k) != nil || !strings.Contains(m.flash, "Jira isn't set up") {
			t.Errorf("%s without Jira: %q", k, m.flash)
		}
	}
	m.atl = &atlassian.Client{Base: "https://acme.atlassian.net"}
	if pressShell(m, "o") == nil || pressShell(m, "m") == nil {
		t.Error("open and move give commands")
	}
	pressShell(m, "h", "h")
	pressShell(m, "M")
	containsAllShell(t, "no MR", m.flash, "no MR for ABC-4 yet")

	// a and d toggle the scope and Done, and stay that way.
	pressShell(m, "d", "l", "l")
	containsAllShell(t, "done", screenShell(m), "Done 1", "ABC-3", "‹ Plan", "my tickets incl. done")
	pressShell(m, "a")
	if p := loadBoardPrefs(); !p.Team || !p.Done {
		t.Errorf("prefs %+v", p)
	}
	if c, _ := m.currentCard(); c.issue.Key != "ABC-7" {
		t.Errorf("team board: %s", c.issue.Key)
	}
	pressShell(m, "a", "d")

	// The filter narrows every column.
	pressShell(m, "/")
	typeShell(m, "invoices")
	pressShell(m, "enter")
	s := screenShell(m)
	containsAllShell(t, "filter", s, "Review 1", "ABC-2")
	lacksShell(t, "filter", s, "Fresh idea", "Payments export")
}

func TestBoardAnimatesShell(t *testing.T) {
	m := boardModelShell(t)
	if m.animates() {
		t.Error("a board without Claude at work is still")
	}
	// With the threads on its MR answered, ABC-2 waits on its working session.
	m.gl.Mine[0].Threads = nil
	m.reindex()
	if !m.animates() {
		t.Error("a card whose Claude works animates")
	}
	containsAllShell(t, "agent", screenShell(m), "ABC-2  ✶ thinking")
}

func TestCardMRShell(t *testing.T) {
	merged := boardMR(1, "merged", nil)
	if mr, ok := cardMR(boardCard{mrs: []gitlab.MR{merged}}, false); !ok || mr.IID != 1 {
		t.Error("a merged MR still opens")
	}
	if _, ok := cardMR(boardCard{mrs: []gitlab.MR{merged}}, true); ok {
		t.Error("a merged MR has no threads to show")
	}
}

func TestBoardChipsShell(t *testing.T) {
	merged := boardMR(1, "merged", nil)
	draft := boardMR(2, "opened", func(mr *gitlab.MR) { mr.Draft = true })
	running := boardMR(3, "opened", func(mr *gitlab.MR) { mr.Pipeline = &gitlab.Pipeline{Status: "running"} })
	bare := boardMR(4, "opened", nil)
	got := plainShell(cardChips(boardCard{mrs: []gitlab.MR{merged, draft, running, bare}, turn: turnColleague, next: "!4 waits"}))
	if got != "!1 merged · !2 draft · !3 … · !4 · !4 waits" {
		t.Errorf("chips %q", got)
	}
	m := newModelShell(t)
	if b := plainShell(m.turnBadge(boardCard{turn: turnColleague})); b != "⧗ waiting" {
		t.Errorf("badge %q", b)
	}
}

func TestBoardKeymapShell(t *testing.T) {
	// Only the board's column keys may take tab-switching keys.
	_, problems := newKeymap(map[string]config.KeyList{"jira.open": {"l"}})
	if len(problems) != 1 || !strings.Contains(problems[0], "global.next_view") {
		t.Errorf("problems %v", problems)
	}
	km, problems := newKeymap(map[string]config.KeyList{"board.right": {"L"}})
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	m := &Model{km: km, view: vBoard}
	if m.action("l") != "global.next_view" || m.action("L") != "board.right" {
		t.Errorf("l → %s, L → %s", m.action("l"), m.action("L"))
	}
}

func TestLedgerFromUsageShell(t *testing.T) {
	m := newModelShell(t)
	dir := t.TempDir()
	writeUsage := func(rel, branch, id string) {
		t.Helper()
		line := `{"type":"assistant","gitBranch":"` + branch + `","requestId":"r` + id + `","timestamp":"` + time.Now().UTC().Format(time.RFC3339) +
			`","message":{"id":"m` + id + `","model":"claude-opus-5","usage":{"output_tokens":1000000}}}` + "\n"
		p := filepath.Join(dir, "projects", rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeUsage("p/aaaaaaaa-wait.jsonl", "feat/ABC-9/x", "1")
	writeUsage("p/cccccccc-idle.jsonl", "feat/ABC-1/x", "2")
	writeUsage("p/zzzz.jsonl", "feat/ABC-1/y", "3")
	m.usageScanner = claude.NewUsageScanner([]claude.Profile{{Name: "work", Dir: dir}}, 31)
	m.usageScanner.Scan()
	// By hand: the idle session belongs to no ticket.
	m.links.SetTicket("cccccccc-idle", "-")
	m.reindex()
	m.Update(usageMsg{})
	// The session's ticket from claudia (its branch is ABC-1) wins over the transcript's ABC-9.
	if got := m.costs["ABC-1"]; got.Sessions != 2 || got.USD != 50 {
		t.Errorf("ABC-1 %+v", got)
	}
	if got := m.costs[""]; got.Sessions != 1 {
		t.Errorf("no ticket %+v", got)
	}
	if again := claude.OpenLedger(); again.Ticket("ABC-1").Sessions != 2 {
		t.Error("the ledger wasn't saved")
	}

	// A ledger that can't be saved says so.
	t.Setenv("XDG_STATE_HOME", "/dev/null/nope")
	m.ledger = nil
	m.Update(usageMsg{})
	containsAllShell(t, "save error", m.flash, "cost ledger:")
}

func TestBoardStepInOutShell(t *testing.T) {
	m := boardModelShell(t)
	m.switchView(vSessions)
	m.switchView(vBoard)
	if m.boardFocus {
		t.Fatal("arriving on the Board starts outside it")
	}
	s := screenShell(m)
	containsAllShell(t, "outside", s, "↓ step in", "↓ steps in", "↓ steps into the board")
	lacksShell(t, "outside", s, "▌", "h/l columns")
	if pressShell(m, "enter"); !strings.Contains(m.flash, "steps into the board") {
		t.Errorf("a card key outside: %q", m.flash)
	}
	m.flash = ""
	pressShell(m, "up")
	if m.boardFocus || m.view != vBoard {
		t.Error("up outside does nothing")
	}

	// Outside, the side keys switch tabs both ways.
	pressShell(m, "right")
	if m.view != vSessions {
		t.Errorf("right outside: %s", viewNames[m.view])
	}
	m.switchView(vBoard)
	pressShell(m, "h")
	if m.view != vInbox {
		t.Errorf("h outside: %s", viewNames[m.view])
	}

	// Down steps in on the first card without moving past it.
	m.switchView(vBoard)
	pressShell(m, "down")
	if c, ok := m.currentCard(); !m.boardFocus || !ok || c.issue.Key != "ABC-4" {
		t.Fatalf("stepped in on %+v", c)
	}
	containsAllShell(t, "inside", screenShell(m), "▌ ABC-4", "h/l columns", "↑ on a top card steps out")
	pressShell(m, "l")
	if m.view != vBoard || m.boardCol == 0 {
		t.Errorf("l inside moves columns, now %s col %d", viewNames[m.view], m.boardCol)
	}

	// Up on a column's top card, or esc, steps back out; tabs work again.
	pressShell(m, "up")
	if m.boardFocus {
		t.Error("up on the top card steps out")
	}
	pressShell(m, "G")
	if !m.boardFocus {
		t.Error("G steps in too")
	}
	pressShell(m, "esc")
	if m.boardFocus || m.view != vBoard {
		t.Error("esc steps out without quitting")
	}

	// Inside, a filter that hides every card leaves an empty panel, not the hint.
	pressShell(m, "down")
	m.filter.SetValue("no such ticket")
	m.refilter()
	if d := plainShell(m.detailBoard()); strings.Contains(d, "steps into") {
		t.Errorf("hint while inside: %s", d)
	}
	m.filter.SetValue("")

	// An empty board can't be stepped into.
	m.boardFocus = false
	m.jira.Sprint = nil
	m.refilter()
	pressShell(m, "down")
	if m.boardFocus {
		t.Error("stepped into an empty board")
	}
}

func TestBoardSprintsShell(t *testing.T) {
	m := boardModelShell(t)
	active := func(name string) *atlassian.Sprint { return &atlassian.Sprint{Name: name, State: "active"} }
	future := func(name string, inDays int) *atlassian.Sprint {
		return &atlassian.Sprint{Name: name, State: "future", Start: time.Now().AddDate(0, 0, inDays)}
	}
	for i := range m.jira.Sprint {
		switch m.jira.Sprint[i].Key {
		case "ABC-4", "ABC-2":
			m.jira.Sprint[i].Sprint = active("Sprint 8")
		case "ABC-1":
			m.jira.Sprint[i].Sprint = active("Discussion 2")
		case "ABC-5":
			m.jira.Sprint[i].Sprint = future("Sprint 10", 28)
		}
	}
	m.jira.Sprint = append(m.jira.Sprint,
		atlassian.Issue{Key: "ABC-6", Summary: "Sooner idea", Status: "To Do", Category: "new", Sprint: future("Sprint 9", 14)},
		atlassian.Issue{Key: "ABC-8", Summary: "Also sooner", Status: "Dev in progress", Category: "indeterminate", Sprint: future("Sprint 9", 14)})
	m.reindex()
	m.refilter()

	// The current sprint: its name in the subtitle, future tickets folded away.
	s := screenShell(m)
	containsAllShell(t, "current", s, "my tickets in Sprint 8 by stage", "f next sprint", "+ 2 in future sprints")
	lacksShell(t, "current", s, "ABC-5", "ABC-6")
	if !strings.Contains(s, "ABC-8") {
		t.Error("an in-progress ticket shows even though it's planned for later")
	}
	containsAllShell(t, "detail", plainShell(m.detailBoard()), "Sprint 8")

	// f steps to the soonest future sprint, then the next, then back.
	pressShell(m, "f")
	s = screenShell(m)
	containsAllShell(t, "sprint 9", s, "my tickets in Sprint 9 by stage", "ABC-6", "ABC-8")
	lacksShell(t, "sprint 9", s, "ABC-4", "ABC-5", "in future sprints")
	containsAllShell(t, "flash", m.flash, "showing Sprint 9")
	if m.boardFocus {
		t.Error("switching sprints steps out of the board")
	}
	pressShell(m, "f")
	containsAllShell(t, "sprint 10", screenShell(m), "in Sprint 10 by stage", "ABC-5")
	pressShell(m, "f")
	containsAllShell(t, "back", screenShell(m), "in Sprint 8 by stage", "ABC-4")

	// A sprint that disappears on refresh drops the board back to the current one.
	pressShell(m, "f", "f")
	for i := range m.jira.Sprint {
		m.jira.Sprint[i].Sprint = nil
	}
	m.reindex()
	m.refilter()
	if m.boardSprint != 0 {
		t.Errorf("still on sprint %d", m.boardSprint)
	}
	if pressShell(m, "f"); !strings.Contains(m.flash, "no future sprint") {
		t.Errorf("f with no future sprint: %q", m.flash)
	}

	// Sprints starting together sort by name; a tie between active sprints goes
	// to the name that sorts first, so the subtitle doesn't flicker.
	same := time.Now()
	got := futureSprints([]boardCard{
		{issue: atlassian.Issue{Sprint: &atlassian.Sprint{Name: "B", State: "future", Start: same}}},
		{issue: atlassian.Issue{Sprint: &atlassian.Sprint{Name: "A", State: "future", Start: same}}},
		{issue: atlassian.Issue{Sprint: &atlassian.Sprint{Name: "A", State: "future", Start: same}}},
	})
	if strings.Join(got, ",") != "A,B" {
		t.Errorf("futures %v", got)
	}
	m.vis.board = [][]boardCard{{
		{issue: atlassian.Issue{Sprint: active("B")}},
		{issue: atlassian.Issue{Sprint: active("A")}},
	}}
	if got := m.currentSprint(); got != "A" {
		t.Errorf("tie: %q", got)
	}
}

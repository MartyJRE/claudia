package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/runs"
)

func todoBoardShell(t *testing.T, projects ...string) (*Model, *runs.Store) {
	t.Helper()
	m := boardModelShell(t)
	db := withRunsShell(t, m)
	withConfig(t, func(c *config.Config) { c.Todos.Projects = projects })
	m.refilter()
	return m, db
}

func TestTodoCaptureShell(t *testing.T) {
	m, db := todoBoardShell(t, "ABC", "OPS")
	pressShell(m, "T")
	if m.prompt == nil || m.prompt.title != "a todo that isn't in Jira" {
		t.Fatalf("T: %+v", m.prompt)
	}
	pressShell(m, "enter")
	if m.prompt != nil {
		t.Error("an empty title cancels")
	}
	pressShell(m, "T")
	typeShell(m, "rotate the staging certs")
	pressShell(m, "enter")
	if m.prompt == nil || !strings.Contains(m.prompt.title, "who asked for “rotate the staging certs”") {
		t.Fatalf("then who: %+v", m.prompt)
	}
	typeShell(m, "Zdeněk")
	pressShell(m, "enter")
	ts := db.Todos(time.Now())
	if len(ts) != 1 || ts[0].Asked != "Zdeněk" || !strings.Contains(m.flash, "noted “rotate the staging certs” in Up next; s tickets it") {
		t.Fatalf("added %+v %q", ts, m.flash)
	}
	// Who asked is optional.
	pressShell(m, "T")
	typeShell(m, "bump node")
	pressShell(m, "enter", "enter")
	if ts := db.Todos(time.Now()); len(ts) != 2 || ts[1].Asked != "" {
		t.Errorf("no asker %+v", ts)
	}
	containsAllShell(t, "on the board", screenShell(m), "✎ todo", "rotate the staging certs", "from Zdeněk")

	m.db = nil
	if m.askTodo() != nil || m.prompt != nil || !strings.Contains(m.flash, "run log isn't open") {
		t.Errorf("no db: %q", m.flash)
	}
}

func TestTodoCardShell(t *testing.T) {
	m, db := todoBoardShell(t, "ABC", "OPS")
	a, _ := db.AddTodo(runs.Todo{Title: "rotate the staging certs", Asked: "Zdeněk", Note: "before Friday", Project: "OPS", Created: time.Now().Add(-2 * time.Hour)})
	m.refilter()
	c := cardShell(t, m, a.Subject())
	if c.stage != stUpNext || c.next != "ticket" || c.turn != turnYou {
		t.Errorf("card %+v", c)
	}
	containsAllShell(t, "lines", wideCard(t, m, a.Subject()), "✎ todo", "rotate the staging certs", "from Zdeněk · 2h · for OPS", "ticket")
	m.boardCol = 0
	m.cur[vBoard].pos = cardIndex(m.vis.board[0], a.Subject())
	containsAllShell(t, "detail", plainShell(m.detailBoard()), "✎ todo rotate the staging certs", "asked by Zdeněk", "for OPS", "before Friday")
	containsAllShell(t, "hints", screenShell(m), "s ticket it", "x tick off", "* agreed order", "T new todo")

	// Two projects: s asks which, with the todo's own preselected.
	pressShell(m, "s")
	if m.chooser == nil || !strings.HasPrefix(m.chooser.title, "ticket “rotate") {
		t.Fatalf("chooser %+v", m.chooser)
	}
	if it, _ := m.chooser.selected(); it.value != "OPS" {
		t.Errorf("preselected %v", it.value)
	}
	msg := pressShell(m, "enter")().(startMsg)
	if msg.sp.Kind != "ticket" || !strings.Contains(strings.Join(msg.sp.Args, " "), "in project OPS") {
		t.Errorf("session %+v", msg.sp)
	}
	if rs := db.For(a.Subject()); len(rs) != 1 || rs[0].Kind != "todo" || rs[0].Step != "ticket" {
		t.Errorf("run %+v", rs)
	}

	for key, want := range map[string]string{"o": "no ticket for this todo yet", "m": "a todo can be ticketed, ticked off or pinned"} {
		pressShell(m, key)
		if !strings.Contains(m.flash, want) {
			t.Errorf("%s: %q", key, m.flash)
		}
	}
	if pressShell(m, "y") == nil {
		t.Error("y copies")
	}
	pressShell(m, "*")
	if _, ok := m.agreed[a.Subject()]; !ok {
		t.Error("a todo can be pinned")
	}
	pressShell(m, "x")
	if got, _ := db.Todo(a.ID); got.Done.IsZero() || !strings.Contains(m.flash, "ticked off") {
		t.Errorf("x: %+v %q", got, m.flash)
	}
	if cardIndex(m.vis.board[0], a.Subject()) >= 0 {
		t.Error("a ticked-off todo leaves the board")
	}
}

func cardIndex(col []boardCard, id string) int {
	for i, c := range col {
		if c.id() == id {
			return i
		}
	}
	return -1
}

func TestTodoTicketedShell(t *testing.T) {
	m, db := todoBoardShell(t, "ABC")
	a, _ := db.AddTodo(runs.Todo{Title: "certs", Created: time.Now()})
	b, _ := db.AddTodo(runs.Todo{Title: "payments thing", Created: time.Now()})
	_ = db.TicketTodo(a.ID, "OPS-9")
	_ = db.TicketTodo(b.ID, "ABC-1")
	m.refilter()
	// Its ticket isn't on the board: the todo stays, saying so; one whose ticket shows leaves.
	c := cardShell(t, m, a.Subject())
	if c.next != "ticketed as OPS-9" {
		t.Errorf("ticketed: %q", c.next)
	}
	for _, col := range m.vis.board {
		for _, c := range col {
			if c.id() == b.Subject() {
				t.Error("a todo whose ticket is on the board")
			}
		}
	}
	m.boardCol, m.boardFocus = 0, true
	m.cur[vBoard].pos = cardIndex(m.vis.board[0], a.Subject())
	containsAllShell(t, "detail", plainShell(m.detailBoard()), "OPS-9, not on the board yet")
	m.atl = nil
	pressShell(m, "o")
	if !strings.Contains(m.flash, "no ticket for this todo yet") {
		t.Errorf("o without Jira: %q", m.flash)
	}

	// One project: s starts right away.
	c2, _ := db.AddTodo(runs.Todo{Title: "logs", Created: time.Now()})
	m.refilter()
	if _, cmd := m.runNext(cardShell(t, m, c2.Subject())); cmd == nil || m.chooser != nil {
		t.Error("one project: no question")
	}
	withConfig(t, func(c *config.Config) { c.Todos.Projects, c.JiraProject = nil, "" })
	c3, _ := db.AddTodo(runs.Todo{Title: "dns", Created: time.Now()})
	m.refilter()
	if _, cmd := m.runNext(cardShell(t, m, c3.Subject())); cmd != nil || !strings.Contains(m.flash, "set todos.projects or jira_project") {
		t.Errorf("no project: %q", m.flash)
	}
}

func TestTodoTurnAndPrompt(t *testing.T) {
	todo := &runs.Todo{ID: 4, Title: "certs"}
	for outcome, want := range map[string]turn{runs.Running: turnAgent, runs.NeedsYou: turnNeedsYou, runs.Waiting: turnColleague} {
		c := boardCard{todo: todo, runs: []runs.Run{{Step: "ticket", Outcome: outcome}}}
		if got, _, _ := todoTurn(c); got != want {
			t.Errorf("%s: %d", outcome, got)
		}
	}
	p := todoPrompt(runs.Todo{ID: 4, Title: "certs", Asked: "Zdeněk", Note: "before Friday"}, "OPS")
	for _, want := range []string{`Zdeněk asked me for this outside Jira: "certs".`, "My note: before Friday", "in project OPS", "wait for my OK", "ctl todo ticket 4 <KEY>"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt misses %q: %s", want, p)
		}
	}
	if !strings.HasPrefix(todoPrompt(runs.Todo{Title: "x"}, "A"), `Someone asked me`) {
		t.Error("no asker")
	}
}

func TestCtlTodosShell(t *testing.T) {
	m, _ := todoBoardShell(t, "ABC", "OPS")
	if _, err := m.AddTodo("certs", "", "", "zzz"); err == nil || !strings.Contains(err.Error(), "zzz isn't in todos.projects (ABC, OPS)") {
		t.Errorf("unknown project: %v", err)
	}
	got, err := m.AddTodo("certs", "Zdeněk", "", "ops")
	if err != nil || got.Project != "OPS" {
		t.Fatalf("add %+v %v", got, err)
	}
	if err := m.TicketTodo("1", "nope"); err == nil {
		t.Error("not a key")
	}
	if err := m.TicketTodo("todo:1", "ops-3"); err != nil {
		t.Error(err)
	}
	if err := m.DoneTodo("1"); err != nil {
		t.Error(err)
	}
	if ts, _ := m.CtlTodos(); len(ts) != 1 || ts[0].Ticket != "OPS-3" || ts[0].Done.IsZero() {
		t.Errorf("todos %+v", ts)
	}
	m.db = nil
	if _, err := m.CtlTodos(); err == nil {
		t.Error("no db")
	}
	if _, err := m.AddTodo("x", "", "", ""); err == nil || m.TicketTodo("1", "A-1") == nil || m.DoneTodo("1") == nil {
		t.Error("no db: errors")
	}
}

func TestTodoEdgesShell(t *testing.T) {
	m, db := todoBoardShell(t, "ABC")
	a, _ := db.AddTodo(runs.Todo{Title: "certs", Created: time.Now()})
	b, _ := db.AddTodo(runs.Todo{Title: "dns", Created: time.Now()})
	_ = db.TicketTodo(b.ID, "OPS-2")
	m.refilter()
	m.boardCol, m.boardFocus = 0, true
	m.cur[vBoard].pos = cardIndex(m.vis.board[0], a.Subject())
	pressShell(m, "*")
	m.cur[vBoard].pos = cardIndex(m.vis.board[0], b.Subject())
	pressShell(m, "*", "K")
	if as := db.Agreed(); as[0].Subject != b.Subject() {
		t.Errorf("K on a todo: %+v", as)
	}
	pressShell(m, "J")
	if as := db.Agreed(); as[1].Subject != b.Subject() {
		t.Errorf("J on a todo: %+v", as)
	}
	m.atl = &atlassian.Client{Base: "https://acme.atlassian.net"}
	m.cur[vBoard].pos = cardIndex(m.vis.board[0], b.Subject())
	if pressShell(m, "o") == nil {
		t.Error("o opens the todo's ticket")
	}
	if l := m.todoCardLines(cardShell(t, m, a.Subject()), 60, true); !strings.Contains(plainShell(l[0]), "▌") {
		t.Error("a selected todo card")
	}
	if m.DoneTodo("x") == nil {
		t.Error("not an id")
	}
	db.Close()
	if pressShell(m, "x"); !strings.Contains(m.flash, "closed") {
		t.Errorf("x on a closed db: %q", m.flash)
	}
	if m.addTodo(runs.Todo{Title: "y"}); !strings.Contains(m.flash, "closed") {
		t.Errorf("add on a closed db: %q", m.flash)
	}
}

package runs

import (
	"testing"
	"time"
)

func TestTodos(t *testing.T) {
	s := open(t)
	a, err := s.AddTodo(Todo{Title: "rotate the staging certs", Asked: "Zdeněk", Project: "DEVOPS", Created: t0})
	if err != nil || a.ID == 0 || a.Subject() != "todo:1" {
		t.Fatalf("add %+v %v", a, err)
	}
	b, _ := s.AddTodo(Todo{Title: "bump node", Note: "after the release", Created: t0.Add(time.Minute)})
	if got := s.Todos(t0); len(got) != 2 || got[0].ID != a.ID || got[1].Note != "after the release" || !got[0].Created.Equal(t0) {
		t.Fatalf("todos %+v", got)
	}
	_ = s.SetAgreed([]string{"ABC-1", a.Subject()}, "", 0, t0)
	if err := s.TicketTodo(a.ID, "DEVOPS-7"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Todo(a.ID); got.Ticket != "DEVOPS-7" || got.Asked != "Zdeněk" {
		t.Errorf("ticketed %+v", got)
	}
	if as := s.Agreed(); as[1].Subject != "DEVOPS-7" || as[1].Pos != 2 {
		t.Errorf("its place passes to the ticket: %+v", as)
	}
	if err := s.TicketTodo(99, "X-1"); err == nil {
		t.Error("no such todo")
	}
	done := t0.Add(time.Hour)
	if err := s.DoneTodo(b.ID, done); err != nil {
		t.Fatal(err)
	}
	if len(s.Todos(done)) != 2 || len(s.Todos(done.Add(time.Second))) != 1 {
		t.Error("a done todo shows only since when asked")
	}
	if got, ok := s.Todo(b.ID); !ok || !got.Done.Equal(done) {
		t.Errorf("done %+v", got)
	}
	_ = s.DoneTodo(b.ID, time.Time{})
	if got, _ := s.Todo(b.ID); !got.Done.IsZero() {
		t.Error("ticked back on")
	}
	if s.DoneTodo(99, done) == nil {
		t.Error("no such todo to tick off")
	}
	if id, ok := TodoID("todo:12"); !ok || id != 12 {
		t.Error("todo id")
	}
	if _, ok := TodoID("ABC-1"); ok {
		t.Error("not a todo")
	}
	s.Close()
	if s.Todos(t0) != nil || s.DoneTodo(1, done) == nil || s.TicketTodo(1, "X") == nil {
		t.Error("a closed store")
	}
	if _, err := s.AddTodo(Todo{Title: "x"}); err == nil {
		t.Error("add on a closed store")
	}
}

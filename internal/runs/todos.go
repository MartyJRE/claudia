package runs

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Todo is work someone asked for that isn't in Jira, often in person. It runs
// through the Board and may become a ticket.
type Todo struct {
	ID      int64
	Title   string
	Note    string
	Asked   string // who asked
	Project string // the Jira project it's meant for, when known
	Ticket  string // the ticket it became
	Created time.Time
	Done    time.Time
}

// TodoPrefix leads a todo's subject in the run log and the agreed order.
const TodoPrefix = "todo:"

func (t Todo) Subject() string { return TodoPrefix + strconv.FormatInt(t.ID, 10) }

// TodoID reads a todo's subject or bare id ("todo:3", "3").
func TodoID(s string) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimPrefix(s, TodoPrefix), 10, 64)
	return id, err == nil && id > 0
}

func (s *Store) AddTodo(t Todo) (Todo, error) {
	res, err := s.db.Exec(`INSERT INTO todos (title, note, asked, project, created) VALUES (?, ?, ?, ?, ?)`,
		t.Title, t.Note, t.Asked, t.Project, ms(t.Created))
	if err != nil {
		return t, err
	}
	t.ID, err = res.LastInsertId()
	return t, err
}

// Todos is every todo not done, and the ones done since since, oldest first.
func (s *Store) Todos(since time.Time) []Todo {
	rows, err := s.db.Query(`SELECT id, title, note, asked, project, ticket, created, done FROM todos
		WHERE done = 0 OR done >= ? ORDER BY created, id`, ms(since))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Todo
	for rows.Next() {
		var t Todo
		var created, done int64
		if rows.Scan(&t.ID, &t.Title, &t.Note, &t.Asked, &t.Project, &t.Ticket, &created, &done) == nil {
			t.Created, t.Done = fromMS(created), fromMS(done)
			out = append(out, t)
		}
	}
	return out
}

// Todo is one todo by id.
func (s *Store) Todo(id int64) (Todo, bool) {
	var t Todo
	var created, done int64
	err := s.db.QueryRow(`SELECT id, title, note, asked, project, ticket, created, done FROM todos WHERE id = ?`, id).
		Scan(&t.ID, &t.Title, &t.Note, &t.Asked, &t.Project, &t.Ticket, &created, &done)
	t.Created, t.Done = fromMS(created), fromMS(done)
	return t, err == nil
}

// TicketTodo records the ticket a todo became. Its place in the agreed order
// passes to the ticket.
func (s *Store) TicketTodo(id int64, key string) error {
	t, ok := s.Todo(id)
	if !ok {
		return fmt.Errorf("no todo %d", id)
	}
	if _, err := s.db.Exec(`UPDATE todos SET ticket = ? WHERE id = ?`, key, id); err != nil {
		return err
	}
	_, err := s.db.Exec(`UPDATE agreed SET subject = ? WHERE subject = ? AND NOT EXISTS (SELECT 1 FROM agreed WHERE subject = ?)`, key, t.Subject(), key)
	return err
}

// DoneTodo ticks a todo off, or back on with the zero time.
func (s *Store) DoneTodo(id int64, at time.Time) error {
	res, err := s.db.Exec(`UPDATE todos SET done = ? WHERE id = ?`, ms(at), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no todo %d", id)
	}
	return nil
}

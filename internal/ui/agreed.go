package ui

import (
	"fmt"
	"time"

	"github.com/MartyJRE/claudia/internal/board"
	"github.com/MartyJRE/claudia/internal/runs"
)

// The order agreed at standup or grooming: * pins a card into it, K/J move it.
// It sorts the Board's columns and the autopilot's queue, and lapses when the
// sprint it was agreed in closes.

func (m *Model) loadAgreed() {
	m.agreed = map[string]runs.Agreement{}
	if m.db == nil {
		return
	}
	if len(m.jira.Sprint) > 0 {
		_ = m.db.PruneAgreed(board.ActiveSprint(m.jira.Sprint))
	}
	for _, a := range m.db.Agreed() {
		m.agreed[a.Subject] = a
	}
}

func (m *Model) agreement(c boardCard) (runs.Agreement, bool) {
	a, ok := m.agreed[c.id()]
	return a, ok
}

// place sorts agreed cards first, in their order.
func (m *Model) place(c boardCard) int {
	if a, ok := m.agreement(c); ok {
		return a.Pos
	}
	return len(m.agreed) + 1
}

func circled(n int) string {
	if n >= 1 && n <= 20 {
		return string(rune('①' + n - 1))
	}
	return fmt.Sprintf("#%d", n)
}

// agreedChip leads a card's first line with its place in the agreed order.
func (m *Model) agreedChip(c boardCard) string {
	if a, ok := m.agreement(c); ok {
		return sAccent.Render(circled(a.Pos)) + " "
	}
	return ""
}

// agreedMeta is the detail panel's word on the agreement: place, when, note.
func (m *Model) agreedMeta(c boardCard) string {
	a, ok := m.agreement(c)
	if !ok {
		return ""
	}
	s := sAccent.Render(circled(a.Pos)) + sDim.Render(" agreed "+agreedWhen(a.At, clockNow()))
	if a.Note != "" {
		s += sDim.Render(", " + a.Note)
	}
	return s
}

func agreedWhen(at, now time.Time) string {
	y1, m1, d1 := at.Date()
	y2, m2, d2 := now.Date()
	days := int(time.Date(y2, m2, d2, 0, 0, 0, 0, time.Local).Sub(time.Date(y1, m1, d1, 0, 0, 0, 0, time.Local)).Hours() / 24)
	switch {
	case days <= 0:
		return "today"
	case days == 1:
		return "yesterday"
	case days < 7:
		return at.Format("Mon")
	}
	return at.Format("2 Jan")
}

// pinCard is *: the card joins the agreed order last, or leaves it.
func (m *Model) pinCard(c boardCard) {
	if m.db == nil {
		m.setFlash(fmt.Sprintf("the run log isn't open: %v", m.dbErr), true)
		return
	}
	pinned, err := m.db.Pin(c.id(), board.ActiveSprint(m.jira.Sprint), clockNow())
	switch {
	case err != nil:
		m.setFlash(err.Error(), true)
	case pinned:
		m.setFlash(c.ref()+" joins the agreed order; "+m.key("board.up")+"/"+m.key("board.down")+" move it", false)
	default:
		m.setFlash(c.ref()+" left the agreed order", false)
	}
	m.loadAgreed()
	m.refilter()
}

// moveCard is K/J: the card one place up or down the agreed order.
func (m *Model) moveCard(c boardCard, by int) {
	if _, ok := m.agreement(c); !ok {
		m.setFlash(c.ref()+" isn't in the agreed order: "+m.key("board.pin")+" pins it", false)
		return
	}
	if err := m.db.MoveAgreed(c.id(), by, board.ActiveSprint(m.jira.Sprint), clockNow()); err != nil {
		m.setFlash(err.Error(), true)
		return
	}
	m.loadAgreed()
	m.refilter()
}

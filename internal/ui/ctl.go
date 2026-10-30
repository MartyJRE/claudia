package ui

import (
	"os"
	"time"

	"github.com/MartyJRE/claudia/internal/state"
	"github.com/MartyJRE/claudia/internal/tmux"
)

// The `claudia ctl` side: the same data the TUI shows, for a Claude session to
// read and act on from its shell.

type CtlItem struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Where       string    `json:"where"`
	From        string    `json:"from,omitempty"`
	Text        string    `json:"text"`
	URL         string    `json:"url,omitempty"`
	Ticket      string    `json:"ticket,omitempty"`
	Since       time.Time `json:"waiting_since"`
	Snoozed     bool      `json:"snoozed,omitempty"`
	ThisSession bool      `json:"this_session,omitempty"`
}

type CtlMR struct {
	URL      string `json:"url"`
	Title    string `json:"title,omitempty"`
	State    string `json:"state,omitempty"` // opened, merged, closed; empty if claudia doesn't track it
	Pipeline string `json:"pipeline,omitempty"`
	Approved bool   `json:"approved,omitempty"`
}

type CtlTicket struct {
	Key    string `json:"key"`
	Title  string `json:"title,omitempty"`
	Status string `json:"status,omitempty"`
	Done   bool   `json:"done,omitempty"`
}

type CtlHere struct {
	Session string     `json:"session"`
	Name    string     `json:"name"`
	Dir     string     `json:"dir"`
	Ticket  *CtlTicket `json:"ticket,omitempty"`
	MRs     []CtlMR    `json:"mrs,omitempty"`
	Items   []CtlItem  `json:"inbox_items"`
	Notes   []string   `json:"notes,omitempty"` // Obsidian notes about the ticket
}

// Headless builds the model from caches and live sessions without a terminal.
func Headless() *Model {
	m := New(Options{})
	m.sessions = m.loadSessions()().(sessionsMsg)
	if m.vault != nil {
		m.vault.Refresh()
		m.vaultNotes = m.vault.All()
	}
	m.reindex()
	return m
}

// CurrentSession finds the Claude session running in this tmux pane.
func (m *Model) CurrentSession() (row, bool) {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return row{}, false
	}
	for _, r := range m.sessions {
		if r.pane != nil && r.pane.ID == pane {
			return r, true
		}
	}
	return row{}, false
}

func (m *Model) linked(it inboxItem, sid string) bool {
	switch {
	case sid == "":
		return false
	case it.kind == kClaude:
		return it.sess.ID == sid
	case it.mr.WebURL != "":
		for _, u := range m.ix.sessionMRs[sid] {
			if u == it.mr.WebURL {
				return true
			}
		}
	}
	return it.ticket != "" && it.ticket == m.ix.sessionTicket[sid]
}

func (m *Model) CtlInbox(sid string) []CtlItem {
	var out []CtlItem
	for _, it := range m.inbox() {
		_, snoozed := m.dismissed[it.id]
		out = append(out, CtlItem{
			ID: it.id, Kind: kindLabel[it.kind], Where: it.where, From: it.who, Text: it.text,
			URL: it.url, Ticket: it.ticket, Since: it.since, Snoozed: snoozed, ThisSession: m.linked(it, sid),
		})
	}
	return out
}

func (m *Model) CtlHere(r row) CtlHere {
	h := CtlHere{Session: r.ID, Name: r.Name(), Dir: r.Dir()}
	if k := m.ix.sessionTicket[r.ID]; k != "" {
		t := &CtlTicket{Key: k}
		if is, ok := m.ix.issues[k]; ok {
			t.Title, t.Status, t.Done = is.Summary, is.Status, is.Category == "done"
		}
		h.Ticket = t
	}
	for _, u := range m.ix.sessionMRs[r.ID] {
		mr := CtlMR{URL: u}
		if g, ok := m.ix.mrs[u]; ok {
			mr.Title, mr.State, mr.Pipeline, mr.Approved = g.Title, g.State, g.PipelineStatus(), g.Approved
		}
		h.MRs = append(h.MRs, mr)
	}
	for _, n := range m.notesFor(m.ix.sessionTicket[r.ID]) {
		h.Notes = append(h.Notes, n.Path)
	}
	for _, it := range m.CtlInbox(r.ID) {
		if it.ThisSession {
			h.Items = append(h.Items, it)
		}
	}
	return h
}

// Snooze hides inbox items until they change, like x in the TUI.
func (m *Model) Snooze(ids []string) int {
	n := 0
	for _, id := range ids {
		if _, ok := m.dismissed[id]; !ok {
			m.dismissed[id] = time.Now()
			n++
		}
	}
	_ = state.Save(dismissedFile, m.dismissed)
	return n
}

// reloadDismissed picks up snoozes written by `claudia ctl`.
func (m *Model) reloadDismissed() bool {
	st, err := os.Stat(state.Path(dismissedFile))
	if err != nil || !st.ModTime().After(m.dismissedAt) {
		return false
	}
	m.dismissedAt = st.ModTime()
	m.dismissed = loadDismissed()
	return true
}

// Home switches the terminal back to claudia's session.
func Home() error { return tmux.Switch("=" + tmux.Home) }

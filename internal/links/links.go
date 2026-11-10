// Package links keeps the pairings claudia can't derive from transcripts: sessions
// started from an MR or a ticket, and tickets set by hand.
package links

import (
	"sync"

	"github.com/MartyJRE/claudia/internal/state"
)

const file = "links.json"

type Links struct {
	mu sync.Mutex
	// SessionMRs: session id → MR web URLs it was started for.
	SessionMRs map[string][]string `json:"session_mrs"`
	// SessionTickets: session id → ticket key; "-" means explicitly none.
	SessionTickets map[string]string `json:"session_tickets"`
}

func Load() *Links {
	l := &Links{}
	l.load()
	return l
}

func (l *Links) load() {
	l.SessionMRs, l.SessionTickets = nil, nil
	state.Load(file, l)
	if l.SessionMRs == nil {
		l.SessionMRs = map[string][]string{}
	}
	if l.SessionTickets == nil {
		l.SessionTickets = map[string]string{}
	}
}

// Reload takes in what the TUI or the watcher wrote since.
func (l *Links) Reload() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.load()
}

// update applies a change to the file as it is now, so the TUI and the
// watcher don't write over each other's links.
func (l *Links) update(change func()) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.load()
	change()
	return state.Save(file, l)
}

func (l *Links) AddMRs(session string, urls []string) error {
	return l.update(func() { l.SessionMRs[session] = append(l.SessionMRs[session], urls...) })
}

func (l *Links) SetTicket(session, key string) error {
	return l.update(func() {
		if key == "" {
			delete(l.SessionTickets, session)
		} else {
			l.SessionTickets[session] = key
		}
	})
}

func (l *Links) Ticket(session string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	k, ok := l.SessionTickets[session]
	return k, ok
}

func (l *Links) MRs(session string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.SessionMRs[session]
}

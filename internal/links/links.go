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
	state.Load(file, l)
	if l.SessionMRs == nil {
		l.SessionMRs = map[string][]string{}
	}
	if l.SessionTickets == nil {
		l.SessionTickets = map[string]string{}
	}
	return l
}

func (l *Links) save() error { return state.Save(file, l) }

func (l *Links) AddMRs(session string, urls []string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.SessionMRs[session] = append(l.SessionMRs[session], urls...)
	return l.save()
}

func (l *Links) SetTicket(session, key string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if key == "" {
		delete(l.SessionTickets, session)
	} else {
		l.SessionTickets[session] = key
	}
	return l.save()
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

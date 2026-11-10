package claude

import (
	"cmp"
	"iter"
	"maps"
	"slices"
	"time"

	"github.com/MartyJRE/claudia/internal/state"
)

// SessionCost is what one session cost, its subagents included.
type SessionCost struct {
	ID      string
	Ticket  string
	Profile string
	Models  map[string]Tokens
	First   time.Time
	Last    time.Time
	USD     float64
	Unknown bool // a model without a known price, left out of USD
	Tokens  int
}

func (c SessionCost) priced() SessionCost {
	c.USD, c.Unknown, c.Tokens = 0, false, 0
	for model, t := range c.Models {
		c.Tokens += t.Total()
		if usd, ok := t.Cost(model); ok {
			c.USD += usd
		} else {
			c.Unknown = true
		}
	}
	return c
}

// TicketCost totals a ticket's sessions; the empty ticket holds the sessions
// that have none.
type TicketCost struct {
	Ticket   string
	USD      float64
	Unknown  bool
	Tokens   int
	Sessions int
	Models   map[string]Tokens
	Last     time.Time
}

func (c SessionCost) total() TicketCost {
	return TicketCost{Ticket: c.Ticket, USD: c.USD, Unknown: c.Unknown, Tokens: c.Tokens, Sessions: 1, Models: c.Models, Last: c.Last}
}

func (t *TicketCost) add(c TicketCost) {
	if t.Models == nil {
		t.Models = map[string]Tokens{}
	}
	t.USD += c.USD
	t.Unknown = t.Unknown || c.Unknown
	t.Tokens += c.Tokens
	t.Sessions += c.Sessions
	for model, tk := range c.Models {
		sum := t.Models[model]
		sum.Add(tk)
		t.Models[model] = sum
	}
	if c.Last.After(t.Last) {
		t.Last = c.Last
	}
}

// EpicCost totals the tickets under one epic; the empty epic holds the rest.
type EpicCost struct {
	TicketCost
	Epic    string
	Tickets []TicketCost
}

// Ledger keeps what every session cost, so the cost outlives Claude Code's
// cleanup of old transcripts. It's the only thing that knows how that's stored.
type Ledger struct {
	entries map[string]ledgerEntry
}

const ledgerFile = "cost-ledger.json"

type ledgerEntry struct {
	Ticket string `json:"ticket,omitempty"`
	// Linked: the ticket came from claudia's links or the session's MR, which
	// a branch never overrides.
	Linked  bool              `json:"linked,omitempty"`
	Profile string            `json:"profile,omitempty"`
	USD     float64           `json:"usd"`
	Tokens  int               `json:"tokens"`
	Models  map[string]Tokens `json:"models"`
	First   time.Time         `json:"first"`
	Last    time.Time         `json:"last"`
}

func (e ledgerEntry) cost(id string) SessionCost {
	return SessionCost{ID: id, Ticket: e.Ticket, Profile: e.Profile, Models: e.Models, First: e.First, Last: e.Last}.priced()
}

// OpenLedger loads the ledger; a missing or broken one starts empty.
func OpenLedger() *Ledger {
	l := &Ledger{entries: map[string]ledgerEntry{}}
	state.Load(ledgerFile, &l.entries)
	if l.entries == nil {
		l.entries = map[string]ledgerEntry{}
	}
	return l
}

func (l *Ledger) Save() error { return state.Save(ledgerFile, l.entries) }

// Merge takes in freshly scanned sessions and says whether anything changed.
// A session keeps its largest total: a rescan of a transcript that still exists
// recomputes the same number, and a deleted one keeps its last.
func (l *Ledger) Merge(scanned []SessionCost) bool {
	changed := false
	for _, c := range scanned {
		c = c.priced()
		old, had := l.entries[c.ID]
		if had && c.Tokens < old.Tokens {
			continue
		}
		e := ledgerEntry{Ticket: c.Ticket, Profile: c.Profile, USD: c.USD, Tokens: c.Tokens, Models: c.Models, First: c.First, Last: c.Last}
		if had && (old.Linked || e.Ticket == "") {
			e.Ticket, e.Linked = old.Ticket, old.Linked
		}
		if had && old.Tokens == e.Tokens && old.Ticket == e.Ticket && old.Last.Equal(e.Last) {
			continue
		}
		l.entries[c.ID] = e
		changed = true
	}
	return changed
}

// SetTicket records the ticket claudia knows a session by, which wins over
// the branch it ran on; "" means it belongs to no ticket.
func (l *Ledger) SetTicket(id, ticket string) bool {
	e, ok := l.entries[id]
	if !ok || (e.Linked && e.Ticket == ticket) {
		return false
	}
	e.Ticket, e.Linked = ticket, true
	l.entries[id] = e
	return true
}

// All lists every session, most recent first.
func (l *Ledger) All() iter.Seq[SessionCost] {
	return func(yield func(SessionCost) bool) {
		if l == nil {
			return
		}
		ids := slices.SortedFunc(maps.Keys(l.entries), func(a, b string) int {
			return cmp.Or(l.entries[b].Last.Compare(l.entries[a].Last), cmp.Compare(a, b))
		})
		for _, id := range ids {
			if !yield(l.entries[id].cost(id)) {
				return
			}
		}
	}
}

// Sessions lists a ticket's sessions, most recent first.
func (l *Ledger) Sessions(ticket string) []SessionCost {
	if l == nil {
		return nil
	}
	var out []SessionCost
	for id, e := range l.entries {
		if e.Ticket == ticket {
			out = append(out, e.cost(id))
		}
	}
	slices.SortFunc(out, func(a, b SessionCost) int { return cmp.Or(b.Last.Compare(a.Last), cmp.Compare(a.ID, b.ID)) })
	return out
}

// Ticket totals one ticket's sessions.
func (l *Ledger) Ticket(key string) TicketCost {
	t := TicketCost{Ticket: key}
	for _, c := range l.Sessions(key) {
		t.add(c.total())
	}
	return t
}

// Tickets totals every ticket, the costliest first.
func (l *Ledger) Tickets() []TicketCost {
	by := map[string]*TicketCost{}
	for c := range l.All() {
		t := by[c.Ticket]
		if t == nil {
			t = &TicketCost{Ticket: c.Ticket}
			by[c.Ticket] = t
		}
		t.add(c.total())
	}
	var out []TicketCost
	for _, t := range by {
		out = append(out, *t)
	}
	slices.SortFunc(out, byCost)
	return out
}

// Epics groups the tickets under their epics, parent[ticket] naming each one's;
// the costliest epic comes first and tickets without one come last.
func (l *Ledger) Epics(parent map[string]string) []EpicCost {
	by := map[string]*EpicCost{}
	for _, t := range l.Tickets() {
		key := parent[t.Ticket]
		e := by[key]
		if e == nil {
			e = &EpicCost{Epic: key, TicketCost: TicketCost{Ticket: key}}
			by[key] = e
		}
		e.Tickets = append(e.Tickets, t)
		e.add(t)
	}
	var out []EpicCost
	for _, e := range by {
		out = append(out, *e)
	}
	slices.SortFunc(out, func(a, b EpicCost) int { return byCost(a.TicketCost, b.TicketCost) })
	return out
}

// byCost puts the costliest first, then the larger, then by key; the empty
// key (no ticket, no epic) goes last.
func byCost(a, b TicketCost) int {
	return cmp.Or(cmp.Compare(emptyLast(a.Ticket), emptyLast(b.Ticket)),
		cmp.Compare(b.USD, a.USD), cmp.Compare(b.Tokens, a.Tokens), cmp.Compare(a.Ticket, b.Ticket))
}

func emptyLast(key string) int {
	if key == "" {
		return 1
	}
	return 0
}

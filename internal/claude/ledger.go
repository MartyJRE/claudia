package claude

import (
	"cmp"
	"database/sql"
	"encoding/json"
	"iter"
	"maps"
	"os"
	"slices"
	"time"

	"github.com/MartyJRE/claudia/internal/state"
	"github.com/MartyJRE/claudia/internal/statedb"
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
// cleanup of old transcripts. It's the only thing that knows that's the costs
// table in state.db, which the TUI and the watcher both write.
type Ledger struct {
	db      *sql.DB
	err     error
	entries map[string]ledgerEntry
	dirty   map[string]bool
}

// ledgerFile is where the ledger lived before state.db; it's imported once.
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

var openDB = statedb.Open

// OpenLedger loads the ledger; when state.db can't be opened it starts empty
// and Save says why.
func OpenLedger() *Ledger {
	l := &Ledger{entries: map[string]ledgerEntry{}, dirty: map[string]bool{}}
	l.db, l.err = openDB()
	if l.err != nil {
		return l
	}
	l.importJSON()
	l.Reload()
	return l
}

// importJSON moves the old JSON ledger into state.db and sets the file aside.
func (l *Ledger) importJSON() {
	var old map[string]ledgerEntry
	if !state.Load(ledgerFile, &old) {
		return
	}
	for id, e := range old {
		if l.put(id, e, "INSERT OR IGNORE") != nil {
			return
		}
	}
	_ = os.Rename(state.Path(ledgerFile), state.Path(ledgerFile)+".migrated")
}

// Reload reads what the other process wrote, keeping the entries not saved yet.
func (l *Ledger) Reload() {
	if l.db == nil {
		return
	}
	rows, err := l.db.Query(`SELECT session, ticket, linked, profile, usd, tokens, models, first, last FROM costs`)
	if err != nil {
		return
	}
	defer rows.Close()
	fresh := map[string]ledgerEntry{}
	for rows.Next() {
		var id, models string
		var e ledgerEntry
		var first, last int64
		if rows.Scan(&id, &e.Ticket, &e.Linked, &e.Profile, &e.USD, &e.Tokens, &models, &first, &last) != nil {
			continue
		}
		_ = json.Unmarshal([]byte(models), &e.Models)
		e.First, e.Last = statedb.FromMS(first), statedb.FromMS(last)
		fresh[id] = e
	}
	for id := range l.dirty {
		fresh[id] = l.entries[id]
	}
	l.entries = fresh
}

func (l *Ledger) put(id string, e ledgerEntry, verb string) error {
	models, _ := json.Marshal(e.Models)
	_, err := l.db.Exec(verb+` INTO costs (session, ticket, linked, profile, usd, tokens, models, first, last) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, e.Ticket, e.Linked, e.Profile, e.USD, e.Tokens, string(models), statedb.MS(e.First), statedb.MS(e.Last))
	return err
}

// Save writes the entries that changed since the last save.
func (l *Ledger) Save() error {
	if l.db == nil {
		return l.err
	}
	for id := range l.dirty {
		if err := l.put(id, l.entries[id], "INSERT OR REPLACE"); err != nil {
			return err
		}
		delete(l.dirty, id)
	}
	return nil
}

func (l *Ledger) Close() error {
	if l.db == nil {
		return nil
	}
	return l.db.Close()
}

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
		l.dirty[c.ID] = true
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
	l.dirty[id] = true
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

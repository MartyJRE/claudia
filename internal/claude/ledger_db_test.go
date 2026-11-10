package claude

import (
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/state"
)

// The JSON ledger of earlier releases moves into state.db once.
func TestLedgerImport(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	now := time.Now().Truncate(time.Second)
	old := map[string]ledgerEntry{
		"a": {Ticket: "ABC-1", Linked: true, Profile: "work", Tokens: 10, Models: map[string]Tokens{"claude-opus-5": {Output: 10}}, First: now, Last: now},
	}
	if err := state.Save(ledgerFile, old); err != nil {
		t.Fatal(err)
	}
	l := OpenLedger()
	defer l.Close()
	if s := l.Sessions("ABC-1"); len(s) != 1 || s[0].Profile != "work" || s[0].Tokens != 10 || !s[0].Last.Equal(now) {
		t.Errorf("imported %+v", s)
	}
	if _, err := os.Stat(state.Path(ledgerFile)); !os.IsNotExist(err) {
		t.Error("the JSON stays where it was")
	}
	if _, err := os.Stat(state.Path(ledgerFile + ".migrated")); err != nil {
		t.Error("the JSON isn't set aside")
	}
	// A second open finds the rows in state.db and nothing to import.
	again := OpenLedger()
	defer again.Close()
	if again.Ticket("ABC-1").Sessions != 1 {
		t.Error("lost on reopen")
	}
}

// The TUI and the watcher each keep a ledger; Reload brings in the other's
// writes and keeps what this one hasn't saved.
func TestLedgerTwoWriters(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	now := time.Now()
	tui, watcher := OpenLedger(), OpenLedger()
	defer tui.Close()
	defer watcher.Close()
	watcher.Merge([]SessionCost{cost("w", "ABC-1", 100, now)})
	if err := watcher.Save(); err != nil {
		t.Fatal(err)
	}
	tui.Merge([]SessionCost{cost("t", "ABC-2", 100, now)})
	tui.Reload()
	if tui.Ticket("ABC-1").Sessions != 1 || tui.Ticket("ABC-2").Sessions != 1 {
		t.Errorf("after reload %+v", tui.Tickets())
	}
	if err := tui.Save(); err != nil {
		t.Fatal(err)
	}
	watcher.Reload()
	if watcher.Ticket("ABC-2").Sessions != 1 {
		t.Error("the watcher doesn't see the TUI's session")
	}
	// A row that doesn't scan is skipped.
	if _, err := tui.db.Exec(`INSERT INTO costs (session, first) VALUES ('bad', 'soon')`); err != nil {
		t.Fatal(err)
	}
	tui.Reload()
	if len(tui.entries) != 2 {
		t.Errorf("entries %v", tui.entries)
	}
	tui.Close()
	tui.Reload()
	if len(tui.entries) != 2 {
		t.Error("a closed ledger keeps what it had")
	}
	tui.Merge([]SessionCost{cost("x", "", 1, now)})
	if tui.Save() == nil {
		t.Error("saved into a closed database")
	}
}

func TestLedgerNoDB(t *testing.T) {
	broken := errors.New("no db")
	old := openDB
	t.Cleanup(func() { openDB = old })
	openDB = func() (*sql.DB, error) { return nil, broken }
	l := OpenLedger()
	if !l.Merge([]SessionCost{cost("a", "ABC-1", 1, time.Now())}) || l.Ticket("ABC-1").Sessions != 1 {
		t.Error("an in-memory ledger still totals")
	}
	l.Reload()
	if l.Save() != broken || l.Close() != nil {
		t.Error("save says why")
	}
}

func TestLedgerImportFails(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := state.Save(ledgerFile, map[string]ledgerEntry{"a": {Ticket: "ABC-1"}}); err != nil {
		t.Fatal(err)
	}
	old := openDB
	t.Cleanup(func() { openDB = old })
	openDB = func() (*sql.DB, error) {
		db, err := old()
		if err == nil {
			_, err = db.Exec(`DROP TABLE costs`)
		}
		return db, err
	}
	l := OpenLedger()
	defer l.Close()
	if _, err := os.Stat(state.Path(ledgerFile)); err != nil {
		t.Error("a failed import keeps the JSON for the next try")
	}
	if len(l.entries) != 0 {
		t.Errorf("entries %v", l.entries)
	}
}

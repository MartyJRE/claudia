package statedb

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/state"
)

// A database made by the first release keeps its runs and gains the new tables.
func TestMigrateFromOne(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	old := Migrations
	Migrations = old[:1]
	db, err := Open()
	Migrations = old
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO runs (id, kind, subject, step, started, outcome) VALUES ('r', 'ticket', 'ABC-1', 'plan', 1, 'ok')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var by string
	if err := db.QueryRow(`SELECT started_by FROM runs WHERE id = 'r'`).Scan(&by); err != nil || by != "" {
		t.Errorf("started_by %q %v", by, err)
	}
	for _, table := range []string{"kv", "overrides", "costs"} {
		if _, err := db.Exec(`SELECT * FROM ` + table); err != nil {
			t.Errorf("%s: %v", table, err)
		}
	}
}

func TestOpenErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	if err := os.WriteFile(filepath.Join(dir, "claudia"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(); err == nil {
		t.Error("opened under a file")
	}
	os.Remove(filepath.Join(dir, "claudia"))
	if err := os.MkdirAll(state.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state.Path(File), []byte(strings.Repeat("not a database ", 100)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(); err == nil {
		t.Error("opened a file that isn't a database")
	}
	os.Remove(state.Path(File))
	old := Migrations
	t.Cleanup(func() { Migrations = old })
	Migrations = append(slices.Clone(old), `CREATE TABLE broken (`)
	if _, err := Open(); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("migration %d", len(old)+1)) {
		t.Errorf("broken migration: %v", err)
	}
}

func TestMS(t *testing.T) {
	at := time.UnixMilli(1_700_000_000_123)
	if MS(time.Time{}) != 0 || !FromMS(0).IsZero() || !FromMS(MS(at)).Equal(at) {
		t.Error("round trip")
	}
}

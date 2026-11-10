package runs

import (
	"strings"
	"time"
)

// Get reads a value the TUI and the watcher share, e.g. whether the autopilot
// is paused.
func (s *Store) Get(key string) (string, bool) {
	var v string
	if err := s.db.QueryRow(`SELECT value FROM kv WHERE key = ?`, key).Scan(&v); err != nil {
		return "", false
	}
	return v, true
}

// Set writes a shared value; "" deletes it.
func (s *Store) Set(key, value string) error {
	if value == "" {
		_, err := s.db.Exec(`DELETE FROM kv WHERE key = ?`, key)
		return err
	}
	_, err := s.db.Exec(`INSERT INTO kv (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// Prefixed is every shared value whose key starts with prefix, by the rest of the key.
func (s *Store) Prefixed(prefix string) map[string]string {
	out := map[string]string{}
	rows, err := s.db.Query(`SELECT key, value FROM kv WHERE substr(key, 1, ?) = ?`, len(prefix), prefix)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) == nil {
			out[strings.TrimPrefix(k, prefix)] = v
		}
	}
	return out
}

// AnyStep is the override that stands for every step of a card.
const AnyStep = "*"

// Overrides is every card's own step modes: subject → step (or AnyStep) → mode.
func (s *Store) Overrides() map[string]map[string]string {
	out := map[string]map[string]string{}
	rows, err := s.db.Query(`SELECT subject, step, mode FROM overrides`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var subj, step, mode string
		if rows.Scan(&subj, &step, &mode) != nil {
			continue
		}
		if out[subj] == nil {
			out[subj] = map[string]string{}
		}
		out[subj][step] = mode
	}
	return out
}

// SetOverride sets a card's mode for one step or AnyStep; "" clears it.
func (s *Store) SetOverride(subject, step, mode string, at time.Time) error {
	if mode == "" {
		_, err := s.db.Exec(`DELETE FROM overrides WHERE subject = ? AND step = ?`, subject, step)
		return err
	}
	_, err := s.db.Exec(`INSERT INTO overrides (subject, step, mode, at) VALUES (?, ?, ?, ?)
		ON CONFLICT (subject, step) DO UPDATE SET mode = excluded.mode, at = excluded.at`, subject, step, mode, ms(at))
	return err
}

// ClearOverrides puts a card back on your settings.
func (s *Store) ClearOverrides(subject string) error {
	_, err := s.db.Exec(`DELETE FROM overrides WHERE subject = ?`, subject)
	return err
}

// PruneOverrides forgets the overrides of cards that left the board: those
// not on it now and untouched since before. The ones still on it are touched.
func (s *Store) PruneOverrides(onBoard []string, at, before time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for _, subj := range onBoard {
		if _, err := tx.Exec(`UPDATE overrides SET at = ? WHERE subject = ?`, ms(at), subj); err != nil {
			tx.Rollback()
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM overrides WHERE at < ?`, ms(before)); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

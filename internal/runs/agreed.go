package runs

import (
	"slices"
	"time"
)

// Agreement is a card's place in the order agreed out loud, at standup or
// grooming. It's never written back to Jira.
type Agreement struct {
	Subject string
	Pos     int // 1 is first
	Note    string
	At      time.Time
	// Sprint is the sprint it was agreed in; it stops counting when that sprint
	// closes. 0: agreed with no sprint active, so it never goes stale.
	Sprint int
}

// Agreed is the agreed order, first first.
func (s *Store) Agreed() []Agreement {
	rows, err := s.db.Query(`SELECT subject, pos, note, at, sprint FROM agreed ORDER BY pos`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Agreement
	for rows.Next() {
		var a Agreement
		var at int64
		if rows.Scan(&a.Subject, &a.Pos, &a.Note, &at, &a.Sprint) == nil {
			a.At = fromMS(at)
			out = append(out, a)
		}
	}
	return out
}

// SetAgreed replaces the whole order with subjects, as agreed now.
func (s *Store) SetAgreed(subjects []string, note string, sprint int, at time.Time) error {
	var as []Agreement
	for _, subj := range subjects {
		if !slices.ContainsFunc(as, func(a Agreement) bool { return a.Subject == subj }) {
			as = append(as, Agreement{Subject: subj, Note: note, At: at, Sprint: sprint})
		}
	}
	return s.saveAgreed(as)
}

// Pin puts subject last in the order; pinned already, it takes it out.
func (s *Store) Pin(subject string, sprint int, at time.Time) (bool, error) {
	as := s.Agreed()
	if i := slices.IndexFunc(as, func(a Agreement) bool { return a.Subject == subject }); i >= 0 {
		return false, s.saveAgreed(slices.Delete(as, i, i+1))
	}
	return true, s.saveAgreed(append(as, Agreement{Subject: subject, At: at, Sprint: sprint}))
}

// MoveAgreed moves subject by places in the order: -1 is one up. Moving
// re-agrees it, so it counts from now and this sprint.
func (s *Store) MoveAgreed(subject string, by, sprint int, at time.Time) error {
	as := s.Agreed()
	i := slices.IndexFunc(as, func(a Agreement) bool { return a.Subject == subject })
	if i < 0 {
		return nil
	}
	a := as[i]
	a.At, a.Sprint = at, sprint
	as = slices.Delete(as, i, i+1)
	j := min(max(i+by, 0), len(as))
	return s.saveAgreed(slices.Insert(as, j, a))
}

// PruneAgreed drops agreements whose sprint closed: any sprint other than
// active, when that's known.
func (s *Store) PruneAgreed(active int) error {
	if active == 0 {
		return nil
	}
	as := s.Agreed()
	kept := slices.DeleteFunc(slices.Clone(as), func(a Agreement) bool { return a.Sprint != 0 && a.Sprint != active })
	if len(kept) == len(as) {
		return nil
	}
	return s.saveAgreed(kept)
}

func (s *Store) saveAgreed(as []Agreement) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM agreed`); err != nil {
		return err
	}
	for i, a := range as {
		if _, err := tx.Exec(`INSERT INTO agreed (subject, pos, note, at, sprint) VALUES (?, ?, ?, ?, ?)`,
			a.Subject, i+1, a.Note, ms(a.At), a.Sprint); err != nil {
			return err
		}
	}
	return tx.Commit()
}

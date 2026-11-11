package runs

import (
	"slices"
	"testing"
	"time"
)

func subjects(as []Agreement) []string {
	var out []string
	for _, a := range as {
		out = append(out, a.Subject)
	}
	return out
}

func TestAgreed(t *testing.T) {
	s := open(t)
	if err := s.SetAgreed([]string{"ABC-2", "ABC-1", "ABC-2", "https://mr/5"}, "Zdeněk, grooming", 7, t0); err != nil {
		t.Fatal(err)
	}
	as := s.Agreed()
	if !slices.Equal(subjects(as), []string{"ABC-2", "ABC-1", "https://mr/5"}) || as[0].Pos != 1 || as[2].Pos != 3 ||
		as[1].Note != "Zdeněk, grooming" || !as[1].At.Equal(t0) || as[1].Sprint != 7 {
		t.Fatalf("set: %+v", as)
	}
	later := t0.Add(time.Hour)
	if pinned, err := s.Pin("ABC-9", 7, later); !pinned || err != nil {
		t.Fatalf("pin: %v %v", pinned, err)
	}
	if pinned, _ := s.Pin("ABC-1", 7, later); pinned {
		t.Error("pinning a pinned card takes it out")
	}
	if got := subjects(s.Agreed()); !slices.Equal(got, []string{"ABC-2", "https://mr/5", "ABC-9"}) {
		t.Errorf("pin/unpin: %v", got)
	}
	_ = s.MoveAgreed("ABC-9", -1, 8, later)
	_ = s.MoveAgreed("ABC-2", -5, 7, later)
	_ = s.MoveAgreed("nope", 1, 7, later)
	as = s.Agreed()
	if !slices.Equal(subjects(as), []string{"ABC-2", "ABC-9", "https://mr/5"}) || as[1].Sprint != 8 || !as[1].At.Equal(later) {
		t.Errorf("move: %+v", as)
	}
	_ = s.MoveAgreed("ABC-2", 9, 7, later)
	if got := subjects(s.Agreed()); got[2] != "ABC-2" {
		t.Errorf("move past the end: %v", got)
	}
	// Sprint 7 closed: its agreements go, the one from sprint 8 and any with no sprint stay.
	_ = s.SetAgreed([]string{"X-0"}, "", 0, t0)
	_, _ = s.Pin("X-7", 7, t0)
	_, _ = s.Pin("X-8", 8, t0)
	_ = s.PruneAgreed(0)
	if len(s.Agreed()) != 3 {
		t.Error("no active sprint known: nothing pruned")
	}
	_ = s.PruneAgreed(8)
	_ = s.PruneAgreed(8)
	if got := subjects(s.Agreed()); !slices.Equal(got, []string{"X-0", "X-8"}) {
		t.Errorf("pruned: %v", got)
	}
	s.Close()
	if s.Agreed() != nil || s.SetAgreed([]string{"a"}, "", 0, t0) == nil {
		t.Error("a closed store")
	}
}

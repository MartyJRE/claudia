package links

import (
	"slices"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	l := Load()
	if _, ok := l.Ticket("s1"); ok || l.MRs("s1") != nil {
		t.Fatal("fresh links aren't empty")
	}
	if err := l.AddMRs("s1", []string{"https://g/a/-/merge_requests/1"}); err != nil {
		t.Fatal(err)
	}
	if err := l.AddMRs("s1", []string{"https://g/b/-/merge_requests/2"}); err != nil {
		t.Fatal(err)
	}
	if err := l.SetTicket("s1", "ABC-1"); err != nil {
		t.Fatal(err)
	}
	if err := l.SetTicket("s2", "-"); err != nil {
		t.Fatal(err)
	}

	r := Load()
	if got := r.MRs("s1"); !slices.Equal(got, []string{"https://g/a/-/merge_requests/1", "https://g/b/-/merge_requests/2"}) {
		t.Errorf("mrs %v", got)
	}
	if k, ok := r.Ticket("s1"); !ok || k != "ABC-1" {
		t.Errorf("ticket %q %v", k, ok)
	}
	if k, ok := r.Ticket("s2"); !ok || k != "-" {
		t.Errorf("explicit none %q %v", k, ok)
	}

	if err := r.SetTicket("s1", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := Load().Ticket("s1"); ok {
		t.Error("cleared ticket still set")
	}
}

package ui

import "testing"

func TestChooserRanksExactNameFirst(t *testing.T) {
	var items []choice
	for _, p := range []string{"Personal/monolisa", "Work/acme/libraries/mono", "Work/monorepo-tools", "Work/x/mono/sub", "Work/m-o-n-o"} {
		items = append(items, choice{search: p, value: p})
	}
	c := newChooser("t", items, nil)
	c.setQuery("mono")
	var got []string
	for _, ch := range c.shown {
		got = append(got, ch.value.(string))
	}
	want := []string{"Work/acme/libraries/mono", "Personal/monolisa", "Work/monorepo-tools", "Work/x/mono/sub", "Work/m-o-n-o"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

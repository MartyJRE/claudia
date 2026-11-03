package claude

import "testing"

func TestShortIDName(t *testing.T) {
	for id, want := range map[string]string{"abc": "abc", "0123456789": "01234567", "": ""} {
		if got := (Session{ID: id}).Name(); got != want {
			t.Errorf("%q: got %q want %q", id, got, want)
		}
	}
}

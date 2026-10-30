package update

import "testing"

func TestCommitOf(t *testing.T) {
	for v, want := range map[string]string{
		"v0.0.0-20261030145546-1133a71d4dc3":       "1133a71d4dc3",
		"v0.0.0-20261030143707-9344af4d9f7a+dirty": "",
		"(devel)": "",
		"v1.2.3":  "",
	} {
		if got, _ := commitOf(v); got != want {
			t.Errorf("%s: got %q want %q", v, got, want)
		}
	}
}

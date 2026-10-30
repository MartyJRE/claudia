package gitlab

import (
	"os"
	"testing"
)

func TestParseMRURL(t *testing.T) {
	os.Setenv("XDG_CONFIG_HOME", t.TempDir())
	os.Setenv("CLAUDIA_GITLAB_HOST", "gitlab.example.com")
	for in, want := range map[string]struct {
		project string
		iid     int
		ok      bool
	}{
		"https://gitlab.example.com/acme/backend/payments/-/merge_requests/615":         {"acme/backend/payments", 615, true},
		"https://gitlab.example.com/acme/infra/-/merge_requests/95/diffs?commit_id=abc": {"acme/infra", 95, true},
		"https://gitlab.other.com/acme/infra/-/merge_requests/95":                       {"", 0, false},
		"https://gitlab.example.com/acme/infra/-/issues/3":                              {"", 0, false},
	} {
		p, iid, ok := ParseMRURL(in)
		if p != want.project || iid != want.iid || ok != want.ok {
			t.Errorf("%s: got %q %d %v", in, p, iid, ok)
		}
	}
}

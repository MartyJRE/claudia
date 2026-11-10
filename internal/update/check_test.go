package update

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/state"
)

func fakeGitHub(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	old := repoAPI
	repoAPI = srv.URL + "/repos/MartyJRE/claudia"
	t.Cleanup(func() { repoAPI = old })
	t.Setenv("XDG_STATE_HOME", t.TempDir())
}

// routes answers each path with its body and anything else with a 400.
func routes(t *testing.T, bodies map[string]string) {
	fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		body, ok := bodies[r.URL.RequestURI()]
		if !ok {
			http.Error(w, "unexpected "+r.URL.String(), 400)
			return
		}
		io.WriteString(w, body)
	})
}

const twoReleases = `[{"tag_name":"v0.2.0","name":"v0.2.0","body":"## ui\n- pills","published_at":"2026-11-10T10:00:00Z"},
	{"tag_name":"v0.1.0","body":"first","published_at":"2026-11-01T10:00:00Z"}]`

func TestCheckAndDue(t *testing.T) {
	fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/MartyJRE/claudia/releases/latest":
			io.WriteString(w, `{"tag_name":"v0.2.0"}`)
		case r.URL.Path == "/repos/MartyJRE/claudia/commits/v0.2.0" && r.Header.Get("Accept") == "application/vnd.github.sha":
			io.WriteString(w, "1133a71d4dc3ffff\n")
		default:
			http.Error(w, "unexpected", 400)
		}
	})
	if !Due(time.Hour) {
		t.Error("due without a previous check")
	}
	if err := Check(); err != nil {
		t.Fatal(err)
	}
	var s status
	if !state.Load(file, &s) || s.Latest != "v0.2.0" || s.Commit != "1133a71d4dc3ffff" || time.Since(s.Checked) > time.Minute {
		t.Errorf("saved %+v", s)
	}
	if Due(time.Hour) {
		t.Error("due right after a check")
	}
	if !Due(-time.Second) {
		t.Error("not due past the interval")
	}
	if Available() {
		t.Error("a test binary has no known version to compare")
	}
}

func TestCheckFailureKeepsState(t *testing.T) {
	fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "rate limited", 403) })
	if err := state.Save(file, status{Latest: "v0.1.0", Checked: time.Now().Add(-2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := Check(); err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("non-200 should be an error: %v", err)
	}
	if Due(time.Hour) {
		t.Error("a failed check must wait until it's due again, not retry every tick")
	}
	var s status
	if !state.Load(file, &s) || s.Latest != "v0.1.0" {
		t.Errorf("a failed check lost the last known release: %+v", s)
	}

	routes(t, map[string]string{"/repos/MartyJRE/claudia/releases/latest": `{"tag_name":"v0.2.0"}`})
	if err := Check(); err == nil {
		t.Error("the tag's commit is part of the check")
	}
	routes(t, map[string]string{"/repos/MartyJRE/claudia/releases/latest": `{`})
	if err := Check(); err == nil {
		t.Error("broken JSON")
	}
	repoAPI = "http://127.0.0.1:1"
	if err := Check(); err == nil {
		t.Error("unreachable GitHub")
	}
}

func TestReleases(t *testing.T) {
	routes(t, map[string]string{
		"/repos/MartyJRE/claudia/releases?per_page=100&page=1": twoReleases,
		"/repos/MartyJRE/claudia/releases?per_page=100&page=2": `[]`,
	})
	list, err := Releases(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Tag != "v0.2.0" || list[0].Body != "## ui\n- pills" || list[0].Published.Day() != 10 || list[1].Name != "" {
		t.Errorf("got %+v", list)
	}
	if list, err := Releases(2); err != nil || len(list) != 0 {
		t.Errorf("past the last page: %+v %v", list, err)
	}
	if _, err := Releases(3); err == nil || !strings.Contains(err.Error(), "400") {
		t.Errorf("got %v", err)
	}
}

func TestFetchLocalBuild(t *testing.T) {
	routes(t, map[string]string{"/repos/MartyJRE/claudia/releases?per_page=100&page=1": twoReleases})
	p, err := Fetch()
	if err != nil {
		t.Fatal(err)
	}
	if p.Current != "" || p.Latest != "v0.2.0" || len(p.Releases) != 1 || p.Releases[0].Tag != "v0.2.0" || p.Commits != nil {
		t.Errorf("a local build sees just the latest release: %+v", p)
	}
}

func TestFetchSinceThisRelease(t *testing.T) {
	built(t, "v0.1.0")
	routes(t, map[string]string{"/repos/MartyJRE/claudia/releases?per_page=100&page=1": twoReleases})
	p, err := Fetch()
	if err != nil {
		t.Fatal(err)
	}
	if p.Current != "v0.1.0" || p.Latest != "v0.2.0" || len(p.Releases) != 1 || p.Releases[0].Tag != "v0.2.0" {
		t.Errorf("got %+v", p)
	}

	built(t, "v0.2.0")
	if p, err := Fetch(); err != nil || p.Latest != "v0.2.0" || p.Releases != nil {
		t.Errorf("up to date: %+v %v", p, err)
	}
	routes(t, map[string]string{"/repos/MartyJRE/claudia/releases?per_page=100&page=1": `[]`})
	if p, err := Fetch(); err != nil || p.Latest != "v0.2.0" || p.Releases != nil {
		t.Errorf("no releases on GitHub: %+v %v", p, err)
	}
}

func TestFetchErrors(t *testing.T) {
	fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "nope", 500) })
	if _, err := Fetch(); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("got %v", err)
	}
	fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "{") })
	if _, err := Fetch(); err == nil {
		t.Error("broken JSON")
	}
	fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "[]") })
	if p, err := Fetch(); err != nil || p.Latest != "" || len(p.Releases) != 0 {
		t.Errorf("empty: %+v %v", p, err)
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v0.2.0", "v0.1.9", true},
		{"v0.10.0", "v0.9.0", true},
		{"v1.0.0", "v0.99.99", true},
		{"v0.1.0", "v0.1.0", false},
		{"v0.1.0", "v0.2.0", false},
		{"1133a71", "v0.1.0", false},
		{"v0.2.0", "", false},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

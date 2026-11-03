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

func TestCheckAndDue(t *testing.T) {
	fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/MartyJRE/claudia/commits/main" || r.Header.Get("Accept") != "application/vnd.github.sha" {
			http.Error(w, "unexpected", 400)
			return
		}
		io.WriteString(w, "1133a71d4dc3ffff\n")
	})
	if !Due(time.Hour) {
		t.Error("due without a previous check")
	}
	if err := Check(); err != nil {
		t.Fatal(err)
	}
	var s status
	if !state.Load(file, &s) || s.Latest != "1133a71d4dc3ffff" || time.Since(s.Checked) > time.Minute {
		t.Errorf("saved %+v", s)
	}
	if Due(time.Hour) {
		t.Error("due right after a check")
	}
	if !Due(-time.Second) {
		t.Error("not due past the interval")
	}
	if Available() {
		t.Error("a test binary has no known commit to compare")
	}
}

func TestCheckFailureKeepsState(t *testing.T) {
	fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "rate limited", 403) })
	if err := state.Save(file, status{Latest: "abc1234", Checked: time.Now().Add(-2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := Check(); err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("non-200 should be an error: %v", err)
	}
	if Due(time.Hour) {
		t.Error("a failed check must wait until it's due again, not retry every tick")
	}
	var s status
	if !state.Load(file, &s) || s.Latest != "abc1234" {
		t.Errorf("a failed check lost the last known commit: %+v", s)
	}
	repoAPI = "http://127.0.0.1:1"
	if err := Check(); err == nil {
		t.Error("unreachable GitHub")
	}
}

func TestFetchLatest(t *testing.T) {
	fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/MartyJRE/claudia/commits" || r.URL.Query().Get("per_page") != "5" {
			http.Error(w, "unexpected "+r.URL.String(), 400)
			return
		}
		io.WriteString(w, `[{"sha":"aaaaaaa1111","commit":{"message":"ui: newest\n\nbody"}},{"sha":"bbbbbbb2222","commit":{"message":"ci: older"}}]`)
	})
	p, err := Fetch()
	if err != nil {
		t.Fatal(err)
	}
	if p.Current != "" || p.Latest != "aaaaaaa" || len(p.Commits) != 2 || p.Commits[0] != (Commit{"aaaaaaa", "ui: newest"}) || p.Commits[1].SHA != "bbbbbbb" {
		t.Errorf("got %+v", p)
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
	if p, err := Fetch(); err != nil || p.Latest != "" || len(p.Commits) != 0 {
		t.Errorf("empty: %+v %v", p, err)
	}
}

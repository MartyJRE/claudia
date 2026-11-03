package ui

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/state"
)

func TestStaleShell(t *testing.T) {
	m := &Model{}
	m.fetchedAt[srcGitlab] = time.Now().Add(-59 * time.Second)
	m.fetchedAt[srcJira] = time.Now().Add(-3 * time.Minute)
	m.fetchedAt[srcConfluence] = time.Now().Add(-3 * time.Minute)
	m.busy[srcRepos] = true
	for s, want := range map[source]bool{srcGitlab: false, srcJira: true, srcConfluence: false, srcRepos: false} {
		if got := m.stale(s); got != want {
			t.Errorf("%s: stale %v, want %v", viewSource(s), got, want)
		}
	}
}

func TestRefreshAllShell(t *testing.T) {
	m := newModelShell(t)
	m.busy[srcRepos] = true
	m.fetchedAt[srcRepos] = time.Now()
	m.fetchedAt[srcConfluence] = time.Now()
	if cmd := m.refreshAll(); cmd == nil {
		t.Fatal("refreshAll fetches")
	}
	for s := source(0); s < nSources; s++ {
		if !m.busy[s] {
			t.Errorf("%s should be fetching", viewSource(s))
		}
	}
	if m.fetchedAt[srcRepos].IsZero() {
		t.Errorf("a fetch already running is left alone")
	}

	m = newModelShell(t)
	pressShell(m, "ctrl+r")
	if m.flash != "refreshing…" {
		t.Errorf("ctrl+r: %q", m.flash)
	}
}

func TestFetchWithoutAtlassianShell(t *testing.T) {
	m := &Model{atlErr: errors.New("ATLASSIAN_TOKEN not set")}
	for _, s := range []source{srcJira, srcConfluence} {
		f, ok := m.fetch(s)().(fetched)
		if !ok || f.src != s || f.data != nil || f.err != m.atlErr {
			t.Errorf("%s without credentials: %+v", viewSource(s), f)
		}
	}
}

func TestLoadCachesShell(t *testing.T) {
	isolateShell(t)
	at := time.Now().Add(-time.Hour).Round(time.Second)
	state.Save(cacheFile[srcGitlab], gitlab.Snapshot{Review: []gitlab.MR{{IID: 1}}, FetchedAt: at})
	state.Save(cacheFile[srcJira], atlassian.JiraSnapshot{Sprint: []atlassian.Issue{{Key: "ABC-1"}}, FetchedAt: at})
	state.Save(cacheFile[srcConfluence], atlassian.ConfluenceSnapshot{Mine: []atlassian.Page{{Title: "p"}}, FetchedAt: at})
	m := &Model{}
	m.loadCaches()
	if len(m.gl.Review) != 1 || len(m.jira.Sprint) != 1 || len(m.conf.Mine) != 1 {
		t.Errorf("caches not loaded")
	}
	for _, s := range []source{srcGitlab, srcJira, srcConfluence} {
		if !m.fetchedAt[s].Equal(at) {
			t.Errorf("%s: fetchedAt %v, want the cache's own time", viewSource(s), m.fetchedAt[s])
		}
	}
	if !m.stale(srcGitlab) {
		t.Errorf("an hour-old cache is refetched")
	}

	isolateShell(t)
	empty := &Model{}
	empty.loadCaches()
	if !empty.fetchedAt[srcGitlab].IsZero() {
		t.Errorf("no cache, never fetched")
	}
}

func TestDismissedToggleShell(t *testing.T) {
	isolateShell(t)
	d := loadDismissed()
	if d == nil || len(d) != 0 {
		t.Fatalf("fresh: %v", d)
	}
	d.toggle("a")
	d.toggle("b")
	d.toggle("a")
	got := loadDismissed()
	if _, ok := got["b"]; !ok || len(got) != 1 {
		t.Errorf("saved state: %v", got)
	}
}

func TestLoadSessionsShell(t *testing.T) {
	isolateShell(t)
	dir := t.TempDir()
	proj := filepath.Join(dir, "projects", "-code-x")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","cwd":"/code/x","gitBranch":"main","sessionId":"jjjjjjjj-1","message":{"role":"user","content":"hello there"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "jjjjjjjj-1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &Model{store: claude.NewStore([]claude.Profile{{Name: "test", Dir: dir}}, time.Hour)}
	rows, ok := m.loadSessions()().(sessionsMsg)
	if !ok || len(rows) != 1 {
		t.Fatalf("got %v", rows)
	}
	r := rows[0]
	if r.ID != "jjjjjjjj-1" || r.live() || r.pane != nil || r.host != "" || r.Profile.Name != "test" {
		t.Errorf("closed session from its transcript: %+v", r)
	}
}

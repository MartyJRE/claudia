package steps

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/repos"
)

func TestMain(m *testing.M) {
	dir, _ := os.MkdirTemp("", "claudia-test")
	os.Setenv("XDG_CONFIG_HOME", dir)
	os.Setenv("XDG_STATE_HOME", dir)
	os.Setenv("CLAUDIA_JIRA_KEYS", "ABC")
	os.Setenv("CLAUDIA_GITLAB_HOST", "gitlab.example.com")
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func mr(project, branch string) gitlab.MR {
	return gitlab.MR{IID: 4, ProjectID: 9, SourceBranch: branch, WebURL: "https://gitlab.example.com/" + project + "/-/merge_requests/4"}
}

func TestPrompts(t *testing.T) {
	c := config.Config{ReviewPrompt: "/review {urls}", ReadinessPrompt: "R {key} {brief}", SessionNames: map[string]string{"qa": "{key}-{verb}", "mr": " - "}}
	if got := FillBrief(Ticket["readiness"].Prompt(c), "ABC-1", "/b.md", nil); got != "R ABC-1 /b.md" {
		t.Errorf("brief %q", got)
	}
	if got := FillBrief("{brief}", "ABC-1", "", errors.New("no")); !strings.Contains(got, "Atlassian MCP") {
		t.Errorf("no brief %q", got)
	}
	for id, s := range Ticket {
		if s.Prompt(config.Get()) == "" || s.Intent == "" || s.Verb == "" {
			t.Errorf("%s: %+v", id, s)
		}
	}
	if got := ReviewPrompt(c, []string{"u1", "u2"}); got != "/review u1 u2" {
		t.Errorf("review %q", got)
	}
	if got := ThreadsPrompt(mr("g/pay", "feat/x")); !strings.Contains(got, "projects/9/merge_requests/4/discussions") {
		t.Errorf("threads %q", got)
	}
	issues := map[string]atlassian.Issue{"ABC-1": {Summary: "Card form"}}
	for _, tc := range []struct{ intent, key, fallback, want string }{
		{"analysis", "ABC-1", "", "analyse: Card form"},
		{"review", "", "feat(ABC-2): Faster search", "analyse: Faster search"},
		{"review", "", "", "analyse: "},
		{"qa", "ABC-1", "", "ABC-1-analyse"},
		{"mr", "ABC-3", "", "analyse: ABC-3"},
		{"general", "", strings.Repeat("x", 60), "analyse: " + strings.Repeat("x", 49) + "…"},
	} {
		if got := Title(c, issues, tc.intent, "analyse", tc.key, tc.fallback); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.intent, got, tc.want)
		}
	}
}

func TestTicketRepo(t *testing.T) {
	pay := repos.Repo{Dir: "/c/pay", Project: "g/pay", Branch: "devel"}
	pay2 := repos.Repo{Dir: "/c/pay2", Project: "g/pay", Branch: "devel"}
	web := repos.Repo{Dir: "/c/web", Project: "g/web", Branch: "feat/abc-1-form"}
	api := repos.Repo{Dir: "/c/api", Project: "g/api", Branch: "feat/ABC-1/x"}
	for _, tc := range []struct {
		name   string
		all    []repos.Repo
		mrs    []gitlab.MR
		picked string
		want   string
	}{
		{"on the ticket's branch", []repos.Repo{pay, web}, nil, "", "/c/web"},
		{"two on its branch", []repos.Repo{web, api}, nil, "", ""},
		{"two on its branch, one picked", []repos.Repo{web, api}, nil, "/c/api", "/c/api"},
		{"two on its branch, another picked", []repos.Repo{web, api, pay}, nil, "/c/pay", ""},
		{"picked before", []repos.Repo{pay, pay2}, nil, "/c/pay2", "/c/pay2"},
		{"picked, since gone", []repos.Repo{pay}, nil, "/c/old", ""},
		{"the one clone of its MR", []repos.Repo{pay, repos.Repo{Dir: "/c/x", Project: "g/x"}}, []gitlab.MR{mr("g/pay", "feat/y")}, "", "/c/pay"},
		{"two clones of its MR", []repos.Repo{pay, pay2}, []gitlab.MR{mr("g/pay", "feat/y")}, "", ""},
		{"MRs in two projects", []repos.Repo{pay}, []gitlab.MR{mr("g/pay", "a"), mr("g/web", "a")}, "", ""},
		{"nothing to go on", []repos.Repo{pay}, nil, "", ""},
	} {
		r, ok := TicketRepo(tc.all, "ABC-1", tc.mrs, tc.picked)
		if ok != (tc.want != "") || r.Dir != tc.want {
			t.Errorf("%s: %q %v", tc.name, r.Dir, ok)
		}
	}
}

func TestReviewClone(t *testing.T) {
	m := mr("g/pay", "feat/y")
	on := repos.Repo{Dir: "/c/on", Project: "g/pay", Branch: "feat/y"}
	off := repos.Repo{Dir: "/c/off", Project: "g/pay", Branch: "devel"}
	for _, tc := range []struct {
		all  []repos.Repo
		want string
	}{
		{[]repos.Repo{off}, "/c/off"},
		{[]repos.Repo{off, on}, "/c/on"},
		{[]repos.Repo{off, off}, ""},
		{nil, ""},
	} {
		if r, ok := ReviewClone(tc.all, m); ok != (tc.want != "") || r.Dir != tc.want {
			t.Errorf("%v: %q %v", tc.all, r.Dir, ok)
		}
	}
}

func TestAnywhereDirtyColor(t *testing.T) {
	root := t.TempDir()
	c := config.Config{CodeRoot: root, WorkDir: "Work"}
	if Anywhere(c) != root {
		t.Error("no work dir: the code root")
	}
	os.MkdirAll(filepath.Join(root, "Work"), 0o755)
	if Anywhere(c) != filepath.Join(root, "Work") {
		t.Error("the work dir")
	}

	repo := t.TempDir()
	if !Dirty(repo) {
		t.Error("not a repo counts as dirty")
	}
	old := gitStatus
	t.Cleanup(func() { gitStatus = old })
	gitStatus = func(string) ([]byte, error) { return []byte("\n"), nil }
	if Dirty(repo) {
		t.Error("clean")
	}
	gitStatus = func(string) ([]byte, error) { return []byte(" M a.go\n"), nil }
	if !Dirty(repo) {
		t.Error("changes")
	}

	if Color(nil) != "red" || Color(map[string]int{"red": 2, "blue": 1, "green": 1}) != "yellow" {
		t.Error("least used colour")
	}
}

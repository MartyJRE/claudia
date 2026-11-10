package ui

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MartyJRE/claudia/internal/launch"
	"github.com/MartyJRE/claudia/internal/repos"
)

func newRepoModelShell(t *testing.T) (*Model, string, *repos.New, *launch.Spec) {
	t.Helper()
	m := newModelShell(t)
	root := repos.Root()
	m.repos = []repos.Repo{
		{Dir: root + "/Work/acme/devops/aws", Project: "acme/devops/aws"},
		{Dir: root + "/Personal/tool", Branch: "main"},
	}
	created, started := &repos.New{}, &launch.Spec{}
	fake(t, &createRepo, func(n repos.New) error {
		*created = n
		return os.MkdirAll(filepath.Join(n.Dir, ".git"), 0o755)
	})
	fake(t, &startSession, func(sp launch.Spec) (string, error) {
		*started = sp
		return "", errors.New("no tmux in tests")
	})
	return m, root, created, started
}

func TestNewRepoOnGitHubShell(t *testing.T) {
	m, root, created, started := newRepoModelShell(t)
	pressShell(m, "n")
	typeShell(m, "gadget")
	if it, _ := m.chooser.selected(); it.value != newRepoRow("gadget") {
		t.Fatalf("a query nothing matches leaves only the new repo row: %+v", it)
	}
	containsAllShell(t, "chooser", screenShell(m), `+ new repo "gadget"…`)
	pressShell(m, "enter")
	containsAllShell(t, "where", screenShell(m), "new repo in ›", "Personal", "GitHub", "Work/acme/devops", "GitLab acme/devops")
	pressShell(m, "enter")
	if m.prompt == nil || m.prompt.in.Value() != "gadget" {
		t.Fatal("the name prompt is prefilled with the query")
	}
	containsAllShell(t, "name", screenShell(m), "new repo in Personal", "enter next")
	pressShell(m, "enter")
	if it, _ := m.chooser.selected(); it.value != "private" {
		t.Errorf("GitHub repos default to private: %+v", it)
	}
	pressShell(m, "enter")
	if m.prompt == nil || m.prompt.in.Value() != "main" {
		t.Fatal("GitHub repos default to main")
	}
	containsAllShell(t, "branch", screenShell(m), "default branch of gadget", "enter create")
	m.Update(pressShell(m, "enter")())
	want := repos.New{Dir: root + "/Personal/gadget", Visibility: "private", Branch: "main"}
	if *created != want {
		t.Errorf("created %+v", *created)
	}
	if started.Dir != want.Dir || started.Name != "gadget" {
		t.Errorf("Claude starts in the new repo: %+v", *started)
	}
	if !slices.ContainsFunc(m.repos, func(r repos.Repo) bool { return r.Dir == want.Dir }) {
		t.Error("the new repo is pickable right away")
	}
}

func TestNewRepoOnGitLabShell(t *testing.T) {
	m, root, created, _ := newRepoModelShell(t)
	pressShell(m, "n")
	for range m.chooser.shown {
		pressShell(m, "down")
	}
	if it, _ := m.chooser.selected(); it.value != newRepoRow("") {
		t.Fatalf("the new repo row comes last: %+v", it)
	}
	pressShell(m, "enter")
	typeShell(m, "devops")
	pressShell(m, "enter")
	if m.prompt == nil || m.prompt.in.Value() != "" {
		t.Fatal("with no query the name starts empty")
	}
	typeShell(m, "svc")
	pressShell(m, "enter")
	if it, _ := m.chooser.selected(); it.value != "internal" {
		t.Errorf("GitLab repos default to internal: %+v", it)
	}
	pressShell(m, "down", "enter")
	if m.prompt == nil || m.prompt.in.Value() != "master" {
		t.Fatal("GitLab repos default to master")
	}
	m.prompt.in.SetValue("develop..")
	pressShell(m, "enter")
	if m.prompt == nil || m.flash != "not a branch name git takes" {
		t.Fatalf("a bad branch keeps the prompt open: %q", m.flash)
	}
	m.prompt.in.SetValue("develop")
	m.Update(pressShell(m, "enter")())
	want := repos.New{Dir: root + "/Work/acme/devops/svc", Group: "acme/devops", GitLabHost: "gitlab.example.com", Visibility: "private", Branch: "develop"}
	if *created != want {
		t.Errorf("created %+v", *created)
	}
}

func TestNewRepoRefusesBadNamesShell(t *testing.T) {
	m, root, _, _ := newRepoModelShell(t)
	if err := os.MkdirAll(root+"/Personal/taken", 0o755); err != nil {
		t.Fatal(err)
	}
	pressShell(m, "n")
	typeShell(m, "taken")
	pressShell(m, "enter", "enter", "enter")
	if m.prompt == nil || m.prompt.in.Value() != "taken" || m.flash != root+"/Personal/taken already exists" {
		t.Errorf("an existing folder keeps the prompt open: %q", m.flash)
	}
	m.prompt.in.SetValue("a b")
	pressShell(m, "enter")
	if m.prompt == nil || m.flash != "letters, digits, . _ and - only" {
		t.Errorf("flash %q", m.flash)
	}
}

func TestNewRepoFailureStartsNothingShell(t *testing.T) {
	m, root, _, started := newRepoModelShell(t)
	m.Update(repoCreatedMsg{repos.New{Dir: root + "/Personal/gadget"}, errors.New("gh repo: name already exists")})
	if m.flash != "gh repo: name already exists" || started.Dir != "" {
		t.Errorf("flash %q, started %+v", m.flash, *started)
	}
}

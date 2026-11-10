package repos

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGroups(t *testing.T) {
	all := []Repo{
		{Dir: "/c/Work/acme/devops/aws", Project: "acme/devops/aws"},
		{Dir: "/c/Work/acme/devops/ecs", Project: "acme/devops/ecs"},
		{Dir: "/c/Work/acme/claude-plugins", Project: "acme/claude-plugins"},
		{Dir: "/c/Work/other/fork", Project: "someone/renamed"},
		{Dir: "/c/Work/nowhere", Project: ""},
		{Dir: "/c/Personal/tool", Project: "me/tool"},
	}
	got := Groups(all, "/c/Work")
	want := map[string]string{"/c/Work/acme/devops": "acme/devops", "/c/Work/acme": "acme"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q, want %q", k, got[k], v)
		}
	}
}

func TestValidBranch(t *testing.T) {
	for b, ok := range map[string]bool{"main": true, "master": true, "release/1.0": true, "-x": false, "a..b": false, "a/": false, "a b": false, "x.lock": false, "": false} {
		if ValidBranch(b) != ok {
			t.Errorf("%q: want %v", b, ok)
		}
	}
}

func TestValidName(t *testing.T) {
	for name, ok := range map[string]bool{"tool": true, "my_tool-2.0": true, ".hidden": false, "a b": false, "x/y": false, "repo.git": false, "": false} {
		if ValidName(name) != ok {
			t.Errorf("%q: want %v", name, ok)
		}
	}
}

// fakeCLIs puts gh and glab on PATH that log their args and cwd to bin/calls.log,
// next to the real git, and returns bin.
func fakeCLIs(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(bin, "calls.log")
	script := "#!/bin/sh\necho \"$(basename \"$0\") $* @ $(pwd) $GITLAB_HOST\" >> " + log + "\n"
	for _, name := range []string{"gh", "glab"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	t.Setenv("PATH", bin+":"+filepath.Dir(git)+":/usr/bin:/bin")
	return bin
}

func remote(dir string) string {
	out, _ := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	return strings.TrimSpace(string(out))
}

func TestCreateGitHub(t *testing.T) {
	log := filepath.Join(fakeCLIs(t), "calls.log")
	dir := filepath.Join(t.TempDir(), "Personal", "tool")
	if err := Create(New{Dir: dir, Visibility: "private", Branch: "main"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Error("git init")
	}
	calls, _ := os.ReadFile(log)
	if want := "gh repo create tool --private --source . --remote origin @ "; !strings.Contains(string(calls), want) {
		t.Errorf("calls %q", calls)
	}
}

func TestCreateGitLab(t *testing.T) {
	log := filepath.Join(fakeCLIs(t), "calls.log")
	dir := filepath.Join(t.TempDir(), "acme", "devops", "tool")
	if err := Create(New{Dir: dir, Group: "acme/devops", GitLabHost: "gitlab.example.com", Visibility: "internal", Branch: "master"}); err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(log)
	if want := "glab repo create --group acme/devops --internal --defaultBranch master @ "; !strings.Contains(string(calls), want) || !strings.Contains(string(calls), "gitlab.example.com") {
		t.Errorf("calls %q", calls)
	}
	if got := remote(dir); got != "git@gitlab.example.com:acme/devops/tool.git" {
		t.Errorf("origin %q", got)
	}
	if head, _ := os.ReadFile(filepath.Join(dir, ".git", "HEAD")); string(head) != "ref: refs/heads/master\n" {
		t.Errorf("HEAD %q", head)
	}
}

func TestCreateReportsTheFailingCLI(t *testing.T) {
	bin := fakeCLIs(t)
	os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\necho 'name already exists on this account' >&2\nexit 1\n"), 0o755)
	dir := filepath.Join(t.TempDir(), "tool")
	err := Create(New{Dir: dir, Visibility: "private", Branch: "main"})
	if err == nil || !strings.Contains(err.Error(), "gh repo: name already exists") {
		t.Errorf("err %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Error("the local repo stays")
	}
}

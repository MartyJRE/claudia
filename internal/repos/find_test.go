package repos

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestOriginProject(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config")
	os.WriteFile(p, []byte(`[core]
	url = not-this
[remote "upstream"]
	url = git@gitlab.example.com:other/repo.git
[remote "origin"]
	fetch = +refs/heads/*:refs/remotes/origin/*
	url = git@gitlab.example.com:acme/pay.git
`), 0o600)
	if got := originProject(p); got != "acme/pay" {
		t.Errorf("got %q", got)
	}
	os.WriteFile(p, []byte("[remote \"upstream\"]\n\turl = git@x:a/b.git\n"), 0o600)
	if got := originProject(p); got != "" {
		t.Errorf("without origin: %q", got)
	}
	if got := originProject(filepath.Join(dir, "missing")); got != "" {
		t.Errorf("missing file: %q", got)
	}
}

func TestClones(t *testing.T) {
	all := []Repo{
		{Dir: "/a", Project: "acme/pay", Branch: "main"},
		{Dir: "/b", Project: "acme/infra", Branch: "feat"},
		{Dir: "/c", Project: "acme/pay", Branch: "feat"},
		{Dir: "/d", Project: "acme/pay", Branch: "devel"},
	}
	got := Clones(all, "acme/pay", "feat")
	if len(got) != 3 || got[0].Dir != "/c" || got[1].Dir != "/a" || got[2].Dir != "/d" {
		t.Errorf("got %v", got)
	}
	if Clones(all, "acme/none", "x") != nil {
		t.Error("clones of an unknown project")
	}
}

func TestFind(t *testing.T) {
	if _, err := exec.LookPath("fd"); err != nil {
		t.Skip("no fd")
	}
	root := t.TempDir()
	for _, r := range []string{"Work/pay", "Personal/x"} {
		git := filepath.Join(root, r, ".git")
		os.MkdirAll(git, 0o755)
		os.WriteFile(filepath.Join(git, "HEAD"), []byte("ref: refs/heads/feat/ABC-1\n"), 0o600)
		os.WriteFile(filepath.Join(git, "config"), []byte("[remote \"origin\"]\n\turl = https://gitlab.example.com/acme/"+filepath.Base(r)+".git\n"), 0o600)
	}
	os.WriteFile(filepath.Join(root, "Work", "pay", ".git", "index"), nil, 0o600)
	got := Find(root)
	if len(got) != 2 || got[0].Dir != filepath.Join(root, "Personal/x") || got[1].Project != "acme/pay" || got[1].Branch != "feat/ABC-1" {
		t.Fatalf("got %+v", got)
	}
	if got[1].Touched.IsZero() || !got[0].Touched.IsZero() {
		t.Errorf("touched %v %v", got[0].Touched, got[1].Touched)
	}
}

// fakeFd puts an fd on PATH that logs its arguments and prints out, or fails
// when out is "fail".
func fakeFd(t *testing.T, out string) string {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(bin, "args")
	script := "#!/bin/sh\nfor a; do printf '%s\\n' \"$a\"; done > " + log + "\n"
	if out == "fail" {
		script += "exit 1\n"
	} else {
		script += "printf '%s' '" + out + "'\n"
	}
	if err := os.WriteFile(filepath.Join(bin, "fd"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	return log
}

func TestFindReadsWhatFdLists(t *testing.T) {
	root := t.TempDir()
	pay := filepath.Join(root, "Work", "pay")
	os.MkdirAll(filepath.Join(pay, ".git"), 0o755)
	os.WriteFile(filepath.Join(pay, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600)
	os.WriteFile(filepath.Join(pay, ".git", "index"), nil, 0o600)
	bare := filepath.Join(root, "bare")
	log := fakeFd(t, bare+"\n"+pay+"\n")
	got := Find(root)
	if len(got) != 2 || got[0].Dir != pay || got[0].Branch != "main" || got[1].Dir != bare || got[1].Branch != "" ||
		got[0].Touched.IsZero() || !got[1].Touched.IsZero() {
		t.Errorf("got %+v", got)
	}
	args, _ := os.ReadFile(log)
	if want := "-H\n-t\nd\n^\\.git$\n" + root + "\n--format\n{//}\n"; string(args) != want {
		t.Errorf("args %q want %q", args, want)
	}
	fakeFd(t, "")
	if got := Find(root); got != nil {
		t.Errorf("nothing found: %+v", got)
	}
	fakeFd(t, "fail")
	if got := Find(root); got != nil {
		t.Errorf("fd failed: %+v", got)
	}
}

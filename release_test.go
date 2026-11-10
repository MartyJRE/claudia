package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestNextVersion runs CI's release script over a scratch repository.
func TestNextVersion(t *testing.T) {
	script, err := filepath.Abs(".github/scripts/next-version.sh")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	env := append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	run := func(name string, args ...string) string {
		t.Helper()
		c := exec.Command(name, args...)
		c.Dir, c.Env = dir, env
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	commit := func(msg string) { run("git", "commit", "-q", "--allow-empty", "-m", msg) }
	next := func() string { t.Helper(); return run(script) }

	run("git", "init", "-q", "-b", "main")
	commit("first")
	if v := next(); v != "v0.1.0" {
		t.Errorf("no tags yet: %q", v)
	}
	run("git", "tag", "v0.1.0")
	if v := next(); v != "" {
		t.Errorf("HEAD is already released: %q", v)
	}
	commit("ui: a fix")
	run("git", "tag", "not-a-version")
	if v := next(); v != "v0.1.1" {
		t.Errorf("patch by default: %q", v)
	}
	commit("ui: a feature\n\nRelease: Minor")
	commit("ui: another fix")
	if v := next(); v != "v0.2.0" {
		t.Errorf("a minor trailer: %q", v)
	}
	commit("config: breaks it\n\nRelease: major")
	if v := next(); v != "v1.0.0" {
		t.Errorf("major wins over minor: %q", v)
	}
	run("git", "tag", "v1.0.0")
	run("git", "tag", "v0.9.0-rc1")
	commit("ui: after the major")
	if v := next(); v != "v1.0.1" {
		t.Errorf("a trailer counts only since the last tag, and only plain versions are tags: %q", v)
	}
	run("git", "tag", "v1.0.10", "HEAD~1")
	if v := next(); v != "v1.0.11" {
		t.Errorf("tags sort as versions, not text: %q", v)
	}
}

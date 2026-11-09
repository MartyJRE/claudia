package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPlanReviewInTmux runs the real binary: a stand-in Claude shows its plan
// prompt in one pane, `claudia plan` reviews that pane's plan in another, and
// the comments typed there arrive as the prompt's feedback.
func TestPlanReviewInTmux(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	for _, bin := range []string{"tmux", "python3", "go"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip("no " + bin)
		}
	}
	root := t.TempDir()
	sock, err := os.MkdirTemp("/tmp", "cl") // a short path, for the socket
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sock) })
	env := []string{"TMUX=", "TMUX_TMPDIR=" + sock, "XDG_CONFIG_HOME=" + filepath.Join(root, "config"), "XDG_STATE_HOME=" + filepath.Join(root, "state"), "HOME=" + root}
	tm := func(args ...string) string {
		t.Helper()
		c := exec.Command("tmux", args...)
		c.Env = append(os.Environ(), env...)
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %s", args, out)
		}
		return strings.TrimSpace(string(out))
	}
	t.Cleanup(func() {
		c := exec.Command("tmux", "kill-server")
		c.Env = append(os.Environ(), env...)
		c.Run()
		// The watcher detaches from tmux, so the server going doesn't end it.
		exec.Command("pkill", "-f", filepath.Join(root, "claudia")+" watch").Run()
	})
	wait := func(what string, ok func() bool) {
		t.Helper()
		for range 200 {
			if ok() {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s", what)
	}

	bin := filepath.Join(root, "claudia")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %s", out)
	}
	prof := filepath.Join(root, "claude-work")
	os.MkdirAll(filepath.Join(root, "config", "claudia"), 0o755)
	cfg, _ := json.Marshal(map[string]any{"profiles": map[string]string{"work": prof}})
	os.WriteFile(filepath.Join(root, "config", "claudia", "config.json"), cfg, 0o600)

	planFile := filepath.Join(root, "plan.md")
	os.WriteFile(planFile, []byte("# Add hello.txt\n\n## Steps\n1. Create it\n2. Commit it\n3. Push it\n"), 0o600)
	sid := "aaaaaaaa-plan"
	project := filepath.Join(prof, "projects", "-repo")
	os.MkdirAll(project, 0o755)
	now := time.Now().UTC().Format(time.RFC3339)
	os.WriteFile(filepath.Join(project, sid+".jsonl"), []byte(
		`{"type":"user","cwd":"/repo","timestamp":"`+now+`","message":{"content":"add hello.txt"}}`+"\n"+
			`{"parentUuid":"a","attachment":{"type":"plan_mode","planFilePath":"`+planFile+`"},"type":"attachment"}`+"\n"+
			`{"type":"ai-title","aiTitle":"hello file"}`+"\n"), 0o600)

	script, _ := filepath.Abs("internal/tmux/testdata/fakeplan.py")
	input := filepath.Join(root, "claude-input")
	claude := tm("new-session", "-d", "-P", "-F", "#{pane_id}", "-x", "140", "-y", "40", "python3 "+script+" "+input+" "+prof+" "+sid+" /repo")
	wait("the stand-in to register", func() bool { m, _ := filepath.Glob(filepath.Join(prof, "sessions", "*.json")); return len(m) == 1 })

	review := tm("new-window", "-P", "-F", "#{pane_id}", "env XDG_CONFIG_HOME="+filepath.Join(root, "config")+" XDG_STATE_HOME="+filepath.Join(root, "state")+" HOME="+root+" "+bin+" plan "+claude+"; sleep 30")
	screen := func() string { return tm("capture-pane", "-p", "-t", review) }
	wait("the review", func() bool { return strings.Contains(screen(), "1. Create it") })
	if s := screen(); !strings.Contains(s, "plan · hello file") || !strings.Contains(s, "0 comment(s)") {
		t.Fatalf("review screen:\n%s", s)
	}

	keys := func(k ...string) {
		tm(append([]string{"send-keys", "-t", review}, k...)...)
		time.Sleep(150 * time.Millisecond)
	}
	keys("G", "k", "V", "j", "c")
	keys("-l", "don't commit or push")
	keys("Enter")
	wait("the comment", func() bool { return strings.Contains(screen(), "you  don't commit or push") })
	keys("k", "k", "c")
	keys("-l", "fine")
	keys("Enter")
	wait("both comments", func() bool { return strings.Contains(screen(), "2 comment(s)") })
	keys("s")

	var got string
	wait("the feedback", func() bool { b, _ := os.ReadFile(input); got = string(b); return strings.HasSuffix(got, "\r") })
	want := "3\x1b[200~Comments on the plan:\r\r> 1. Create it\rfine\r\r> 2. Commit it\r> 3. Push it\rdon't commit or push\x1b[201~\r"
	if got != want {
		t.Errorf("the plan prompt got\n%q\nwant\n%q", got, want)
	}
	wait("claudia to quit", func() bool { return !strings.Contains(screen(), "comment(s)") })
	if b, _ := os.ReadFile(filepath.Join(root, "state", "claudia", "plan-comments.json")); strings.Contains(string(b), "commit") {
		t.Errorf("sent comments are cleared: %s", b)
	}
}

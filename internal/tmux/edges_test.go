package tmux

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// failOn puts a tmux on PATH that fails any call whose arguments contain
// pattern and passes the rest to the real one.
func failOn(t *testing.T, pattern string) {
	t.Helper()
	real, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("no tmux")
	}
	bin := t.TempDir()
	script := "#!/bin/sh\ncase \"$*\" in *\"$TMUX_FAIL_ON\"*) echo 'fake failure' >&2; exit 1;; esac\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("TMUX_FAIL_ON", pattern)
}

func session(t *testing.T, names ...string) map[string]Pane {
	t.Helper()
	for _, s := range names {
		if _, err := tm("new-session", "-d", "-s", s, "-n", "✻ "+s, "sleep 60"); err != nil {
			t.Fatal(err)
		}
	}
	panes := map[string]Pane{}
	for _, p := range Panes() {
		panes[p.Session] = p
	}
	return panes
}

func TestNoServer(t *testing.T) {
	privateServer(t)
	Retitle()
	if Panes() != nil || Borrowed() != nil || Running() {
		t.Error("panes, links or a server after asking an absent one")
	}
}

func TestActiveSession(t *testing.T) {
	privateServer(t)
	session(t, "pay", "bill")
	if name, ok := ActiveSession(); ok || name != "" {
		t.Errorf("without clients: %q", name)
	}
	// A control-mode client attaches without a terminal; it stays while stdin is open.
	cmd := exec.Command("tmux", "-C", "attach-session", "-t", "=bill")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdin.Close(); cmd.Wait() })
	name, ok := "", false
	for i := 0; i < 200 && !ok; i++ {
		time.Sleep(10 * time.Millisecond)
		name, ok = ActiveSession()
	}
	if !ok || name != "bill" {
		t.Errorf("got %q %v", name, ok)
	}
}

func TestNewWindowFails(t *testing.T) {
	privateServer(t)
	failOn(t, "new-session")
	if _, err := NewWindow("pay", "n", t.TempDir(), nil, []string{"sleep", "60"}); err == nil || !strings.HasPrefix(err.Error(), "fake failure: exit status 1") {
		t.Errorf("got %v", err)
	}
}

func TestJoinSplitCantStartSession(t *testing.T) {
	privateServer(t)
	failOn(t, "new-session")
	if _, err := JoinSplit([]Pane{{ID: "%1", Session: "pay", WindowID: "@1"}}); err == nil {
		t.Error("split without a splits session")
	}
}

func TestJoinSplitFailures(t *testing.T) {
	for _, pattern := range []string{"display-message", splitHomeOpt + " pay", splitNameOpt, "break-pane"} {
		t.Run(pattern, func(t *testing.T) {
			privateServer(t)
			p := session(t, "pay")["pay"]
			failOn(t, pattern)
			if _, err := JoinSplit([]Pane{p}); err == nil || !strings.Contains(err.Error(), "fake failure") {
				t.Errorf("got %v", err)
			}
		})
	}
}

func TestJoinSplitIntoMissingWindow(t *testing.T) {
	privateServer(t)
	p := session(t, "pay")["pay"]
	gone := Pane{ID: "%999", Session: SplitSession, WindowID: "@999"}
	if _, err := JoinSplit([]Pane{gone, p}); err == nil {
		t.Error("joined into a window that doesn't exist")
	}
}

func TestJoinTwoSplits(t *testing.T) {
	privateServer(t)
	panes := session(t, "pay", "bill", "car", "dog")
	first, err := JoinSplit([]Pane{panes["pay"], panes["bill"]})
	if err != nil {
		t.Fatal(err)
	}
	second, err := JoinSplit([]Pane{panes["car"], panes["dog"]})
	if err != nil || second == first {
		t.Fatalf("second split %q %v", second, err)
	}
	// Panes of another split keep the home and name they had before.
	merged, err := JoinSplit(Panes())
	if err != nil || merged != first && merged != second {
		t.Fatalf("merged %q %v", merged, err)
	}
	if n, _ := tm("list-panes", "-t", strings.TrimPrefix(merged, "="+SplitSession+":")); len(strings.Split(n, "\n")) != 4 {
		t.Errorf("merged window panes:\n%s", n)
	}
	if err := Unsplit(strings.TrimPrefix(merged, "="+SplitSession+":")); err != nil {
		t.Fatal(err)
	}
	homes, _ := tm("list-windows", "-a", "-F", "#{session_name}:#{window_name}")
	for _, s := range []string{"pay", "bill", "car", "dog"} {
		if !strings.Contains(homes, s+":"+Icon+" "+s) {
			t.Errorf("%s not home: %s", s, homes)
		}
	}
}

func TestUnsplitPaneWithoutHome(t *testing.T) {
	privateServer(t)
	p := session(t, "pay")["pay"]
	if _, err := tm("split-window", "-d", "-t", p.ID, "sleep 60"); err != nil {
		t.Fatal(err)
	}
	if _, err := tm("set-option", "-p", "-t", p.ID, splitNameOpt, "named"); err != nil {
		t.Fatal(err)
	}
	if err := Unsplit(p.WindowID); err != nil {
		t.Fatal(err)
	}
	// Without a recorded home the pane goes to a window of its own in the splits session.
	out, _ := tm("list-panes", "-a", "-F", "#{session_name}:#{window_name}:#{pane_id}")
	if !strings.Contains(out, SplitSession+":named:"+p.ID) {
		t.Errorf("panes %s", out)
	}
}

func TestUnsplitFailures(t *testing.T) {
	for _, pattern := range []string{"new-session", "break-pane"} {
		t.Run(pattern, func(t *testing.T) {
			privateServer(t)
			p := session(t, "pay")["pay"]
			target, err := JoinSplit([]Pane{p})
			if err != nil {
				t.Fatal(err)
			}
			failOn(t, pattern)
			if err := Unsplit(strings.TrimPrefix(target, "="+SplitSession+":")); err == nil || !strings.Contains(err.Error(), "fake failure") {
				t.Errorf("got %v", err)
			}
		})
	}
}

func TestUnsplitLastPaneWithEmptyFields(t *testing.T) {
	privateServer(t)
	p := session(t, "pay")["pay"]
	// No home and no name: the trimmed output loses the line's trailing tabs.
	if err := Unsplit(p.WindowID); err != nil {
		t.Fatal(err)
	}
	if !HasSession(SplitSession) {
		t.Fatal("a pane without a home should get a window of its own in splits")
	}
	if out, _ := tm("list-panes", "-s", "-t", "="+SplitSession, "-F", "#{pane_id}"); out != p.ID {
		t.Errorf("panes in splits %q, want %s", out, p.ID)
	}
}

package tmux

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// privateServer points tmux at a server of the test's own, never the user's.
func privateServer(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux")
	}
	t.Setenv("TMUX", "")
	dir, err := os.MkdirTemp("/tmp", "cl") // t.TempDir is too long for a socket path
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-server").Run() })
}

func TestSplitRoundTrip(t *testing.T) {
	privateServer(t)
	for _, s := range []string{"pay", "bill"} {
		if _, err := tm("new-session", "-d", "-s", s, "-n", "✻ "+s, "sleep 60"); err != nil {
			t.Fatal(err)
		}
	}
	panes := Panes()
	if len(panes) != 2 {
		t.Fatalf("panes: %+v", panes)
	}
	target, err := JoinSplit(panes)
	if err != nil {
		t.Fatal(err)
	}
	split := map[string]int{}
	for _, p := range Panes() {
		split[p.Session]++
	}
	if split[SplitSession] != 2 || len(split) != 1 {
		t.Fatalf("after split: %v (target %s)", split, target)
	}
	win := strings.TrimPrefix(target, "="+SplitSession+":")
	if out, _ := exec.Command("tmux", "list-panes", "-t", target, "-F", "#{pane_top}").Output(); len(strings.Fields(string(out))) != 2 || strings.Fields(string(out))[0] != strings.Fields(string(out))[1] {
		t.Errorf("panes should sit side by side, tops: %q", out)
	}
	if err := Unsplit(win); err != nil {
		t.Fatal(err)
	}
	out, _ := tm("list-windows", "-a", "-F", "#{session_name}:#{window_name}")
	got := strings.Fields(strings.ReplaceAll(out, "\n", " "))
	for _, want := range []string{"pay:" + Icon + " pay", "bill:" + Icon + " bill"} {
		if !strings.Contains(out, want) {
			t.Errorf("after unsplit, windows %v miss %s", got, want)
		}
	}
	if strings.Contains(out, SplitSession+":") {
		t.Errorf("split window left behind: %v", got)
	}
}

func TestBorrow(t *testing.T) {
	privateServer(t)
	for _, s := range []string{"pay", "bill"} {
		if _, err := tm("new-session", "-d", "-s", s, "sleep 60"); err != nil {
			t.Fatal(err)
		}
	}
	var pay Pane
	for _, p := range Panes() {
		if p.Session == "pay" {
			pay = p
		}
	}
	if pay.ID == "" {
		t.Fatalf("pay missing from %+v", Panes())
	}
	if err := Borrow(pay.WindowID, "pay", "bill"); err != nil {
		t.Fatal(err)
	}
	if ps := Panes(); len(ps) != 2 {
		t.Errorf("a borrowed tab must not list its pane twice: %+v", ps)
	}
	if ls := Borrowed(); len(ls) != 1 || ls[0].Session != "bill" || ls[0].Home != "pay" {
		t.Fatalf("borrowed: %+v", ls)
	}

	// Splitting a borrowed window drops the tab and keeps the pane findable.
	if _, err := JoinSplit(Panes()); err != nil {
		t.Fatal(err)
	}
	if ls := Borrowed(); len(ls) != 0 {
		t.Errorf("tab left after split: %+v", ls)
	}
	n := 0
	for _, p := range Panes() {
		if p.Session == SplitSession {
			n++
		}
	}
	if n != 2 {
		t.Errorf("panes in split: %d, all: %+v", n, Panes())
	}
}

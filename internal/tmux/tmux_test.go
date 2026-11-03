package tmux

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestNewWindowAndPanes(t *testing.T) {
	privateServer(t)
	if Running() || HasSession("pay") {
		t.Fatal("fresh server already has sessions")
	}
	if _, ok := ActiveSession(); ok {
		t.Error("active session without clients")
	}
	dir := t.TempDir()
	first, err := NewWindow("pay", "✻ one", dir, []string{"FOO=bar"}, []string{"sleep", "60"})
	if err != nil {
		t.Fatal(err)
	}
	if !Running() || !HasSession("pay") || HasSession("pa") {
		t.Error("HasSession must match the exact name")
	}
	second, err := NewWindow("pay", "✻ two", dir, nil, []string{"sleep", "60"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first, "pay:") || !strings.HasPrefix(second, "pay:") || first == second {
		t.Errorf("targets %q %q", first, second)
	}
	names, _ := tm("list-windows", "-t", "=pay", "-F", "#{window_name}")
	if names != "✻ one\n✻ two" {
		t.Errorf("windows %q", names)
	}
	panes := Panes()
	if len(panes) != 2 {
		t.Fatalf("panes %+v", panes)
	}
	for _, p := range panes {
		if p.Session != "pay" || p.PID <= 0 || !strings.HasPrefix(p.ID, "%") || !strings.HasPrefix(p.WindowID, "@") || p.Index == "" {
			t.Errorf("pane %+v", p)
		}
	}
	if panes[0].Target() != "pay:"+panes[0].Window {
		t.Errorf("target %q", panes[0].Target())
	}

	if env, _ := tm("show-environment", "-t", "=pay", "FOO"); env != "FOO=bar" {
		t.Errorf("env %q", env)
	}
	if err := Switch("=nowhere"); err == nil {
		t.Error("switched without a client")
	}
}

func TestLocator(t *testing.T) {
	privateServer(t)
	if _, err := tm("new-session", "-d", "-s", "pay", "sleep 60"); err != nil {
		t.Fatal(err)
	}
	pane := Panes()[0]
	l := NewLocator()
	if got, ok := l.Pane(pane.PID); !ok || got != pane {
		t.Errorf("pane of its own pid: %+v %v", got, ok)
	}
	if _, ok := l.Pane(os.Getpid()); ok {
		t.Error("the test process isn't in the private server")
	}
	if _, ok := l.comm[pane.PID]; !ok {
		t.Error("ps output not parsed")
	}
}

func TestLocatorHost(t *testing.T) {
	l := &Locator{
		parent: map[int]int{40: 30, 30: 20, 20: 10, 10: 1},
		comm: map[int]string{
			40: "claude",
			30: "-zsh",
			20: "/Applications/Ghostty.app/Contents/MacOS/ghostty",
			10: "/Applications/Setapp/Some IDE.app/Contents/MacOS/ide",
		},
		panes: map[int]Pane{30: {ID: "%3"}},
	}
	if got := l.Host(40); got != "Some IDE" {
		t.Errorf("outermost app: %q", got)
	}
	if got := l.Host(30); got != "Some IDE" {
		t.Errorf("from the shell: %q", got)
	}
	l.comm[10] = "launchd"
	if got := l.Host(40); got != "Ghostty" {
		t.Errorf("got %q", got)
	}
	if got, ok := l.Pane(40); !ok || got.ID != "%3" {
		t.Errorf("pane through parents: %+v", got)
	}
	if l.Host(99) != "" {
		t.Error("unknown pid has a host")
	}
}

func TestBorrowedViewedAndUnborrow(t *testing.T) {
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
	if err := Borrow(pay.WindowID, "pay", "bill"); err != nil {
		t.Fatal(err)
	}
	ls := Borrowed()
	if len(ls) != 1 || ls[0].Viewed || ls[0].WindowID != pay.WindowID {
		t.Fatalf("borrowed %+v", ls)
	}
	if ls[0].Target() != "=bill:"+pay.WindowID {
		t.Errorf("target %q", ls[0].Target())
	}
	if err := Unborrow(ls[0]); err != nil {
		t.Fatal(err)
	}
	if ls := Borrowed(); len(ls) != 0 {
		t.Errorf("after unborrow %+v", ls)
	}
	if !HasSession("pay") || len(Panes()) != 2 {
		t.Errorf("home lost its window: %+v", Panes())
	}
	if err := Borrow("@999", "pay", "bill"); err == nil || !strings.Contains(err.Error(), "exit status") {
		t.Errorf("borrow of a missing window: %v", err)
	}
	if err := Borrow(pay.WindowID, "pay", "nowhere"); err == nil {
		t.Error("borrow into a missing session")
	}
}

func TestUnsplitWhenHomeIsGone(t *testing.T) {
	privateServer(t)
	for _, s := range []string{"pay", "bill"} {
		if _, err := tm("new-session", "-d", "-s", s, "-n", "✻ "+s, "sleep 60"); err != nil {
			t.Fatal(err)
		}
	}
	target, err := JoinSplit(Panes())
	if err != nil {
		t.Fatal(err)
	}
	if HasSession("pay") || HasSession("bill") {
		t.Fatal("sessions whose only window was split should end")
	}
	if name, _ := tm("display-message", "-p", "-t", target, "#{window_name}"); name != splitIcon+" bill │ pay" && name != splitIcon+" pay │ bill" {
		t.Errorf("split tab should name its sessions, got %q", name)
	}
	// Splitting the split again is a no-op that keeps the window.
	again, err := JoinSplit(Panes())
	if err != nil || again != target {
		t.Errorf("re-split %q %v", again, err)
	}
	if err := Unsplit(strings.TrimPrefix(target, "="+SplitSession+":")); err != nil {
		t.Fatal(err)
	}
	if !HasSession("pay") || !HasSession("bill") {
		t.Error("homes not recreated")
	}
	out, _ := tm("list-windows", "-a", "-F", "#{session_name}:#{window_name}")
	if strings.Contains(out, SplitSession+":") {
		t.Errorf("split window left: %s", out)
	}
	if _, err := JoinSplit(nil); err == nil {
		t.Error("split of nothing")
	}
	if err := Unsplit("@999"); err == nil {
		t.Error("unsplit of a missing window")
	}
}

func TestInside(t *testing.T) {
	t.Setenv("TMUX", "")
	if Inside() {
		t.Error("inside without TMUX")
	}
	t.Setenv("TMUX", "/tmp/x,1,0")
	if !Inside() {
		t.Error("not inside with TMUX")
	}
}

func TestRetitle(t *testing.T) {
	privateServer(t)
	for _, n := range []string{"✻ old", "zsh"} {
		if _, err := tm("new-session", "-d", "-s", strings.Fields(n)[len(strings.Fields(n))-1], "-n", n, "sleep 60"); err != nil {
			t.Fatal(err)
		}
	}
	Retitle()
	out, _ := tm("list-windows", "-a", "-F", "#{window_name}")
	if got := strings.Split(out, "\n"); len(got) != 2 || got[0] != Icon+" old" || got[1] != "zsh" {
		t.Errorf("got %q", got)
	}
}

func TestResplitFixesOldLayoutAndLabels(t *testing.T) {
	privateServer(t)
	for _, s := range []string{"pay", "bill"} {
		if _, err := tm("new-session", "-d", "-s", s, "-x", "200", "-y", "50", "-n", "✻ "+s, "sleep 60"); err != nil {
			t.Fatal(err)
		}
	}
	target, err := JoinSplit(Panes())
	if err != nil {
		t.Fatal(err)
	}
	// What an older claudia left behind: stacked panes labelled with "✻ ".
	_, _ = tm("select-layout", "-t", target, "even-vertical")
	panes, _ := tm("list-panes", "-t", target, "-F", "#{pane_id}")
	for _, id := range strings.Fields(panes) {
		_, _ = tm("set-option", "-p", "-t", id, splitNameOpt, "✻ old")
	}

	if _, err := JoinSplit(Panes()); err != nil {
		t.Fatal(err)
	}
	tops, _ := tm("list-panes", "-t", target, "-F", "#{pane_top}")
	if f := strings.Fields(tops); len(f) != 2 || f[0] != f[1] {
		t.Errorf("re-split should lay panes side by side, tops %q", tops)
	}
	labels, _ := tm("list-panes", "-t", target, "-F", "#{"+splitNameOpt+"}")
	if labels != Icon+" old\n"+Icon+" old" {
		t.Errorf("labels %q", labels)
	}
}

func TestStripIcon(t *testing.T) {
	for in, want := range map[string]string{
		Icon + " pay":           "pay",
		"✻ pay":                 "pay",
		"\U000F0208 pay" + bell: "pay",
		" pay":                 "pay",
		"zsh":                   "zsh",
		"zsh" + bell:            "zsh",
		"a b":                   "a b",
		"":                      "",
	} {
		if got := stripIcon(in); got != want {
			t.Errorf("stripIcon(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMarkWaiting(t *testing.T) {
	privateServer(t)
	ids := map[string]string{}
	for _, s := range []string{"pay", "bill"} {
		id, err := tm("new-session", "-d", "-s", s, "-n", Icon+" "+s, "-P", "-F", "#{window_id}", "sleep 60")
		if err != nil {
			t.Fatal(err)
		}
		ids[s] = id
	}
	name := func(s string) string {
		n, _ := tm("display-message", "-p", "-t", ids[s], "#{window_name}")
		return n
	}
	MarkWaiting(map[string]bool{ids["pay"]: true})
	if name("pay") != Icon+" pay"+bell || name("bill") != Icon+" bill" {
		t.Errorf("after marking pay: %q %q", name("pay"), name("bill"))
	}
	MarkWaiting(map[string]bool{ids["pay"]: true})
	if name("pay") != Icon+" pay"+bell {
		t.Errorf("marking twice adds a second bell: %q", name("pay"))
	}
	MarkWaiting(map[string]bool{ids["bill"]: true})
	if name("pay") != Icon+" pay" || name("bill") != Icon+" bill"+bell {
		t.Errorf("after moving the bell: %q %q", name("pay"), name("bill"))
	}
	failOn(t, "list-windows")
	MarkWaiting(nil)
	if name("bill") != Icon+" bill"+bell {
		t.Error("a failed listing must change nothing")
	}
}

func TestSplitKeepsKindIcons(t *testing.T) {
	privateServer(t)
	review := "\U000F0208"
	if _, err := tm("new-session", "-d", "-s", "pay", "-n", review+" pay"+bell, "sleep 60"); err != nil {
		t.Fatal(err)
	}
	if _, err := tm("new-session", "-d", "-s", "bill", "-n", "zsh", "sleep 60"); err != nil {
		t.Fatal(err)
	}
	target, err := JoinSplit(Panes())
	if err != nil {
		t.Fatal(err)
	}
	labels, _ := tm("list-panes", "-t", target, "-F", "#{"+splitNameOpt+"}")
	got := strings.Split(labels, "\n")
	slices.Sort(got)
	if want := []string{"zsh", review + " pay"}; !slices.Equal(got, want) {
		t.Errorf("labels %q, want %q", got, want)
	}
}

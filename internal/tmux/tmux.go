package tmux

import (
	"bytes"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type Pane struct {
	PID     int
	ID      string
	Session string
	Window  string
	Index   string
}

func (p Pane) Target() string { return p.Session + ":" + p.Window }

// Home is the tmux session claudia itself runs in.
const Home = "claudia"

func Inside() bool { return os.Getenv("TMUX") != "" }

func Running() bool { return exec.Command("tmux", "has-session").Run() == nil }

func Panes() []Pane {
	out, err := exec.Command("tmux", "list-panes", "-a", "-F",
		"#{pane_pid}\t#{pane_id}\t#{session_name}\t#{window_index}\t#{pane_index}").Output()
	if err != nil {
		return nil
	}
	var panes []Pane
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 5 {
			continue
		}
		pid, _ := strconv.Atoi(f[0])
		panes = append(panes, Pane{PID: pid, ID: f[1], Session: f[2], Window: f[3], Index: f[4]})
	}
	return panes
}

// Locator maps arbitrary pids to the tmux pane whose shell they descend from.
type Locator struct {
	parent map[int]int
	comm   map[int]string
	panes  map[int]Pane
}

func NewLocator() *Locator {
	l := &Locator{parent: map[int]int{}, comm: map[int]string{}, panes: map[int]Pane{}}
	for _, p := range Panes() {
		l.panes[p.PID] = p
	}
	out, _ := exec.Command("ps", "-A", "-o", "pid=,ppid=,comm=").Output()
	for _, line := range bytes.Split(out, []byte("\n")) {
		f := strings.Fields(string(line))
		if len(f) < 3 {
			continue
		}
		pid, _ := strconv.Atoi(f[0])
		ppid, _ := strconv.Atoi(f[1])
		l.parent[pid] = ppid
		l.comm[pid] = strings.Join(f[2:], " ")
	}
	return l
}

func (l *Locator) Pane(pid int) (Pane, bool) {
	for p := pid; p > 1; p = l.parent[p] {
		if pane, ok := l.panes[p]; ok {
			return pane, true
		}
	}
	return Pane{}, false
}

// Host names the GUI app a pid ultimately runs under (Ghostty, iTerm2, an IDE…).
func (l *Locator) Host(pid int) string {
	host := ""
	for p := pid; p > 1; p = l.parent[p] {
		c := l.comm[p]
		if i := strings.Index(c, ".app/"); i >= 0 {
			host = c[strings.LastIndex(c[:i], "/")+1 : i]
		}
	}
	return host
}

func Switch(target string) error {
	return exec.Command("tmux", "switch-client", "-t", target).Run()
}

func HasSession(name string) bool {
	return exec.Command("tmux", "has-session", "-t", "="+name).Run() == nil
}

// NewWindow runs argv in a new window of session, creating the session if needed,
// and returns the new window's target.
func NewWindow(session, name, dir string, env []string, argv []string) (string, error) {
	var args []string
	if HasSession(session) {
		args = []string{"new-window", "-t", "=" + session + ":"}
	} else {
		args = []string{"new-session", "-d", "-s", session}
	}
	args = append(args, "-P", "-F", "#{session_name}:#{window_index}", "-n", name, "-c", dir)
	for _, e := range env {
		args = append(args, "-e", e)
	}
	args = append(args, argv...)
	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		return "", &cmdError{string(out), err}
	}
	return strings.TrimSpace(string(out)), nil
}

type cmdError struct {
	out string
	err error
}

func (e *cmdError) Error() string { return strings.TrimSpace(e.out) + ": " + e.err.Error() }

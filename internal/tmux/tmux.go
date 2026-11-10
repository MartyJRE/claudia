package tmux

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/MartyJRE/claudia/internal/config"
)

type Pane struct {
	PID      int
	ID       string
	Session  string
	Window   string
	Index    string
	WindowID string
}

func (p Pane) Target() string { return p.Session + ":" + p.Window }

// Home is the tmux session claudia itself runs in.
const Home = "claudia"

func Inside() bool { return os.Getenv("TMUX") != "" }

func Running() bool { return exec.Command("tmux", "has-session").Run() == nil }

func Panes() []Pane {
	out, err := exec.Command("tmux", "list-panes", "-a", "-F",
		"#{pane_pid}\t#{pane_id}\t#{session_name}\t#{window_index}\t#{pane_index}\t#{window_id}\t#{"+homeOpt+"}").Output()
	if err != nil {
		return nil
	}
	var panes []Pane
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 7 || f[6] != "" && f[6] != f[2] {
			continue // a borrowed link: the pane belongs to its home session
		}
		pid, _ := strconv.Atoi(f[0])
		panes = append(panes, Pane{PID: pid, ID: f[1], Session: f[2], Window: f[3], Index: f[4], WindowID: f[5]})
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

// Alone: the pane is the only one in its window.
func (l *Locator) Alone(p Pane) bool {
	n := 0
	for _, o := range l.panes {
		if o.WindowID == p.WindowID {
			n++
		}
	}
	return n == 1
}

// KillWindow closes the window, ending whatever runs in it.
func KillWindow(target string) error {
	_, err := tm("kill-window", "-t", target)
	return err
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

// Term is the terminal app the tmux client runs in, and its XTVERSION reply
// (#{client_termtype}, e.g. "ghostty 1.4.0"), when tmux knows it.
type Term struct {
	App      string
	Termtype string
}

// Kitty reports whether the terminal speaks the kitty graphics protocol.
func (t Term) Kitty() bool {
	for _, name := range []string{"ghostty", "kitty", "wezterm", "konsole"} {
		if strings.EqualFold(t.App, name) || strings.HasPrefix(strings.ToLower(t.Termtype), name) {
			return true
		}
	}
	return false
}

// Terminal is the pinned terminal, else the app behind the most recently
// active tmux client, else Ghostty.
func Terminal() Term {
	if app := config.Get().Terminal(); app != "" {
		return Term{App: app}
	}
	out, _ := exec.Command("tmux", "list-clients", "-F", "#{client_pid}\t#{client_activity}\t#{client_termtype}").Output()
	pid, termtype := latestClient(string(out))
	if pid == 0 {
		return Term{App: "Ghostty"}
	}
	app := NewLocator().Host(pid)
	if app == "" {
		app = "Ghostty"
	}
	return Term{App: app, Termtype: termtype}
}

func latestClient(out string) (pid int, termtype string) {
	latest := -1
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.SplitN(line, "\t", 3)
		if len(f) < 2 {
			continue
		}
		p, _ := strconv.Atoi(f[0])
		act, _ := strconv.Atoi(f[1])
		if p > 0 && act > latest {
			latest, pid, termtype = act, p, ""
			if len(f) == 3 {
				termtype = f[2]
			}
		}
	}
	return pid, termtype
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

// homeOpt tags a window linked into another session with the session it lives in.
const homeOpt = "@claudia_home"

// ActiveSession is the session of the attached client used most recently.
func ActiveSession() (string, bool) {
	out, err := exec.Command("tmux", "list-clients", "-F", "#{client_activity}\t#{session_name}").Output()
	if err != nil {
		return "", false
	}
	best, name := int64(-1), ""
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.SplitN(line, "\t", 2)
		if len(f) != 2 {
			continue
		}
		if t, _ := strconv.ParseInt(f[0], 10, 64); t > best {
			best, name = t, f[1]
		}
	}
	return name, name != ""
}

// Link is a window borrowed into a session other than its home.
type Link struct {
	Session, WindowID, Home string
	Viewed                  bool // the active window of an attached session
}

func (l Link) Target() string { return "=" + l.Session + ":" + l.WindowID }

// Borrow links the window into another session as a background tab.
func Borrow(windowID, home, into string) error {
	if out, err := exec.Command("tmux", "set-option", "-w", "-t", windowID, homeOpt, home).CombinedOutput(); err != nil {
		return &cmdError{string(out), err}
	}
	if out, err := exec.Command("tmux", "link-window", "-d", "-s", windowID, "-t", "="+into+":").CombinedOutput(); err != nil {
		return &cmdError{string(out), err}
	}
	return nil
}

func Borrowed() []Link {
	out, err := exec.Command("tmux", "list-windows", "-a", "-F",
		"#{session_name}\t#{window_id}\t#{window_active}#{session_attached}\t#{"+homeOpt+"}").Output()
	if err != nil {
		return nil
	}
	var links []Link
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 4 || f[3] == "" || f[3] == f[0] {
			continue
		}
		links = append(links, Link{Session: f[0], WindowID: f[1], Home: f[3], Viewed: f[2][0] == '1' && f[2][1:] != "0"})
	}
	return links
}

// Unborrow drops the link; without -k tmux refuses to unlink a window's last link,
// so the home session keeps it.
func Unborrow(l Link) error {
	return exec.Command("tmux", "unlink-window", "-t", l.Target()).Run()
}

type cmdError struct {
	out string
	err error
}

func (e *cmdError) Error() string { return strings.TrimSpace(e.out) + ": " + e.err.Error() }

// Icon marks the windows claudia opens Claude in: Nerd Font's robot (md-robot).
const Icon = "\U000F06A9"

const (
	splitIcon = "\U000F0BCC"  // md-view_split_vertical
	bell      = " \U000F009E" // md-bell_ring, after the name while a session needs you
)

// stripIcon drops a leading icon (a Nerd Font glyph, or the older "✻") and the
// trailing bell, leaving the bare name.
func stripIcon(name string) string {
	name = strings.TrimSuffix(name, bell)
	r := []rune(name)
	if len(r) > 1 && r[1] == ' ' && (r[0] == '✻' || r[0] >= 0xE000 && r[0] <= 0xF8FF || r[0] >= 0xF0000) {
		return string(r[2:])
	}
	return name
}

// MarkWaiting puts the bell on the given windows and takes it off every other
// window that has one, renaming only what changes.
func MarkWaiting(waiting map[string]bool) {
	out, err := tm("list-windows", "-a", "-F", "#{window_id}\t#{window_name}")
	if err != nil {
		return
	}
	for _, line := range strings.Split(out, "\n") {
		id, name, _ := strings.Cut(line, "\t")
		has := strings.HasSuffix(name, bell)
		switch {
		case waiting[id] && !has:
			_, _ = tm("rename-window", "-t", id, name+bell)
		case !waiting[id] && has:
			_, _ = tm("rename-window", "-t", id, strings.TrimSuffix(name, bell))
		}
	}
}

// Retitle swaps the "✻ " older claudia versions put on window names for Icon.
func Retitle() {
	out, err := tm("list-windows", "-a", "-F", "#{window_id}\t#{window_name}")
	if err != nil {
		return
	}
	for _, line := range strings.Split(out, "\n") {
		id, name, _ := strings.Cut(line, "\t")
		if rest, ok := strings.CutPrefix(name, "✻ "); ok {
			_, _ = tm("rename-window", "-t", id, Icon+" "+rest)
		}
	}
}

// SplitSession holds the windows claudia tiles several sessions into.
const SplitSession = "splits"

// Split options remember where a pane came from, so Unsplit can send it back.
const (
	splitHomeOpt = "@claudia_split_home"
	splitNameOpt = "@claudia_split_name"
)

func tm(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		return "", &cmdError{string(out), err}
	}
	return strings.TrimSpace(string(out)), nil
}

// JoinSplit tiles the panes into one window of SplitSession and returns its
// target. Panes already in a split pull the others into that window.
func JoinSplit(panes []Pane) (string, error) {
	win := ""
	for _, p := range panes {
		if p.Session == SplitSession {
			win = p.WindowID
			break
		}
	}
	placeholder := ""
	if win == "" && !HasSession(SplitSession) {
		id, err := tm("new-session", "-d", "-s", SplitSession, "-P", "-F", "#{window_id}")
		if err != nil {
			return "", err
		}
		placeholder = id
	}
	for _, p := range panes {
		if p.Session == SplitSession && p.WindowID == win {
			continue
		}
		name, err := tm("display-message", "-p", "-t", p.ID, "#{window_name}")
		if err != nil {
			return "", err
		}
		// A borrowed tab would point at a window the pane is about to leave.
		for _, l := range Borrowed() {
			if l.WindowID == p.WindowID {
				_ = Unborrow(l)
			}
		}
		_, _ = tm("set-option", "-wu", "-t", p.WindowID, homeOpt)
		home := p.Session
		if home == SplitSession {
			home, _ = tm("show-options", "-pqv", "-t", p.ID, splitHomeOpt)
			name, _ = tm("show-options", "-pqv", "-t", p.ID, splitNameOpt)
		}
		if _, err := tm("set-option", "-p", "-t", p.ID, splitHomeOpt, home); err != nil {
			return "", err
		}
		if _, err := tm("set-option", "-p", "-t", p.ID, splitNameOpt, name); err != nil {
			return "", err
		}
		if win == "" {
			win, err = tm("break-pane", "-d", "-s", p.ID, "-t", "="+SplitSession+":", "-n", splitIcon+" split", "-P", "-F", "#{window_id}")
		} else {
			_, err = tm("join-pane", "-d", "-s", p.ID, "-t", win)
		}
		if err != nil {
			return "", err
		}
	}
	if placeholder != "" {
		_, _ = tm("kill-window", "-t", placeholder)
	}
	if win == "" {
		return "", errors.New("nothing to split")
	}
	// Also on a re-split: panes already in the window may come from an older layout.
	_, _ = tm("select-layout", "-t", win, "even-horizontal")
	// Name the tab after what's in it, so the status bar says which sessions are
	// tiled. Labels keep each session's icon; the old "✻ " and the bell go.
	if out, err := tm("list-panes", "-t", win, "-F", "#{pane_id}\t#{"+splitNameOpt+"}"); err == nil {
		var short []string
		for _, line := range strings.Split(out, "\n") {
			id, n, _ := strings.Cut(line, "\t")
			n = strings.TrimSuffix(n, bell)
			if rest, ok := strings.CutPrefix(n, "✻ "); ok {
				n = Icon + " " + rest
			}
			short = append(short, stripIcon(n))
			_, _ = tm("set-option", "-p", "-t", id, splitNameOpt, n)
		}
		_, _ = tm("rename-window", "-t", win, splitIcon+" "+strings.Join(short, " │ "))
	}
	_, _ = tm("set-option", "-w", "-t", win, "pane-border-status", "top")
	_, _ = tm("set-option", "-w", "-t", win, "pane-border-format", " #{"+splitNameOpt+"} ")
	return "=" + SplitSession + ":" + win, nil
}

// Unsplit sends every pane of the split window back to its own window in the
// session it came from, or keeps it as a window of its own when that's gone.
func Unsplit(windowID string) error {
	out, err := tm("list-panes", "-t", windowID, "-F", "#{pane_id}\t#{"+splitHomeOpt+"}\t#{"+splitNameOpt+"}")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(out, "\n") {
		// tm trims the output, which also eats the last pane's empty trailing fields.
		f := append(strings.Split(line, "\t"), "", "")
		dest := f[1]
		if dest == "" {
			dest = SplitSession
		}
		// A session whose only window went into the split ended with it.
		placeholder := ""
		if !HasSession(dest) {
			if placeholder, err = tm("new-session", "-d", "-s", dest, "-P", "-F", "#{window_id}"); err != nil {
				return err
			}
		}
		args := []string{"break-pane", "-d", "-s", f[0], "-t", "=" + dest + ":"}
		if f[2] != "" {
			args = append(args, "-n", f[2])
		}
		if _, err := tm(args...); err != nil {
			return err
		}
		if placeholder != "" {
			_, _ = tm("kill-window", "-t", placeholder)
		}
		_, _ = tm("set-option", "-pu", "-t", f[0], splitHomeOpt)
		_, _ = tm("set-option", "-pu", "-t", f[0], splitNameOpt)
	}
	return nil
}

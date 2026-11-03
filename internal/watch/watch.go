// Package watch is the headless poller behind `claudia watch`: it keeps the
// caches fresh for the popup and sends macOS notifications for things that need you.
package watch

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/brief"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/state"
	"github.com/MartyJRE/claudia/internal/tmux"
	"github.com/MartyJRE/claudia/internal/update"
)

const pidFile = "watch.pid"

// Running reports whether a watcher process is alive.
func Running() bool {
	b, err := os.ReadFile(state.Path(pidFile))
	if err != nil {
		return false
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return pid > 0 && syscall.Kill(pid, 0) == nil
}

// Stop ends a running watcher, so the next claudia start runs a fresh one.
func Stop() {
	b, err := os.ReadFile(state.Path(pidFile))
	if err != nil {
		return
	}
	if pid, _ := strconv.Atoi(strings.TrimSpace(string(b))); pid > 0 {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
}

// Spawn starts `claudia watch` detached, inheriting this environment (and so the
// Atlassian token), unless one is already running.
func Spawn() {
	if Running() {
		return
	}
	self, err := os.Executable()
	if err != nil {
		return
	}
	_ = os.MkdirAll(state.Dir(), 0o755)
	log, err := os.OpenFile(state.Path("watch.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	c := exec.Command(self, "watch")
	c.Stdout, c.Stderr = log, log
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if c.Start() == nil {
		_ = c.Process.Release()
	}
	log.Close()
}

type watcher struct {
	store *claude.Store
	atl   *atlassian.Client

	status   map[string]claude.Status
	reviews  map[string]bool
	pipeline map[string]string
	approved map[string]bool
	mentions map[string]bool
	baseline [3]bool
}

// Run polls until killed. makeBrief writes the inbox brief; it runs on the
// configured interval and whenever the TUI asks for one.
func Run(makeBrief func()) error {
	if Running() {
		return fmt.Errorf("already running")
	}
	if err := state.Save(pidFile, os.Getpid()); err != nil {
		return err
	}
	defer os.Remove(state.Path(pidFile))
	tmux.Retitle()

	w := &watcher{
		store:    claude.NewStore(claude.DefaultProfiles(), time.Hour),
		status:   map[string]claude.Status{},
		reviews:  map[string]bool{},
		pipeline: map[string]string{},
		approved: map[string]bool{},
		mentions: map[string]bool{},
	}
	w.atl, _ = atlassian.New()

	var lastGL, lastJira time.Time
	lastBrief := time.Now()
	if b, ok := brief.Load(); ok {
		lastBrief = b.At
	}
	briefing := make(chan struct{}, 1)
	for {
		if _, ok := update.Current(); ok && update.Due(time.Hour) {
			go update.Check()
		}
		every := config.Get().BriefEvery
		due := every > 0 && time.Since(lastBrief) >= every
		if (brief.TakeRequest() || due) && len(briefing) == 0 {
			lastBrief = time.Now()
			briefing <- struct{}{}
			go func() {
				defer func() { <-briefing }()
				makeBrief()
			}()
		}
		w.sessions()
		if time.Since(lastGL) >= time.Minute {
			lastGL = time.Now()
			if s, err := gitlab.Fetch(); err == nil {
				_ = state.Save("cache-gitlab.json", s)
				w.gitlab(s)
			} else {
				fmt.Fprintln(os.Stderr, time.Now().Format(time.TimeOnly), err)
			}
		}
		if w.atl != nil && time.Since(lastJira) >= 2*time.Minute {
			lastJira = time.Now()
			if s, err := w.atl.FetchJira(); err == nil {
				_ = state.Save("cache-jira.json", s)
				w.jira(s)
			} else {
				fmt.Fprintln(os.Stderr, time.Now().Format(time.TimeOnly), err)
			}
		}
		time.Sleep(3 * time.Second)
	}
}

func (w *watcher) sessions() {
	loc := tmux.NewLocator()
	first := !w.baseline[0]
	w.baseline[0] = true
	seen := map[string]bool{}
	waiting := map[string]bool{} // window ids with a waiting Claude
	for _, s := range w.store.Sessions() {
		if s.Live == nil {
			continue
		}
		seen[s.ID] = true
		prev, had := w.status[s.ID]
		w.status[s.ID] = s.Live.Status
		p, inTmux := loc.Pane(s.Live.PID)
		if inTmux && s.Live.Status == claude.StatusWaiting {
			waiting[p.WindowID] = true
		}
		if first || !had || prev == s.Live.Status || s.Live.Status != claude.StatusWaiting {
			continue
		}
		n := note{title: "Claude needs you", subtitle: s.Name(), body: filepath.Base(s.Dir()), thread: "claude", id: "claude:" + s.ID}
		if s.Transcript != nil && s.LastPrompt != "" {
			n.body += " · " + oneLine(s.LastPrompt)
		}
		if inTmux {
			n.target = p.Target() + "." + p.Index
			if looking(n.target) || lookingAtBorrowed(p.WindowID) {
				continue
			}
			n.borrowed = borrow(p)
		}
		notify(n)
	}
	for id := range w.status {
		if !seen[id] {
			delete(w.status, id)
		}
	}
	for _, l := range tmux.Borrowed() {
		if !waiting[l.WindowID] && !l.Viewed {
			_ = tmux.Unborrow(l)
		}
	}
}

// borrow links a waiting session's window into the session you're in, so it's a
// tab away; the cleanup in sessions() unlinks it once answered and left. It
// returns the tab's target, or "" when nothing was linked.
func borrow(p tmux.Pane) string {
	into, ok := tmux.ActiveSession()
	if !ok || into == tmux.Home || into == p.Session {
		return ""
	}
	l := tmux.Link{Session: into, WindowID: p.WindowID}
	for _, b := range tmux.Borrowed() {
		if b.Session == into && b.WindowID == p.WindowID {
			return l.Target()
		}
	}
	if err := tmux.Borrow(p.WindowID, p.Session, into); err != nil {
		fmt.Fprintln(os.Stderr, time.Now().Format(time.TimeOnly), "borrow:", err)
		return ""
	}
	return l.Target()
}

func (w *watcher) gitlab(s gitlab.Snapshot) {
	first := !w.baseline[1]
	w.baseline[1] = true
	for _, m := range s.Review {
		if !w.reviews[m.WebURL] && !first && !m.Draft {
			notify(note{title: m.Author.Username + " asks for your review", subtitle: fmt.Sprintf("%s !%d", m.Repo(), m.IID), body: m.Title, thread: "review", id: "review:" + m.WebURL, url: m.WebURL})
		}
		w.reviews[m.WebURL] = true
	}
	for _, m := range s.Mine {
		st := m.PipelineStatus()
		if !first && st == "failed" && w.pipeline[m.WebURL] != "failed" {
			notify(note{title: "Pipeline failed", subtitle: fmt.Sprintf("%s !%d", m.Repo(), m.IID), body: m.Title, thread: "pipeline", id: "pipeline:" + m.WebURL, url: m.Pipeline.WebURL})
		}
		if !first && m.Approved && !w.approved[m.WebURL] {
			notify(note{title: strings.Join(m.ApprovedBy, ", ") + " approved your MR", subtitle: fmt.Sprintf("%s !%d", m.Repo(), m.IID), body: m.Title, thread: "approved", id: "approved:" + m.WebURL, url: m.WebURL})
		}
		w.pipeline[m.WebURL] = st
		w.approved[m.WebURL] = m.Approved
	}
	for _, m := range s.Merged {
		if m.MergePipeline == nil {
			continue
		}
		key := "merge:" + m.WebURL
		st := m.MergePipeline.Status
		if !first && st == "failed" && w.pipeline[key] != "failed" {
			notify(note{title: "Pipeline failed after merge", subtitle: fmt.Sprintf("%s !%d", m.Repo(), m.IID), body: m.Title, thread: "pipeline", id: "merge:" + m.WebURL, url: m.MergePipeline.WebURL})
		}
		w.pipeline[key] = st
	}
}

func (w *watcher) jira(s atlassian.JiraSnapshot) {
	first := !w.baseline[2]
	w.baseline[2] = true
	for _, m := range s.Mentions {
		if !w.mentions[m.CommentID] && !first && !m.Answered {
			notify(note{title: m.Author + " mentioned you", subtitle: m.Key + " " + m.Summary, body: m.Text, thread: "mention", id: "mention:" + m.CommentID, url: m.URL})
		}
		w.mentions[m.CommentID] = true
	}
}

type note struct {
	title, subtitle, body string
	thread                string // groups notifications of one kind in Notification Center
	id                    string // a newer notification with the same id replaces the old one
	target                string // tmux pane to jump to on click
	borrowed              string // or first the tab it was linked into in your session
	url                   string // or a link to open
}

// notifier is the Claudia.app helper built by notifier/build.sh.
func Notifier() string {
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, "Applications", "Claudia.app", "Contents", "MacOS", "claudia-notify")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// clickCommand jumps to the pane and brings Ghostty forward. It runs from the
// helper app with launchd's minimal PATH, hence the absolute tmux path.
func clickCommand(n note) string {
	tm, err := exec.LookPath("tmux")
	if err != nil {
		tm = "tmux"
	}
	jump := fmt.Sprintf("%q switch-client -t %q", tm, n.target)
	if n.borrowed != "" {
		jump = fmt.Sprintf("%q switch-client -t %q || %s", tm, n.borrowed, jump)
	}
	return jump + "; open -a Ghostty"
}

// looking reports whether the pane is the one on screen, so a session you are
// watching doesn't also ping you.
func looking(target string) bool {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", target, "#{pane_active}#{window_active}#{session_attached}").Output()
	st := strings.TrimSpace(string(out))
	if err != nil || !strings.HasPrefix(st, "11") || st == "110" {
		return false
	}
	return ghosttyInFront()
}

// lookingAtBorrowed: the window is on screen as a tab borrowed into your session.
func lookingAtBorrowed(windowID string) bool {
	for _, l := range tmux.Borrowed() {
		if l.WindowID == windowID && l.Viewed {
			return ghosttyInFront()
		}
	}
	return false
}

func ghosttyInFront() bool {
	asn, _ := exec.Command("lsappinfo", "front").Output()
	front, _ := exec.Command("lsappinfo", "info", "-only", "name", strings.TrimSpace(string(asn))).Output()
	return strings.HasPrefix(string(front), `"Ghostty"`)
}

// notify is a var so tests can catch notifications instead of posting them.
var notify = post

func post(n note) {
	if bin := Notifier(); bin != "" {
		args := []string{"post", "--title", n.title, "--subtitle", n.subtitle, "--body", n.body, "--thread", n.thread, "--id", n.id}
		switch {
		case n.target != "":
			args = append(args, "--exec", clickCommand(n))
		case n.url != "":
			args = append(args, "--url", n.url)
		}
		if out, err := exec.Command(bin, args...).CombinedOutput(); err == nil {
			return
		} else {
			fmt.Fprintln(os.Stderr, time.Now().Format(time.TimeOnly), "notifier:", strings.TrimSpace(string(out)), err)
		}
	}
	if bin, err := exec.LookPath("terminal-notifier"); err == nil {
		args := []string{"-title", n.title, "-subtitle", n.subtitle, "-message", n.body, "-group", n.id}
		switch {
		case n.target != "":
			args = append(args, "-execute", clickCommand(n))
		case n.url != "":
			args = append(args, "-open", n.url)
		}
		_ = exec.Command(bin, args...).Run()
		return
	}
	script := fmt.Sprintf("display notification %q with title %q subtitle %q", n.body, n.title, n.subtitle)
	_ = exec.Command("osascript", "-e", script).Run()
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

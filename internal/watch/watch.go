// Package watch is the headless poller behind `claudia watch`: it keeps the
// caches fresh for the popup and sends macOS notifications for things that need you.
package watch

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/brief"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/launch"
	"github.com/MartyJRE/claudia/internal/runs"
	"github.com/MartyJRE/claudia/internal/state"
	"github.com/MartyJRE/claudia/internal/summary"
	"github.com/MartyJRE/claudia/internal/tmux"
	"github.com/MartyJRE/claudia/internal/update"
)

const (
	pidFile  = "watch.pid"
	lockFile = "watch.lock"
)

// Seams for tests.
var (
	executable   = os.Executable
	execCommand  = exec.Command // lsappinfo, which only macOS has
	currentBuild = update.Current
	checkUpdate  = update.Check
	pollEvery    = 3 * time.Second
	stopRun      chan struct{} // closing it ends Run; nil never does
	openRuns     = runs.Open
)

// Running reports whether a watcher holds the lock. A pid alone can't say: a
// watcher that died under the TUI that spawned it lingers as a zombie, and
// kill(pid, 0) still succeeds on it. The kernel drops the lock on exit.
func Running() bool {
	f, err := os.Open(state.Path(lockFile))
	if err != nil {
		return false
	}
	defer f.Close()
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil
}

// lock takes the watcher lock for as long as the returned file stays open.
func lock() (*os.File, error) {
	if err := os.MkdirAll(state.Dir(), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(state.Path(lockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		f.Close()
		return nil, fmt.Errorf("already running")
	}
	return f, nil
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
	self, err := executable()
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
		go c.Wait()
	}
	log.Close()
}

type watcher struct {
	store *claude.Store
	atl   *atlassian.Client

	status   map[string]claude.Status
	stuck    map[string]bool // sessions told about as stuck at startup
	reviews  map[string]bool
	pipeline map[string]string
	approved map[string]bool
	mentions map[string]bool
	baseline [3]bool

	summaries   map[string]summary.Summary
	summarizing chan struct{}
	summaryMu   sync.Mutex

	runs      *runs.Store
	settledAt time.Time

	gl    gitlab.Snapshot // the latest fetches, for the autopilot
	js    atlassian.JiraSnapshot
	pilot *pilot
}

var summarize = summary.Generate

// summarize writes a fresh summary for a stopped session that moved on, one
// at a time; a failure keeps the last good text and waits for the next turn.
func (w *watcher) summarize(s claude.Session) {
	w.summaryMu.Lock()
	stale := summary.Stale(s, w.summaries)
	w.summaryMu.Unlock()
	if !stale {
		return
	}
	select {
	case w.summarizing <- struct{}{}:
	default:
		return
	}
	go func() {
		defer func() { <-w.summarizing }()
		sum := summarize(s)
		w.summaryMu.Lock()
		if sum.Err != "" {
			sum.Text = w.summaries[s.ID].Text
			fmt.Fprintln(os.Stderr, time.Now().Format(time.TimeOnly), "summary:", sum.Err)
		}
		w.summaries[s.ID] = sum
		w.summaryMu.Unlock()
		_ = summary.Put(s.ID, sum)
	}()
}

// Run polls until killed. makeBrief writes the inbox brief; it runs on the
// configured interval and whenever the TUI asks for one.
func Run(makeBrief func()) error {
	l, err := lock()
	if err != nil {
		return err
	}
	defer l.Close()
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(term)
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

		summaries:   summary.Load(),
		summarizing: make(chan struct{}, 1),
	}
	w.atl, _ = atlassian.New()
	if w.runs, err = openRuns(); err == nil {
		defer w.runs.Close()
		w.startPilot(claude.DefaultProfiles())
		state.Load("cache-gitlab.json", &w.gl)
		state.Load("cache-jira.json", &w.js)
	} else {
		fmt.Fprintln(os.Stderr, time.Now().Format(time.TimeOnly), "runs:", err)
	}

	var lastGL, lastJira time.Time
	lastBrief := time.Now()
	if b, ok := brief.Load(); ok {
		lastBrief = b.At
	}
	briefing := make(chan struct{}, 1)
	for {
		if _, ok := currentBuild(); ok && update.Due(time.Hour) {
			go checkUpdate()
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
		w.autopilot(time.Now())
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
		select {
		case <-stopRun:
			return nil
		case <-term:
			return nil
		case <-time.After(pollEvery):
		}
	}
}

func (w *watcher) sessions() {
	loc := tmux.NewLocator()
	first := !w.baseline[0]
	w.baseline[0] = true
	seen := map[string]bool{}
	waiting := map[string]bool{} // window ids with a waiting Claude
	var ran []runs.Seen
	stopped := first
	all := w.store.Sessions()
	for _, s := range all {
		w.summaryMu.Lock()
		sum := w.summaries[s.ID]
		w.summaryMu.Unlock()
		ran = append(ran, runs.SeenOf(s, sum.Text, sum.For))
		if s.Live == nil || s.Live.Worker != "" {
			continue
		}
		seen[s.ID] = true
		prev, had := w.status[s.ID]
		w.status[s.ID] = s.Live.Status
		stopped = stopped || had && prev.Working() && !s.Live.Status.Working()
		if !s.Live.Status.Working() && w.summarizing != nil {
			w.summarize(s)
		}
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
			stopped = true
		}
	}
	w.settle(ran, stopped)
	w.contextNotes(all, loc)
	w.stuckNotes(all, processSessions(), launch.Kinds(), loc)
	for _, l := range tmux.Borrowed() {
		if !waiting[l.WindowID] && !l.Viewed {
			_ = tmux.Unborrow(l)
		}
	}
	tmux.MarkWaiting(waiting)
}

// settle reads how runs went when a session stopped or exited, so the run log
// stays current with the TUI closed; once a minute it also catches a missed edge.
func (w *watcher) settle(seen []runs.Seen, stopped bool) {
	if w.runs == nil || !stopped && time.Since(w.settledAt) < time.Minute {
		return
	}
	w.settledAt = time.Now()
	if _, err := w.runs.Reconcile(runs.WithStarting(seen, claude.ProcessSessions()), w.settledAt); err != nil {
		fmt.Fprintln(os.Stderr, w.settledAt.Format(time.TimeOnly), "runs:", err)
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
	w.gl = s
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
	w.js = s
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

// clickCommand jumps to the pane and brings the terminal forward. It runs from
// the helper app with launchd's minimal PATH, hence the absolute tmux path.
func clickCommand(n note) string {
	tm, err := exec.LookPath("tmux")
	if err != nil {
		tm = "tmux"
	}
	jump := fmt.Sprintf("%q switch-client -t %q", tm, n.target)
	if n.borrowed != "" {
		jump = fmt.Sprintf("%q switch-client -t %q || %s", tm, n.borrowed, jump)
	}
	return jump + "; open -a " + shellQuote(terminal().App)
}

// looking reports whether the pane is the one on screen, so a session you are
// watching doesn't also ping you.
func looking(target string) bool {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", target, "#{pane_active}#{window_active}#{session_attached}").Output()
	st := strings.TrimSpace(string(out))
	if err != nil || !strings.HasPrefix(st, "11") || st == "110" {
		return false
	}
	return terminalInFront()
}

// lookingAtBorrowed: the window is on screen as a tab borrowed into your session.
func lookingAtBorrowed(windowID string) bool {
	for _, l := range tmux.Borrowed() {
		if l.WindowID == windowID && l.Viewed {
			return terminalInFront()
		}
	}
	return false
}

// terminal is a var so tests don't depend on the terminal they run in.
var terminal = tmux.Terminal

// terminalInFront matches the front app by name, or by bundle for apps whose
// process name differs from it (iTerm.app runs as "iTerm2").
func terminalInFront() bool {
	app := terminal().App
	asn, _ := execCommand("lsappinfo", "front").Output()
	front, _ := execCommand("lsappinfo", "info", "-only", "name", strings.TrimSpace(string(asn))).Output()
	if strings.HasPrefix(string(front), `"`+app+`"`) {
		return true
	}
	bundle, _ := execCommand("lsappinfo", "info", "-only", "bundlepath", strings.TrimSpace(string(asn))).Output()
	return strings.Contains(string(bundle), "/"+app+`.app"`)
}

// shellQuote leaves plain names bare, so the Ghostty command reads as before.
func shellQuote(s string) string {
	if strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_')
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
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

var processSessions = claude.ProcessSessions

// stuckNotes tells once of each session claudia started whose claude runs but
// never registered, most likely held by a dialog nobody sees in its window.
func (w *watcher) stuckNotes(all []claude.Session, procs map[string]claude.Proc, kinds map[string]string, loc *tmux.Locator) {
	if w.stuck == nil {
		w.stuck = map[string]bool{}
	}
	known := map[string]bool{}
	for _, s := range all {
		known[s.ID] = true
	}
	for id, p := range procs {
		if known[id] || kinds[id] == "" || p.Age < runs.Grace || w.stuck[id] {
			continue
		}
		w.stuck[id] = true
		n := note{title: "Claude is stuck at startup", subtitle: p.Name, body: "a prompt in its window waits for you",
			thread: "claude", id: "stuck:" + id}
		if pane, ok := loc.Pane(p.PID); ok {
			n.target = pane.Target() + "." + pane.Index
		}
		notify(n)
	}
}

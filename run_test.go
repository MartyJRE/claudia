package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MartyJRE/claudia/internal/brief"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/state"
	"github.com/MartyJRE/claudia/internal/tmux"
	"github.com/MartyJRE/claudia/internal/ui"
	"github.com/MartyJRE/claudia/internal/update"
)

type exited int

type execed struct{}

type result struct {
	code     int      // -1 when run returned
	exec     []string // argv0, then argv
	out, err string
}

func swap[T any](t *testing.T, p *T, v T) {
	old := *p
	*p = v
	t.Cleanup(func() { *p = old })
}

// claudia runs a command line with exit and exec caught, and its output captured.
func claudia(t *testing.T, args ...string) (r result) {
	t.Helper()
	r.code = -1
	swap(t, &exit, func(code int) { panic(exited(code)) })
	swap(t, &syscallExec, func(argv0 string, argv []string, _ []string) error {
		r.exec = append([]string{argv0}, argv...)
		panic(execed{})
	})
	outR, outW, _ := os.Pipe()
	errR, errW, _ := os.Pipe()
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	read := func(f *os.File, dst *string, done chan struct{}) {
		b, _ := io.ReadAll(f)
		*dst = string(b)
		close(done)
	}
	outDone, errDone := make(chan struct{}), make(chan struct{})
	go read(outR, &r.out, outDone)
	go read(errR, &r.err, errDone)
	func() {
		defer func() {
			switch v := recover().(type) {
			case nil, execed:
			case exited:
				r.code = int(v)
			default:
				panic(v)
			}
		}()
		run(args)
	}()
	os.Stdout, os.Stderr = oldOut, oldErr
	outW.Close()
	errW.Close()
	<-outDone
	<-errDone
	return r
}

// isolate gives the test its own home, state and tmux server, outside tmux.
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("TMUX_PANE", "")
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

func tmuxOut(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %v: %s %v", args, out, err)
	}
	return strings.TrimSpace(string(out))
}

// inside starts the private server with a session and points $TMUX at it, as
// if claudia ran in one of its panes.
func inside(t *testing.T, session string) tmux.Pane {
	t.Helper()
	tmuxOut(t, "new-session", "-d", "-s", session, "sleep 60")
	sock := tmuxOut(t, "display-message", "-p", "#{socket_path}")
	t.Setenv("TMUX", sock+",1,0")
	for _, p := range tmux.Panes() {
		if p.Session == session {
			return p
		}
	}
	t.Fatal("no pane")
	return tmux.Pane{}
}

// attach attaches a control-mode client, which tmux counts like a terminal.
func attach(t *testing.T, session string) {
	t.Helper()
	c := exec.Command("tmux", "-C", "attach", "-t", "="+session)
	stdin, err := c.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdin.Close(); _ = c.Wait() })
	for range 1000 {
		if s, ok := tmux.ActiveSession(); ok && s == session {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("control client never attached")
}

// fakeSelf stands in for the claudia binary that home starts in tmux.
func fakeSelf(t *testing.T) string {
	self := filepath.Join(t.TempDir(), "claudia")
	os.WriteFile(self, []byte("#!/bin/sh\nexec sleep 60\n"), 0o755)
	swap(t, &executable, func() (string, error) { return self, nil })
	return self
}

func TestMainRunsArgs(t *testing.T) {
	swap(t, &os.Args, []string{"claudia", "version"})
	out := stdout(t, main)
	if out != "unknown (local build)\n" {
		t.Errorf("got %q", out)
	}
}

func TestVersionSources(t *testing.T) {
	swap(t, &currentBuild, func() (string, bool) { return "1133a71d4dc3", true })
	if r := claudia(t, "version"); r.out != "1133a71d4dc3\n" || r.code != -1 {
		t.Errorf("proxy build %+v", r)
	}
	currentBuild = func() (string, bool) { return "", false }
	swap(t, &buildInfo, func() (*debug.BuildInfo, bool) { return nil, false })
	if v := version(); v != "unknown" {
		t.Errorf("no build info: %q", v)
	}
	buildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "9344af4d9f7a0123456789"},
			{Key: "vcs.modified", Value: "true"},
		}}, true
	}
	if v := version(); v != "9344af4d9f7a+changes (local build)" {
		t.Errorf("dirty checkout: %q", v)
	}
	buildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "short"}, {Key: "vcs.modified", Value: "false"}}}, true
	}
	if v := version(); v != "unknown (local build)" {
		t.Errorf("short revision: %q", v)
	}
}

func TestKeys(t *testing.T) {
	if r := claudia(t, "keys"); r.out != ui.KeyActions() || r.code != -1 {
		t.Errorf("got %+v", r)
	}
}

func TestImg(t *testing.T) {
	if r := claudia(t, "img"); r.code != -1 || r.out != "" {
		t.Errorf("no files: %+v", r)
	}
	null, _ := os.Open(os.DevNull)
	defer null.Close()
	swap(t, &os.Stdin, null)
	if r := claudia(t, "img", "a.png"); r.code != 1 || !strings.HasPrefix(r.err, "claudia: ") {
		t.Errorf("without a terminal: %+v", r)
	}
}

func TestCtlCommand(t *testing.T) {
	if r := claudia(t, "ctl", "help"); r.out != ctlHelp || r.code != -1 {
		t.Errorf("help %+v", r)
	}
	if r := claudia(t, "ctl", "nope"); r.code != 1 || !strings.HasPrefix(r.err, `claudia: unknown command "nope"`) {
		t.Errorf("unknown %+v", r)
	}
}

func TestOpenOutsideTmux(t *testing.T) {
	isolate(t)
	swap(t, &executable, func() (string, error) { return "/opt/claudia", nil })
	swap(t, &lookPath, func(string) (string, error) { return "/bin/tmux", nil })
	r := claudia(t, "open", "https://gitlab.example.com/a/b/-/merge_requests/1")
	want := []string{"/bin/tmux", "tmux", "new-session", "-A", "-s", "claudia", `"/opt/claudia" ui`}
	if !slices.Equal(r.exec, want) {
		t.Errorf("exec %q", r.exec)
	}
	var req map[string]string
	if !state.Load("open-request.json", &req) || req["link"] != "https://gitlab.example.com/a/b/-/merge_requests/1" {
		t.Errorf("request %v", req)
	}
}

func TestOpenCantSaveRequest(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	os.WriteFile(filepath.Join(dir, "claudia"), nil, 0o600)
	if r := claudia(t, "open", "x"); r.code != 1 || r.exec != nil {
		t.Errorf("got %+v", r)
	}
}

func TestHomeOutsideTmux(t *testing.T) {
	isolate(t)
	swap(t, &executable, func() (string, error) { return "/opt/claudia", nil })
	swap(t, &lookPath, func(string) (string, error) { return "", errors.New("no tmux") })
	if r := claudia(t); r.code != 1 || r.err != "claudia: no tmux\n" || r.exec != nil {
		t.Errorf("without tmux: %+v", r)
	}
	executable = func() (string, error) { return "", errors.New("no binary") }
	if r := claudia(t); r.code != 1 || !strings.HasPrefix(r.err, "claudia: no binary\n") {
		t.Errorf("without binary: %+v", r)
	}
}

func TestHomeInsideTmux(t *testing.T) {
	isolate(t)
	inside(t, "pay")
	self := fakeSelf(t)
	// No claudia session yet and no client to switch: the session starts, the switch fails.
	r := claudia(t)
	if r.code != 1 || !strings.HasPrefix(r.err, "claudia: ") {
		t.Errorf("without a client %+v", r)
	}
	if cmd := tmuxOut(t, "display-message", "-p", "-t", "=claudia:", "#{pane_start_command}"); !strings.Contains(cmd, self) || !strings.Contains(cmd, " ui") {
		t.Errorf("claudia session runs %q", cmd)
	}
	attach(t, "pay")
	if r := claudia(t); r.code != -1 || r.err != "" {
		t.Errorf("with a client %+v", r)
	}
	if s := tmuxOut(t, "list-clients", "-F", "#{session_name}"); s != "claudia" {
		t.Errorf("client is on %q", s)
	}
}

func TestHomeCantStartSession(t *testing.T) {
	isolate(t)
	inside(t, "pay")
	fakeSelf(t)
	var ran []string
	swap(t, &execCommand, func(name string, args ...string) *exec.Cmd {
		ran = append([]string{name}, args...)
		return exec.Command("false")
	})
	if r := claudia(t); r.code != 1 || len(ran) < 5 || ran[1] != "new-session" || tmux.HasSession("claudia") {
		t.Errorf("got %+v, ran %q", r, ran)
	}
}

// fakeClaude puts a claude on PATH that runs script.
func fakeClaude(t *testing.T, script string) string {
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"+script), 0o755)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	return bin
}

func TestBrief(t *testing.T) {
	isolate(t)
	bin := fakeClaude(t, `cat > /dev/null; echo "$CLAUDE_CONFIG_DIR" > "$(dirname "$0")/env"; printf '  All quiet.\n'`)
	r := claudia(t, "brief")
	if r.code != -1 || r.out != "All quiet.\n" {
		t.Fatalf("got %+v", r)
	}
	if env, _ := os.ReadFile(filepath.Join(bin, "env")); strings.TrimSpace(string(env)) != filepath.Join(os.Getenv("HOME"), ".claude") {
		t.Errorf("profile %q", env)
	}
	if b, ok := brief.Load(); !ok || b.Text != "All quiet." || b.Err != "" {
		t.Errorf("saved %+v", b)
	}

	fakeClaude(t, `cat > /dev/null; echo "not logged in" >&2; exit 1`)
	r = claudia(t, "brief")
	if r.code != 1 || !strings.Contains(r.err, "brief: not logged in\n") || !strings.HasSuffix(r.err, "claudia: not logged in\n") {
		t.Errorf("failing %+v", r)
	}
	if b, _ := brief.Load(); b.Text != "All quiet." || b.Err != "not logged in" {
		t.Errorf("a failure must keep the last text: %+v", b)
	}
}

func TestUpdate(t *testing.T) {
	isolate(t)
	// The running watcher is stopped so the next start runs the new binary.
	child := exec.Command("sleep", "60")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill() })
	state.Save("watch.pid", child.Process.Pid)
	swap(t, &fetchUpdate, func() (update.Plan, error) {
		return update.Plan{Latest: "bbbbbbb", Commits: []update.Commit{{SHA: "bbbbbbb", Subject: "ui: new"}, {SHA: "aaaaaaa", Subject: "ci: old"}}}, nil
	})
	swap(t, &installLatest, func(line func(string)) error {
		line("get github.com/MartyJRE/claudia@latest")
		line("github.com/MartyJRE/claudia")
		return nil
	})
	r := claudia(t, "update")
	want := "updating to bbbbbbb, what's new:\n  bbbbbbb  ui: new\n  aaaaaaa  ci: old\n" +
		"downloading and building…\n  github.com/MartyJRE/claudia\n" +
		"done; restart claudia to use it (ctrl+u inside claudia restarts by itself)\n"
	if r.code != -1 || r.out != want {
		t.Errorf("got %+v\nwant %q", r, want)
	}
	err := child.Wait()
	if ws, ok := child.ProcessState.Sys().(syscall.WaitStatus); err == nil || !ok || ws.Signal() != syscall.SIGTERM {
		t.Errorf("watcher not stopped: %v", err)
	}

	fetchUpdate = func() (update.Plan, error) {
		return update.Plan{Latest: "v0.2.0", Releases: []update.Release{{Tag: "v0.2.0", Body: "## ui\r\n- pills\n", Published: time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)}}}, nil
	}
	if r := claudia(t, "update"); !strings.HasPrefix(r.out, "updating to v0.2.0, what's new:\n  v0.2.0  2026-11-10\n    ## ui\n    - pills\ndownloading") {
		t.Errorf("release notes %+v", r)
	}
	fetchUpdate = func() (update.Plan, error) { return update.Plan{Latest: "1133a71"}, nil }
	if r := claudia(t, "update"); !strings.HasPrefix(r.out, "already up to date; reinstalling 1133a71\ndownloading") {
		t.Errorf("up to date %+v", r)
	}
	fetchUpdate = func() (update.Plan, error) { return update.Plan{}, errors.New("rate limited") }
	installLatest = func(func(string)) error { return errors.New("exit status 1") }
	r = claudia(t, "update")
	if r.code != 1 || !strings.HasPrefix(r.out, "couldn't ask GitHub what's new: rate limited\n") || r.err != "claudia: exit status 1\n" {
		t.Errorf("failing %+v", r)
	}
}

func TestSetupAndWatch(t *testing.T) {
	setupErr := errors.New("needs xcrun")
	swap(t, &runSetup, func() error { return setupErr })
	if r := claudia(t, "setup"); r.code != 1 || r.err != "claudia: needs xcrun\n" {
		t.Errorf("setup %+v", r)
	}
	setupErr = nil
	if r := claudia(t, "setup"); r.code != -1 {
		t.Errorf("setup %+v", r)
	}
	var got func()
	watchErr := errors.New("already running")
	swap(t, &runWatch, func(makeBrief func()) error { got = makeBrief; return watchErr })
	if r := claudia(t, "watch"); r.code != 1 || r.err != "claudia: already running\n" || got == nil {
		t.Errorf("watch %+v", r)
	}
	watchErr = nil
	if r := claudia(t, "watch"); r.code != -1 {
		t.Errorf("watch %+v", r)
	}
}

type quit struct{}

func (quit) Init() tea.Cmd                       { return tea.Quit }
func (quit) Update(tea.Msg) (tea.Model, tea.Cmd) { return quit{}, tea.Quit }
func (quit) View() string                        { return "" }

// tui stands in for the TUI: it ends at once, as if the user left with res.
func tui(t *testing.T, res ui.Result, ctx context.Context) {
	swap(t, &newProgram, func(m tea.Model, _ ...tea.ProgramOption) *tea.Program {
		m.(*ui.Model).Result = res
		return tea.NewProgram(quit{}, tea.WithContext(ctx), tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignalHandler())
	})
}

func TestTUI(t *testing.T) {
	isolate(t)
	// A watcher counts as running, so none is spawned.
	state.Save("watch.pid", os.Getpid())
	swap(t, &executable, func() (string, error) { return "/opt/claudia", nil })
	swap(t, &lookPath, func(string) (string, error) { return "/bin/tmux", nil })
	ctx := context.Background()

	tui(t, ui.Result{}, ctx)
	if r := claudia(t, "ui"); r.code != -1 || r.exec != nil {
		t.Errorf("plain exit %+v", r)
	}
	tui(t, ui.Result{Restart: true}, ctx)
	if r := claudia(t, "popup"); !slices.Equal(r.exec, append([]string{"/opt/claudia"}, os.Args...)) {
		t.Errorf("restart exec %q", r.exec)
	}
	tui(t, ui.Result{Attach: "pay:1"}, ctx)
	if r := claudia(t, "ui"); !slices.Equal(r.exec, []string{"/bin/tmux", "tmux", "attach-session", "-t", "pay:1"}) {
		t.Errorf("attach exec %q", r.exec)
	}

	executable = func() (string, error) { return "", errors.New("no binary") }
	lookPath = func(string) (string, error) { return "", errors.New("no tmux") }
	tui(t, ui.Result{Restart: true}, ctx)
	if r := claudia(t, "ui"); r.code != 1 || r.err != "claudia: no binary\n" {
		t.Errorf("restart without binary %+v", r)
	}
	tui(t, ui.Result{Attach: "pay:1"}, ctx)
	if r := claudia(t, "ui"); r.code != 1 || r.err != "claudia: no tmux\n" {
		t.Errorf("attach without tmux %+v", r)
	}
	gone, cancel := context.WithCancel(ctx)
	cancel()
	tui(t, ui.Result{}, gone)
	if r := claudia(t, "ui"); r.code != 1 || !strings.HasPrefix(r.err, "claudia: ") {
		t.Errorf("failed program %+v", r)
	}
}

// liveSession registers a waiting Claude session running in pane.
func liveSession(t *testing.T, pane tmux.Pane) claude.Live {
	home := os.Getenv("HOME")
	l := claude.Live{PID: pane.PID, SessionID: "s1", Cwd: "/code/pay", Status: claude.StatusWaiting, UpdatedAt: 1762160000000}
	b, _ := json.Marshal(l)
	os.MkdirAll(filepath.Join(home, ".claude", "projects"), 0o755)
	os.MkdirAll(filepath.Join(home, ".claude", "sessions"), 0o755)
	os.WriteFile(filepath.Join(home, ".claude", "sessions", strconv.Itoa(l.PID)+".json"), b, 0o600)
	return l
}

func TestCtlHereAndInbox(t *testing.T) {
	isolate(t)
	if r := claudia(t, "ctl", "here"); r.code != 1 || !strings.Contains(r.err, "not inside a Claude session") {
		t.Errorf("outside %+v", r)
	}
	if r := claudia(t, "ctl", "dismiss", "--here"); r.code != 1 || !strings.Contains(r.err, "not inside a Claude session") {
		t.Errorf("dismiss outside %+v", r)
	}
	if r := claudia(t, "ctl", "inbox"); r.out != "null\n" {
		t.Errorf("empty inbox %+v", r)
	}

	pane := inside(t, "pay")
	liveSession(t, pane)
	t.Setenv("TMUX_PANE", pane.ID)
	var here ui.CtlHere
	r := claudia(t, "ctl", "here")
	if err := json.Unmarshal([]byte(r.out), &here); err != nil {
		t.Fatalf("%v: %+v", err, r)
	}
	if here.Session != "s1" || here.Dir != "/code/pay" || len(here.Items) != 1 || here.Items[0].ID != "cc:s1:1762160000000" || !here.Items[0].ThisSession {
		t.Errorf("here %+v", here)
	}
	var inbox []ui.CtlItem
	json.Unmarshal([]byte(claudia(t, "ctl", "inbox").out), &inbox)
	if len(inbox) != 1 || !inbox[0].ThisSession || inbox[0].Snoozed {
		t.Errorf("inbox %+v", inbox)
	}

	if r := claudia(t, "ctl", "dismiss", "--here"); r.out != "snoozed 1 item(s)\n" {
		t.Errorf("dismiss %+v", r)
	}
	if r := claudia(t, "ctl", "dismiss", "--here"); r.out != "nothing to dismiss\n" {
		t.Errorf("dismiss again %+v", r)
	}
	if r := claudia(t, "ctl", "dismiss", "a", "b"); r.out != "snoozed 2 item(s)\n" {
		t.Errorf("dismiss ids %+v", r)
	}
}

func TestCtlHome(t *testing.T) {
	isolate(t)
	swap(t, &executable, func() (string, error) { return "/opt/claudia", nil })
	swap(t, &lookPath, func(string) (string, error) { return "/bin/tmux", nil })
	// claudia isn't running: it starts, taking over the terminal.
	if r := claudia(t, "ctl", "home"); !slices.Equal(r.exec, []string{"/bin/tmux", "tmux", "new-session", "-A", "-s", "claudia", `"/opt/claudia" ui`}) {
		t.Errorf("not running %+v", r)
	}

	inside(t, "claudia")
	tmuxOut(t, "new-session", "-d", "-s", "pay", "sleep 60")
	attach(t, "pay")
	var ran []string
	swap(t, &execCommand, func(name string, args ...string) *exec.Cmd {
		ran = append([]string{name}, args...)
		return exec.Command("true")
	})
	if r := claudia(t, "ctl", "home", "--close"); r.code != -1 || ran != nil {
		t.Errorf("close without a pane %+v %q", r, ran)
	}
	if s := tmuxOut(t, "list-clients", "-F", "#{session_name}"); s != "claudia" {
		t.Errorf("client is on %q", s)
	}
	t.Setenv("TMUX_PANE", "%7")
	if r := claudia(t, "ctl", "home", "--close"); r.code != -1 {
		t.Errorf("close %+v", r)
	}
	if want := []string{"tmux", "run-shell", "-b", "sleep 1; tmux kill-pane -t '%7'"}; !slices.Equal(ran, want) {
		t.Errorf("ran %q", ran)
	}
}

func TestPlanCommand(t *testing.T) {
	isolate(t)
	var asked []string
	var err error
	swap(t, &planFor, func(pane string) (*ui.Model, error) { asked = append(asked, pane); return ui.New(ui.Options{}), err })
	tui(t, ui.Result{}, context.Background())
	if r := claudia(t, "plan", "%7"); r.code != -1 || len(asked) != 1 || asked[0] != "%7" {
		t.Errorf("the pane given: %+v %q", r, asked)
	}

	swap(t, &execCommand, func(name string, args ...string) *exec.Cmd {
		if strings.Join(append([]string{name}, args...), " ") != "tmux display-message -p #{pane_id}" {
			t.Errorf("ran %s %v", name, args)
		}
		return exec.Command("echo", "%3")
	})
	if r := claudia(t, "plan"); r.code != -1 || asked[1] != "%3" {
		t.Errorf("without one, the active pane: %+v %q", r, asked)
	}

	err = errors.New("no Claude session in this pane")
	if r := claudia(t, "plan", "%9"); r.code != 1 || !strings.Contains(r.err, "no Claude session in this pane") {
		t.Errorf("no session: %+v", r)
	}
	swap(t, &execCommand, func(string, ...string) *exec.Cmd { return exec.Command("false") })
	if r := claudia(t, "plan"); r.code != 1 || !strings.Contains(r.err, "claudia plan runs inside tmux") {
		t.Errorf("outside tmux: %+v", r)
	}
}

func TestCtlPrio(t *testing.T) {
	isolate(t)
	if r := claudia(t, "ctl", "prio"); r.out != "[]\n" {
		t.Errorf("no order %+v", r)
	}
	if r := claudia(t, "ctl", "prio", "ABC-1", "--note"); r.code != 1 || !strings.Contains(r.err, "--note needs a value") {
		t.Errorf("note without a value %+v", r)
	}
	if r := claudia(t, "ctl", "prio", "what?"); r.code != 1 || !strings.Contains(r.err, `can't tell what "what?" is`) {
		t.Errorf("bad ref %+v", r)
	}
	var got []ui.CtlAgreed
	r := claudia(t, "ctl", "prio", "abc-2", "--note", "Zdeněk, grooming", "ABC-1")
	if err := json.Unmarshal([]byte(r.out), &got); err != nil || len(got) != 2 || got[0].Subject != "ABC-2" || got[1].Note != "Zdeněk, grooming" {
		t.Errorf("set %+v %v", r, err)
	}
	if r := claudia(t, "ctl", "prio", "--clear"); r.out != "[]\n" {
		t.Errorf("cleared %+v", r)
	}
}

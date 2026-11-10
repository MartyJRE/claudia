package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MartyJRE/claudia/internal/brief"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/kitty"
	"github.com/MartyJRE/claudia/internal/launch"
	"github.com/MartyJRE/claudia/internal/tmux"
	"github.com/MartyJRE/claudia/internal/ui"
	"github.com/MartyJRE/claudia/internal/update"
	"github.com/MartyJRE/claudia/internal/watch"
)

// Seams for tests: what replaces or ends the process, and what would run for
// real (the TUI, the watcher, setup, GitHub).
var (
	exit          = os.Exit
	syscallExec   = syscall.Exec
	executable    = os.Executable
	lookPath      = exec.LookPath
	execCommand   = exec.Command
	newProgram    = tea.NewProgram
	runWatch      = watch.Run
	runSetup      = setup
	fetchUpdate   = update.Fetch
	installLatest = update.Install
	currentBuild  = update.Current
	buildInfo     = debug.ReadBuildInfo
	planFor       = ui.PlanFor
)

func main() { run(os.Args[1:]) }

func run(args []string) {
	if len(args) > 0 && args[0] == "img" {
		if err := kitty.View(args[1:]); err != nil {
			fail(err)
		}
		return
	}
	if len(args) > 0 && args[0] == "ctl" {
		if err := ctl(args[1:]); err != nil {
			fail(err)
		}
		return
	}
	if len(args) > 0 && args[0] == "plan" {
		if err := planCmd(args[1:]); err != nil {
			fail(err)
		}
		return
	}
	if len(args) > 0 && args[0] == "teams" {
		if err := teamsCmd(args[1:]); err != nil {
			fail(err)
		}
		return
	}
	if len(args) > 1 && args[0] == "open" {
		// Hand the link to claudia's session and switch there; it picks it up
		// on its next tick (or right after starting).
		if err := ui.RequestOpen(args[1]); err != nil {
			fail(err)
		}
		home()
		return
	}
	if len(args) > 0 && args[0] == "brief" {
		makeBrief()
		b, _ := brief.Load()
		if b.Err != "" {
			fail(errors.New(b.Err))
		}
		fmt.Println(b.Text)
		return
	}
	if len(args) > 0 && args[0] == "update" {
		p, err := fetchUpdate()
		switch {
		case err != nil:
			fmt.Println("couldn't ask GitHub what's new:", err)
		case len(p.Commits) == 0 && len(p.Releases) == 0:
			fmt.Println("already up to date; reinstalling", p.Latest)
		default:
			fmt.Printf("updating to %s, what's new:\n", p.Latest)
			for _, r := range p.Releases {
				fmt.Printf("  %s  %s\n", r.Tag, r.Published.Format(time.DateOnly))
				for l := range strings.SplitSeq(strings.TrimSpace(r.Body), "\n") {
					fmt.Println("    " + strings.TrimRight(l, "\r"))
				}
			}
			for _, c := range p.Commits {
				fmt.Printf("  %s  %s\n", c.SHA, c.Subject)
			}
		}
		fmt.Println("downloading and building…")
		if err := installLatest(func(l string) {
			if !strings.HasPrefix(l, "get ") {
				fmt.Println("  " + l)
			}
		}); err != nil {
			fail(err)
		}
		watch.Stop()
		fmt.Println("done; restart claudia to use it (ctrl+u inside claudia restarts by itself)")
		return
	}
	if len(args) > 0 && args[0] == "version" {
		fmt.Println(version())
		return
	}
	if len(args) > 0 && args[0] == "keys" {
		fmt.Print(ui.KeyActions())
		return
	}
	if len(args) > 0 && args[0] == "setup" {
		if err := runSetup(); err != nil {
			fail(err)
		}
		return
	}
	if len(args) > 0 && args[0] == "watch" {
		if err := runWatch(makeBrief); err != nil {
			fail(err)
		}
		return
	}
	if len(args) == 0 {
		home()
		return
	}
	watch.Spawn()
	m := ui.New(ui.Options{Popup: args[0] == "popup"})
	if _, err := newProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fail(err)
	}
	if m.Result.Restart {
		// The binary on disk was just replaced: start it in place of this one.
		self, err := executable()
		if err != nil {
			fail(err)
		}
		fail(syscallExec(self, os.Args, os.Environ()))
	}
	if t := m.Result.Attach; t != "" {
		bin, err := lookPath("tmux")
		if err != nil {
			fail(err)
		}
		fail(syscallExec(bin, []string{"tmux", "attach-session", "-t", t}, os.Environ()))
	}
}

// planCmd reviews the plan of the Claude session in a tmux pane: the one
// given, else the active one, which in a popup is the pane under it.
func planCmd(args []string) error {
	pane := ""
	if len(args) > 0 {
		pane = args[0]
	} else {
		out, err := execCommand("tmux", "display-message", "-p", "#{pane_id}").Output()
		if err != nil {
			return fmt.Errorf("claudia plan runs inside tmux: %w", err)
		}
		pane = strings.TrimSpace(string(out))
	}
	m, err := planFor(pane)
	if err != nil {
		return err
	}
	_, err = newProgram(m, tea.WithAltScreen()).Run()
	return err
}

// home puts you in the dedicated "claudia" tmux session, starting it if needed.
// claudia lives there so jumping to a Claude session never closes it, and
// prefix + C toggles between it and wherever you were.
func home() {
	self, err := executable()
	if err != nil {
		fail(err)
	}
	cmd := fmt.Sprintf("%q ui", self)
	if tmux.Inside() {
		if !tmux.HasSession(tmux.Home) {
			if err := execCommand("tmux", "new-session", "-d", "-s", tmux.Home, cmd).Run(); err != nil {
				fail(err)
			}
		}
		if err := tmux.Switch("=" + tmux.Home); err != nil {
			fail(err)
		}
		return
	}
	bin, err := lookPath("tmux")
	if err != nil {
		fail(err)
	}
	fail(syscallExec(bin, []string{"tmux", "new-session", "-A", "-s", tmux.Home, cmd}, os.Environ()))
}

// makeBrief rewrites the inbox brief; on failure the previous text stays, with
// the error next to it.
func makeBrief() {
	prev, _ := brief.Load()
	m := ui.Headless()
	text, err := brief.Generate(m.BriefData(), prev.Text, launch.WorkProfile(claude.DefaultProfiles()).Dir)
	b := brief.Brief{Text: text, At: time.Now()}
	if err != nil {
		b = brief.Brief{Text: prev.Text, At: prev.At, Err: err.Error()}
		fmt.Fprintln(os.Stderr, time.Now().Format(time.TimeOnly), "brief:", err)
	}
	_ = brief.Save(b)
}

func version() string {
	if sha, ok := currentBuild(); ok {
		return sha
	}
	bi, ok := buildInfo()
	if !ok {
		return "unknown"
	}
	rev, dirty := "", ""
	for _, st := range bi.Settings {
		switch {
		case st.Key == "vcs.revision" && len(st.Value) >= 12:
			rev = st.Value[:12]
		case st.Key == "vcs.modified" && st.Value == "true":
			dirty = "+changes"
		}
	}
	if rev == "" {
		return "unknown (local build)"
	}
	return rev + dirty + " (local build)"
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "claudia:", err)
	exit(1)
}

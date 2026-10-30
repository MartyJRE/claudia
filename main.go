package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
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

func main() {
	args := os.Args[1:]
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
		if err := update.Install(os.Stdout); err != nil {
			fail(err)
		}
		watch.Stop()
		fmt.Println("updated; restart claudia to use it (ctrl+u inside claudia does that for you)")
		return
	}
	if len(args) > 0 && args[0] == "setup" {
		if err := setup(); err != nil {
			fail(err)
		}
		return
	}
	if len(args) > 0 && args[0] == "watch" {
		if err := watch.Run(makeBrief); err != nil {
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
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fail(err)
	}
	if m.Result.Restart {
		// The binary on disk was just replaced: start it in place of this one.
		self, err := os.Executable()
		if err != nil {
			fail(err)
		}
		fail(syscall.Exec(self, os.Args, os.Environ()))
	}
	if t := m.Result.Attach; t != "" {
		bin, err := exec.LookPath("tmux")
		if err != nil {
			fail(err)
		}
		fail(syscall.Exec(bin, []string{"tmux", "attach-session", "-t", t}, os.Environ()))
	}
}

// home puts you in the dedicated "claudia" tmux session, starting it if needed.
// claudia lives there so jumping to a Claude session never closes it, and
// prefix + C toggles between it and wherever you were.
func home() {
	self, err := os.Executable()
	if err != nil {
		fail(err)
	}
	cmd := fmt.Sprintf("%q ui", self)
	if tmux.Inside() {
		if !tmux.HasSession(tmux.Home) {
			if err := exec.Command("tmux", "new-session", "-d", "-s", tmux.Home, cmd).Run(); err != nil {
				fail(err)
			}
		}
		if err := tmux.Switch("=" + tmux.Home); err != nil {
			fail(err)
		}
		return
	}
	bin, err := exec.LookPath("tmux")
	if err != nil {
		fail(err)
	}
	fail(syscall.Exec(bin, []string{"tmux", "new-session", "-A", "-s", tmux.Home, cmd}, os.Environ()))
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

func fail(err error) {
	fmt.Fprintln(os.Stderr, "claudia:", err)
	os.Exit(1)
}

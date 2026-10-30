package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MartyJRE/claudia/internal/kitty"
	"github.com/MartyJRE/claudia/internal/tmux"
	"github.com/MartyJRE/claudia/internal/ui"
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
	if len(args) > 0 && args[0] == "watch" {
		if err := watch.Run(); err != nil {
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

func fail(err error) {
	fmt.Fprintln(os.Stderr, "claudia:", err)
	os.Exit(1)
}

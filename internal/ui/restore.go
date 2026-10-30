package ui

import (
	"fmt"
	"os/exec"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MartyJRE/claudia/internal/launch"
	"github.com/MartyJRE/claudia/internal/state"
	"github.com/MartyJRE/claudia/internal/tmux"
)

const liveFile = "live-sessions.json"

type restoreItem struct {
	ID      string `json:"id"`
	Profile string `json:"profile"`
	Dir     string `json:"dir"`
	Name    string `json:"name"`
	InTmux  bool   `json:"in_tmux"`
}

type liveSnapshot struct {
	Boot     string        `json:"boot"`
	Tmux     string        `json:"tmux"`
	Sessions []restoreItem `json:"sessions"`
}

func bootID() string {
	out, _ := exec.Command("sysctl", "-n", "kern.boottime").Output()
	return strings.TrimSpace(string(out))
}

func tmuxServer() string {
	out, _ := exec.Command("tmux", "display-message", "-p", "#{pid}").Output()
	return strings.TrimSpace(string(out))
}

// pendingRestore offers the sessions that were live before a reboot or a tmux
// server restart; tmux-resurrect brings windows back but not the claude processes.
func pendingRestore() []restoreItem {
	var snap liveSnapshot
	if !state.Load(liveFile, &snap) || len(snap.Sessions) == 0 {
		return nil
	}
	if snap.Boot != bootID() {
		return snap.Sessions
	}
	if snap.Tmux == tmuxServer() {
		return nil
	}
	// Only tmux restarted: sessions in other terminals weren't affected, and
	// if they're gone, they were closed on purpose.
	var out []restoreItem
	for _, s := range snap.Sessions {
		if s.InTmux {
			out = append(out, s)
		}
	}
	return out
}

var lastLive string

// saveLive records live terminal sessions, unless a restore offer is pending:
// overwriting then would lose what the offer is about.
func saveLive(rows []row, pending bool) {
	if pending {
		return
	}
	snap := liveSnapshot{Boot: bootID(), Tmux: tmuxServer()}
	for _, r := range rows {
		if !r.live() || r.Live.Entrypoint != "cli" || r.Dir() == "" {
			continue
		}
		snap.Sessions = append(snap.Sessions, restoreItem{r.ID, r.Profile.Name, r.Dir(), r.Name(), r.pane != nil})
	}
	slices.SortFunc(snap.Sessions, func(a, b restoreItem) int { return strings.Compare(a.ID, b.ID) })
	key := snap.Boot + snap.Tmux
	for _, s := range snap.Sessions {
		key += s.ID
	}
	if key == lastLive {
		return
	}
	lastLive = key
	_ = state.Save(liveFile, snap)
}

func (m *Model) restoreAll() (tea.Model, tea.Cmd) {
	if len(m.restore) == 0 {
		return m, nil
	}
	live := map[string]bool{}
	for _, r := range m.sessions {
		if r.live() {
			live[r.ID] = true
		}
	}
	var first string
	n := 0
	for _, it := range m.restore {
		if live[it.ID] {
			continue
		}
		sp := launch.Spec{Dir: it.Dir, Profile: launch.Profile(it.Profile, m.store.Profiles), Name: it.Name, Args: []string{"--resume", it.ID}}
		target, err := launch.Start(sp)
		if err != nil {
			m.setFlash(err.Error(), true)
			continue
		}
		if first == "" {
			first = target
		}
		n++
	}
	m.forgetRestore()
	m.setFlash(fmt.Sprintf("restored %d sessions", n), false)
	if first != "" && !tmux.Inside() {
		return m.goTo(first)
	}
	return m, nil
}

func (m *Model) forgetRestore() {
	m.restore = nil
	lastLive = ""
	saveLive(m.sessions, false)
}

// notLive drops sessions that are running again; nil once nothing is left to offer.
func notLive(items []restoreItem, rows []row) []restoreItem {
	if items == nil {
		return nil
	}
	live := map[string]bool{}
	for _, r := range rows {
		if r.live() {
			live[r.ID] = true
		}
	}
	var out []restoreItem
	for _, it := range items {
		if !live[it.ID] {
			out = append(out, it)
		}
	}
	return out
}

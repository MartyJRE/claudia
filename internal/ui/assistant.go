package ui

import (
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/launch"
	"github.com/MartyJRE/claudia/internal/state"
)

const assistantFile = "assistant.json"

type assistant struct {
	ID  string `json:"id"`
	Dir string `json:"dir"`
	Day string `json:"day"`
}

func today() string { return clockNow().Format("2006-01-02") }

// openAssistant jumps to the assistant session: live → switch to it, ended
// today → resume it, otherwise pick a dir and start a new one, so a
// conversation lasts a day at most.
func (m *Model) openAssistant() (tea.Model, tea.Cmd) {
	var a assistant
	state.Load(assistantFile, &a)
	if r, ok := m.assistantRow(a.ID); ok {
		if r.live() {
			return m.jumpTo(r)
		}
		if a.Day == today() && a.Dir != "" {
			return m.start(launch.Assistant(r.Profile, a.Dir, a.ID, true))
		}
	}
	m.pickAssistantDir()
	return m, nil
}

// freshAssistant closes the running assistant, if any, and starts a new one.
func (m *Model) freshAssistant() (tea.Model, tea.Cmd) {
	var a assistant
	state.Load(assistantFile, &a)
	if r, ok := m.assistantRow(a.ID); ok && r.live() {
		if err := killProcess(r.Live.PID, syscall.SIGTERM); err != nil {
			m.setFlash("close the assistant: "+err.Error(), true)
			return m, nil
		}
	}
	m.pickAssistantDir()
	return m, nil
}

func (m *Model) assistantRow(id string) (row, bool) {
	if id == "" {
		return row{}, false
	}
	for _, r := range m.sessions {
		if r.ID == id {
			return r, true
		}
	}
	return row{}, false
}

func (m *Model) pickAssistantDir() {
	m.pickDir("assistant in", "", getConfig().AssistantDir, nil, func(m *Model, dir string, p claude.Profile) (tea.Model, tea.Cmd) {
		a := assistant{ID: launch.NewID(), Dir: dir, Day: today()}
		if err := state.Save(assistantFile, a); err != nil {
			m.setFlash(err.Error(), true)
			return m, nil
		}
		return m.start(launch.Assistant(p, dir, a.ID, false))
	})
}

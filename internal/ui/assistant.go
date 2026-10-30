package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/MartyJRE/claudia/internal/launch"
	"github.com/MartyJRE/claudia/internal/state"
)

const assistantFile = "assistant.json"

// openAssistant jumps to the assistant session: live → switch to it, ended →
// resume it, never started → start it.
func (m *Model) openAssistant() (tea.Model, tea.Cmd) {
	var a struct {
		ID string `json:"id"`
	}
	state.Load(assistantFile, &a)
	p := launch.WorkProfile(m.store.Profiles)
	if a.ID != "" {
		for _, r := range m.sessions {
			if r.ID != a.ID {
				continue
			}
			if r.live() {
				return m.jumpTo(r)
			}
			return m.start(launch.Assistant(r.Profile, a.ID, true))
		}
	}
	a.ID = launch.NewID()
	if err := state.Save(assistantFile, a); err != nil {
		m.setFlash(err.Error(), true)
		return m, nil
	}
	return m.start(launch.Assistant(p, a.ID, false))
}

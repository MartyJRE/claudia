package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type imagesMsg struct {
	key   string
	files []string
}

var imageExt = []string{".png", ".jpg", ".jpeg", ".gif"}

// showImages exports the ticket (which downloads its images) and hands the
// terminal to `claudia img`, so Bubble Tea's redraws can't wipe the pictures.
func (m *Model) showImages(key string) tea.Cmd {
	return m.exportBrief(key, func(path string) tea.Msg {
		entries, err := os.ReadDir(filepath.Dir(path))
		if err != nil {
			return flashMsg{err.Error(), true}
		}
		var files []string
		for _, e := range entries {
			if slices.Contains(imageExt, strings.ToLower(filepath.Ext(e.Name()))) {
				files = append(files, filepath.Join(filepath.Dir(path), e.Name()))
			}
		}
		return imagesMsg{key, files}
	})
}

func (m *Model) openImages(msg imagesMsg) tea.Cmd {
	if len(msg.files) == 0 {
		m.setFlash(msg.key+" has no image attachments", false)
		return nil
	}
	self, err := os.Executable()
	if err != nil {
		m.setFlash(err.Error(), true)
		return nil
	}
	m.flash = ""
	return tea.ExecProcess(exec.Command(self, append([]string{"img"}, msg.files...)...), func(err error) tea.Msg {
		if err != nil {
			return flashMsg{err.Error(), true}
		}
		return nil
	})
}

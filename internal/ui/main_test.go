package ui

import (
	"os"
	"testing"
)

// Tests must not read the real ~/.config/claudia, ~/.claude or ask glab for its host.
func TestMain(m *testing.M) {
	dir, _ := os.MkdirTemp("", "claudia-test")
	os.Setenv("XDG_CONFIG_HOME", dir)
	os.Setenv("XDG_STATE_HOME", dir)
	os.Setenv("HOME", dir)
	os.Setenv("CLAUDIA_JIRA_KEYS", "ABC")
	os.Setenv("CLAUDIA_GITLAB_HOST", "gitlab.example.com")
	os.Unsetenv("TMUX")
	os.Unsetenv("TMUX_PANE")
	stubOS()
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

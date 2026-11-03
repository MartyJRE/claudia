package atlassian

import (
	"os"
	"testing"
)

// Tests must not read the real ~/.config/claudia.
func TestMain(m *testing.M) {
	dir, _ := os.MkdirTemp("", "claudia-test")
	os.Setenv("XDG_CONFIG_HOME", dir)
	os.Setenv("XDG_STATE_HOME", dir)
	os.Setenv("CLAUDIA_GITLAB_HOST", "gitlab.example.com")
	os.Setenv("ATLASSIAN_BASE_URL", "https://acme.atlassian.net/")
	os.Setenv("CLAUDIA_JIRA_PROJECT", "PAY")
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

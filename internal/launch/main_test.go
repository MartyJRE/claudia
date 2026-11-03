package launch

import (
	"os"
	"path/filepath"
	"testing"
)

// Tests must not read the real ~/.config/claudia.
func TestMain(m *testing.M) {
	dir, _ := os.MkdirTemp("", "claudia-test")
	os.Setenv("XDG_CONFIG_HOME", dir)
	os.Setenv("XDG_STATE_HOME", dir)
	os.Setenv("CLAUDIA_GITLAB_HOST", "gitlab.example.com")
	os.MkdirAll(filepath.Join(dir, "claudia"), 0o755)
	os.WriteFile(filepath.Join(dir, "claudia", "config.json"),
		[]byte(`{"code_root": "/code", "remote_control": ["work"], "vault": "/notes"}`), 0o600)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

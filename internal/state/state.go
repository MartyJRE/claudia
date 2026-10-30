package state

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func Dir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "claudia")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "claudia")
}

func Path(name string) string { return filepath.Join(Dir(), name) }

func Load(name string, v any) bool {
	b, err := os.ReadFile(Path(name))
	if err != nil {
		return false
	}
	return json.Unmarshal(b, v) == nil
}

// Save writes through a temp file so a crash never leaves half a file behind.
func Save(name string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	p := Path(name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

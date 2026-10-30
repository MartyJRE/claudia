package claude

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultProfilesDiscovers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, d := range []string{".claude-work/projects", ".claude-personal/projects", ".claude-empty"} {
		os.MkdirAll(filepath.Join(home, d), 0o755)
	}
	os.WriteFile(filepath.Join(home, ".claude.json"), []byte("{}"), 0o644)
	got := DefaultProfiles()
	if len(got) != 2 || got[0].Name != "personal" || got[1].Name != "work" || got[1].Dir != filepath.Join(home, ".claude-work") {
		t.Errorf("got %+v", got)
	}
}

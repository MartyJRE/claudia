package brief

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveLoad(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if _, ok := Load(); ok {
		t.Fatal("loaded before saving")
	}
	at := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	if err := Save(Brief{Text: "all quiet", At: at, Err: "boom"}); err != nil {
		t.Fatal(err)
	}
	b, ok := Load()
	if !ok || b.Text != "all quiet" || !b.At.Equal(at) || b.Err != "boom" {
		t.Errorf("got %+v %v", b, ok)
	}
	if !strings.HasSuffix(Path(), filepath.Join("claudia", "brief.json")) {
		t.Errorf("path %s", Path())
	}
}

func TestRequest(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if TakeRequest() {
		t.Fatal("request without asking")
	}
	if err := Request(); err != nil {
		t.Fatal(err)
	}
	if !TakeRequest() {
		t.Error("request lost")
	}
	if TakeRequest() {
		t.Error("request not cleared")
	}
}

// A fake claude on PATH stands in for the real one.
func fakeClaude(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	return dir
}

func TestGenerate(t *testing.T) {
	dir := fakeClaude(t, `cat > "$(dirname "$0")/stdin"; echo "$CLAUDE_CONFIG_DIR" > "$(dirname "$0")/env"; echo "$@" > "$(dirname "$0")/args"; printf '  ABC-1 waits on you.\n\n'`)
	got, err := Generate(map[string]int{"inbox": 2}, "", "/profiles/work")
	if err != nil {
		t.Fatal(err)
	}
	if got != "ABC-1 waits on you." {
		t.Errorf("text %q", got)
	}
	in, _ := os.ReadFile(filepath.Join(dir, "stdin"))
	if !strings.HasPrefix(string(in), "Previous brief:\n(none yet)\n") || !strings.Contains(string(in), `"inbox": 2`) {
		t.Errorf("stdin %q", in)
	}
	env, _ := os.ReadFile(filepath.Join(dir, "env"))
	if strings.TrimSpace(string(env)) != "/profiles/work" {
		t.Errorf("config dir %q", env)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args"))
	if !strings.HasPrefix(string(args), "-p --no-session-persistence --tools  --system-prompt") {
		t.Errorf("args %q", args)
	}

	if _, err := Generate(nil, "before", ""); err != nil {
		t.Fatal(err)
	}
	if in, _ := os.ReadFile(filepath.Join(dir, "stdin")); !strings.HasPrefix(string(in), "Previous brief:\nbefore\n") {
		t.Errorf("previous not passed: %q", in)
	}
}

func TestGenerateErrors(t *testing.T) {
	fakeClaude(t, `cat >/dev/null; echo "  not logged in " >&2; exit 1`)
	if _, err := Generate(nil, "", ""); err == nil || err.Error() != "not logged in" {
		t.Errorf("stderr error: %v", err)
	}
	fakeClaude(t, `exit 3`)
	if _, err := Generate(nil, "", ""); err == nil || !strings.Contains(err.Error(), "exit status 3") {
		t.Errorf("silent failure: %v", err)
	}
	if _, err := Generate(func() {}, "", ""); err == nil {
		t.Error("unmarshalable data")
	}
}

func TestGenerateFallsBackToLocalBin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	bin := filepath.Join(home, ".local", "bin")
	os.MkdirAll(bin, 0o755)
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nprintf 'from ~/.local/bin'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := Generate(nil, "", ""); err != nil || got != "from ~/.local/bin" {
		t.Errorf("got %q %v", got, err)
	}
}

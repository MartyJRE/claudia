package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDir(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/s")
	if got := Path("x.json"); got != "/s/claudia/x.json" {
		t.Errorf("got %s", got)
	}
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/h")
	if got := Dir(); got != "/h/.local/state/claudia" {
		t.Errorf("got %s", got)
	}
}

func TestRoundTrip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	type doc struct {
		Name string
		N    int
	}
	var got doc
	if Load("doc.json", &got) {
		t.Fatal("loaded a file that doesn't exist")
	}
	if err := Save("doc.json", doc{"a", 2}); err != nil {
		t.Fatal(err)
	}
	if !Load("doc.json", &got) || got != (doc{"a", 2}) {
		t.Errorf("got %+v", got)
	}
	if _, err := os.Stat(Path("doc.json") + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp file left behind: %v", err)
	}
	if err := Save("doc.json", doc{"b", 3}); err != nil {
		t.Fatal(err)
	}
	if !Load("doc.json", &got) || got.Name != "b" {
		t.Errorf("overwrite: %+v", got)
	}
}

func TestLoadBroken(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path("bad.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if Load("bad.json", &v) {
		t.Error("broken JSON loaded")
	}
}

func TestSaveErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	if err := Save("x.json", func() {}); err == nil {
		t.Error("unmarshalable value saved")
	}
	// A file where the state dir should be.
	if err := os.WriteFile(filepath.Join(dir, "claudia"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Save("x.json", 1); err == nil {
		t.Error("saved under a file")
	}
	os.Remove(filepath.Join(dir, "claudia"))
	// A directory where the temp file goes.
	if err := os.MkdirAll(filepath.Join(dir, "claudia", "y.json.tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Save("y.json", 1); err == nil {
		t.Error("saved over a directory")
	}
}

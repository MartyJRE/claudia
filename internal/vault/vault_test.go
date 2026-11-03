package vault

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestIndex(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	write("01 - Projects/ABC-7/Plan.md", "# Plan")
	write("04 - Permanent/Teams.md", "see [[ABC-7]] and abc-9")
	write("05 - Fleeting/Other.md", "nothing here")
	write(".obsidian/x.md", "ABC-7")
	re := regexp.MustCompile(`(?i)\b(ABC)-(\d+)\b`)
	ix := New(root, "01 - Projects", re)
	ix.Refresh()
	got := ix.For("ABC-7")
	if len(got) != 2 || got[0].Title != "Plan" || got[1].Title != "Teams" {
		t.Fatalf("got %+v", got)
	}
	if n := ix.For("ABC-9"); len(n) != 1 {
		t.Errorf("lower-case mention: %+v", n)
	}
	p, err := ix.NewNote("ABC-8", "First: thoughts", []string{"abc"})
	if err != nil || filepath.Base(p) != "First - thoughts.md" {
		t.Fatalf("new note %q %v", p, err)
	}
	ix.Refresh()
	if len(ix.For("ABC-8")) != 1 {
		t.Error("new note not indexed")
	}
}

func TestRefreshEdges(t *testing.T) {
	root := t.TempDir()
	re := regexp.MustCompile(`(?i)\b(ABC)-(\d+)\b`)
	missing := New(filepath.Join(root, "missing"), "P", re)
	if missing.Refresh(); len(missing.All()) != 0 {
		t.Error("notes from a missing vault")
	}
	os.MkdirAll(filepath.Join(root, "P", "ABC-1"), 0o755)
	gone := filepath.Join(root, "P", "ABC-1", "Gone.md")
	os.WriteFile(gone, []byte("x"), 0o644)
	os.WriteFile(filepath.Join(root, "P", "ABC-1", "pic.png"), nil, 0o644)
	// Listed but not searchable: its notes can't be stat'ed.
	locked := filepath.Join(root, "locked")
	os.Mkdir(locked, 0o755)
	os.WriteFile(filepath.Join(locked, "Hidden.md"), []byte("ABC-1"), 0o644)
	os.Chmod(locked, 0o400)
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	ix := New(root, "P", re)
	ix.Refresh()
	all := ix.All()
	if len(all) != 1 || all[0].Title != "Gone" || all[0].Folder() != filepath.Join("P", "ABC-1") {
		t.Fatalf("got %+v", all)
	}
	os.Remove(gone)
	ix.Refresh()
	if len(ix.All()) != 0 {
		t.Errorf("deleted note kept: %+v", ix.All())
	}
}

func TestNewNoteEdges(t *testing.T) {
	root := t.TempDir()
	ix := New(root, "P", regexp.MustCompile(`ABC-\d+`))
	p, err := ix.NewNote("ABC-1", "Plan", nil)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(p, []byte("mine"), 0o644)
	if again, err := ix.NewNote("ABC-1", " Plan ", []string{"x"}); err != nil || again != p {
		t.Errorf("got %q %v", again, err)
	}
	if b, _ := os.ReadFile(p); string(b) != "mine" {
		t.Errorf("existing note overwritten: %q", b)
	}
	os.WriteFile(filepath.Join(root, "Q"), nil, 0o644)
	ix.Projects = "Q"
	if _, err := ix.NewNote("ABC-1", "Plan", nil); err == nil {
		t.Error("note under a file")
	}
}

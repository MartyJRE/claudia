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

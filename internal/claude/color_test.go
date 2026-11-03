package claude

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTranscriptColor(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	write := func(s string) {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString(s)
		f.Close()
	}
	tr := &Transcript{ID: "s", Path: p}
	write(`{"type":"agent-color","agentColor":"pink","sessionId":"s"}` + "\n")
	if err := tr.Feed(); err != nil || tr.Color != "pink" {
		t.Fatalf("color %q %v", tr.Color, err)
	}
	write(`{"type":"agent-color","agentColor":"green","sessionId":"s"}` + "\n")
	if tr.Feed(); tr.Color != "green" {
		t.Errorf("the latest /color wins, got %q", tr.Color)
	}
	write(`{"type":"agent-color","agentColor":"default","sessionId":"s"}` + "\n")
	if tr.Feed(); tr.Color != "" {
		t.Errorf("/color default resets, got %q", tr.Color)
	}
}

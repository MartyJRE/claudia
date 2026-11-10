package claude

import (
	"os"
	"path/filepath"
	"testing"
)

func TestContextFill(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	lines := `{"type":"assistant","requestId":"r1","message":{"model":"claude-opus-5","usage":{"input_tokens":1000,"cache_read_input_tokens":150000,"cache_creation_input_tokens":13000}}}
{"type":"assistant","isSidechain":true,"requestId":"r2","message":{"model":"claude-haiku-4-5","usage":{"input_tokens":190000}}}
`
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	tr := &Transcript{Path: path}
	if err := tr.Feed(); err != nil {
		t.Fatal(err)
	}
	if tr.Context != 164000 || tr.ContextFill() != 0.82 || tr.ContextFull {
		t.Errorf("context %d fill %v full %v", tr.Context, tr.ContextFill(), tr.ContextFull)
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"type":"assistant","message":{"model":"<synthetic>","content":[{"type":"text","text":"Prompt is too long"}]}}` + "\n")
	f.Close()
	tr.Feed()
	if !tr.ContextFull {
		t.Error("the too-long error")
	}
	f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"type":"assistant","requestId":"r3","message":{"model":"claude-opus-5[1m]","usage":{"input_tokens":100000}}}` + "\n")
	f.Close()
	tr.Feed()
	if tr.ContextFull || tr.ContextFill() != 0.1 {
		t.Errorf("after /compact: full %v fill %v", tr.ContextFull, tr.ContextFill())
	}
	var none *Transcript
	if none.ContextFill() != 0 || (&Transcript{}).ContextFill() != 0 {
		t.Error("no context yet")
	}
	if ContextWindow("claude-sonnet-5") != 200_000 || ContextWindow("Claude-Opus-5[1M]") != 1_000_000 {
		t.Error("windows")
	}
}

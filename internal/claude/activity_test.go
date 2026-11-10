package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func toolUse(id, name, input string) string {
	return `{"type":"assistant","cwd":"/repo","message":{"model":"claude-opus-5-5","content":[{"type":"tool_use","id":"` + id + `","name":"` + name + `","input":` + input + `}]}}` + "\n"
}

func toolResult(id string) string {
	return `{"type":"user","cwd":"/repo","message":{"content":[{"type":"tool_result","tool_use_id":"` + id + `","content":"ok"}]}}` + "\n"
}

func feedLines(t *testing.T, tr *Transcript, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(tr.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(strings.Join(lines, ""))
	f.Close()
	if err := tr.Feed(); err != nil {
		t.Fatal(err)
	}
}

func TestActivity(t *testing.T) {
	tr := &Transcript{ID: "s", Path: filepath.Join(t.TempDir(), "s.jsonl")}
	want := func(step string, verb, detail string) {
		t.Helper()
		if tr.Activity != (Activity{verb, detail}) {
			t.Errorf("%s: got %+v, want %s %q", step, tr.Activity, verb, detail)
		}
	}
	feedLines(t, tr, `{"type":"user","cwd":"/repo","message":{"content":"run the tests"}}`+"\n")
	want("a prompt alone", "", "")

	feedLines(t, tr, toolUse("t1", "Bash", `{"command":"  go test ./...\nmore"}`))
	want("bash", "running", "go test ./...")
	feedLines(t, tr, toolResult("t1"))
	want("its result", "", "")

	for _, c := range []struct{ name, input, verb, detail string }{
		{"Edit", `{"file_path":"/repo/internal/ui/app.go"}`, "editing", "internal/ui/app.go"},
		{"Write", `{"file_path":"/elsewhere/notes.md"}`, "editing", "notes.md"},
		{"MultiEdit", `{"file_path":"rel.go"}`, "editing", "rel.go"},
		{"NotebookEdit", `{"notebook_path":"/repo/a.ipynb"}`, "editing", "a.ipynb"},
		{"Read", `{"file_path":"/repo/README.md"}`, "reading", "README.md"},
		{"LS", `{"path":"/repo/internal"}`, "reading", "internal"},
		{"Grep", `{"pattern":"func main"}`, "reading", "func main"},
		{"Glob", `{"pattern":"**/*.go"}`, "reading", "**/*.go"},
		{"WebFetch", `{"url":"https://docs.example.org/page"}`, "reading", "docs.example.org"},
		{"WebFetch", `{"url":"not a url"}`, "reading", "not a url"},
		{"WebSearch", `{"query":"bubbletea tick"}`, "reading", "bubbletea tick"},
		{"Task", `{"description":"look around"}`, "delegating", "look around"},
		{"mcp__claude_ai_Atlassian__getJiraIssue", `{}`, "working", "getJiraIssue"},
		{"TodoWrite", `{}`, "working", "TodoWrite"},
		{"Read", `{}`, "reading", ""},
	} {
		feedLines(t, tr, toolUse("x", c.name, c.input))
		want(c.name, c.verb, c.detail)
		feedLines(t, tr, toolResult("x"))
	}

	feedLines(t, tr, toolUse("a1", "Agent", `{"description":"one"}`), toolUse("a2", "Agent", `{"description":"two"}`))
	want("parallel agents", "delegating", "to 2 agents")
	feedLines(t, tr, toolResult("a1"))
	want("one agent back", "delegating", "two")
	feedLines(t, tr, toolUse("r", "Read", `{"file_path":"/repo/go.mod"}`))
	want("newest call wins", "reading", "go.mod")
	feedLines(t, tr, toolResult("nope"))
	want("an unknown result", "reading", "go.mod")

	feedLines(t, tr, `{"type":"user","message":{"content":[{"type":"text","text":"[Request interrupted by user]"}]}}`+"\n")
	want("an interruption ends them", "", "")
	feedLines(t, tr, toolUse("b", "Bash", `{"command":"make"}`), `{"type":"user","message":{"content":"stop"}}`+"\n")
	want("a prompt ends them", "", "")
	feedLines(t, tr, toolUse("s", "Bash", `{"command":"make"}`), `{"type":"user","isSidechain":true,"message":{"content":"sub"}}`+"\n")
	want("sidechains don't count", "running", "make")
	feedLines(t, tr, `{"type":"assistant","message":{"model":"claude-opus-5-5","content":"plain"}}`+"\n", `{"type":"user","message":{}}`+"\n")
	want("odd content changes nothing", "running", "make")
}

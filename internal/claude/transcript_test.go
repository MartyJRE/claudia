package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

const fixture = `{"type":"mode","mode":"normal","sessionId":"s1"}
{"type":"api-request-blob","sessionId":"s1","message":{"model":"claude-haiku-4-5"}}
{"type":"user","cwd":"/repo","gitBranch":"feat/ABC-123/thing","isSidechain":false,"timestamp":"2026-10-29T10:00:00Z","message":{"content":"fix the thing\nplease"}}
{"type":"assistant","cwd":"/repo","gitBranch":"feat/ABC-123/thing","requestId":"r1","timestamp":"2026-10-29T10:00:05Z","message":{"model":"claude-opus-5-5","usage":{"input_tokens":10,"output_tokens":20}}}
{"type":"assistant","cwd":"/repo","gitBranch":"feat/ABC-123/thing","requestId":"r1","timestamp":"2026-10-29T10:00:06Z","message":{"model":"claude-opus-5-5","usage":{"input_tokens":10,"output_tokens":20}}}
{"type":"assistant","isSidechain":true,"requestId":"r2","message":{"model":"claude-haiku-4-5"}}
{"type":"assistant","requestId":"r3","message":{"model":"<synthetic>"}}
{"type":"pr-link","sessionId":"s1","prNumber":7,"prUrl":"https://gitlab/x/-/merge_requests/7","prRepository":"x"}
{"type":"ai-title","aiTitle":"fix-thing","sessionId":"s1"}
{"type":"user","cwd":"/repo","gitBranch":"devel","timestamp":"2026-10-29T11:00:00Z","message":{"content":"push it"}}
`

func TestFeed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s1.jsonl")
	// The trailing partial line must wait for the next Feed.
	partial := `{"type":"custom-title","customTitle":"mine"`
	if err := os.WriteFile(path, []byte(fixture+partial), 0o600); err != nil {
		t.Fatal(err)
	}
	tr := &Transcript{ID: "s1", Path: path}
	if err := tr.Feed(); err != nil {
		t.Fatal(err)
	}

	if tr.Title() != "fix-thing" {
		t.Errorf("title = %q", tr.Title())
	}
	if tr.Model != "claude-opus-5-5" || len(tr.Models) != 1 {
		t.Errorf("model = %q %v", tr.Model, tr.Models)
	}
	if tr.Usage.Requests != 1 || tr.Usage.Output != 20 {
		t.Errorf("usage = %+v", tr.Usage)
	}
	if tr.Branch != "devel" || tr.TicketBranch != "feat/ABC-123/thing" {
		t.Errorf("branch = %q, ticket branch = %q", tr.Branch, tr.TicketBranch)
	}
	if len(tr.PRs) != 1 || tr.PRs[0].Number != 7 {
		t.Errorf("prs = %+v", tr.PRs)
	}
	if s := (Session{ID: "s1", Transcript: tr}); s.Ticket() != "ABC-123" {
		t.Errorf("ticket = %q", s.Ticket())
	}

	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`,"sessionId":"s1"}` + "\n")
	f.Close()
	if err := tr.Feed(); err != nil {
		t.Fatal(err)
	}
	if tr.Title() != "mine" {
		t.Errorf("title after append = %q", tr.Title())
	}
}

func TestTicketFrom(t *testing.T) {
	for in, want := range map[string]string{
		"feat/abc-12662/spot": "ABC-12662",
		"ABC-1 fix":           "ABC-1",
		"devel":               "",
		"fabc-12":             "",
	} {
		if got := TicketFrom(in); got != want {
			t.Errorf("TicketFrom(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	var body string
	line := func(typ, content string) {
		body += `{"type":"` + typ + `","message":{"content":` + content + `}}` + "\n"
	}
	line("user", `"first ask"`)
	for i := range 5 {
		line("assistant", `[{"type":"text","text":"part `+string(rune('a'+i))+`"},{"type":"tool_use","name":"Bash"}]`)
		line("user", `[{"type":"tool_result","content":"ok"}]`)
	}
	line("assistant", `[{"type":"text","text":"Want me to commit?"}]`)
	line("user", `"<command-name>/clear</command-name>"`)
	line("user", `[{"type":"text","text":"yes, `+strings.Repeat("x", 2000)+`"},{"type":"image"}]`)
	line("user", `"q2"`)
	line("assistant", `[{"type":"text","text":"r2"}]`)
	line("user", `"q3"`)
	line("assistant", `[{"type":"text","text":"r3"}]`)
	os.WriteFile(path, []byte(body), 0o600)
	tr := &Transcript{Path: path}
	if err := tr.Feed(); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, x := range tr.Tail {
		got = append(got, x.Role[:1]+":"+x.Text[:min(len(x.Text), 12)])
	}
	if strings.Join(got, " ") != "a:part a\npart  u:…xxxxxxxxx u:q2 a:r2 u:q3 a:r3" {
		t.Errorf("tail: %q", got)
	}
	if !strings.HasSuffix(tr.Tail[0].Text, "Want me to commit?") || len([]rune(tr.Tail[1].Text)) != 1501 {
		t.Errorf("a reply over tool calls is one turn, long text keeps its end: %+v", tr.Tail[:2])
	}
}

func TestAddTurn(t *testing.T) {
	tr := &Transcript{}
	tr.addTurn("user", "")
	tr.addTurn("assistant", "")
	if tr.Tail != nil {
		t.Fatalf("empty text adds nothing: %+v", tr.Tail)
	}
	tr.addTurn("user", "a")
	tr.addTurn("user", "b")
	tr.addTurn("assistant", "c")
	tr.addTurn("assistant", "d")
	want := []Turn{{"user", "a"}, {"user", "b"}, {"assistant", "c\nd"}}
	if !slices.Equal(tr.Tail, want) {
		t.Errorf("prompts stay apart, a reply joins up: %+v", tr.Tail)
	}
	for i := range 10 {
		tr.addTurn([]string{"user", "assistant"}[i%2], strconv.Itoa(i))
	}
	want = []Turn{{"user", "4"}, {"assistant", "5"}, {"user", "6"}, {"assistant", "7"}, {"user", "8"}, {"assistant", "9"}}
	if !slices.Equal(tr.Tail, want) {
		t.Errorf("the last %d turns kept: %+v", tailTurns, tr.Tail)
	}
}

func TestClip(t *testing.T) {
	exact := strings.Repeat("é", turnChars)
	if clip(exact) != exact || clip("") != "" {
		t.Error("text at the limit stays whole")
	}
	got := clip("drop me " + strings.Repeat("ž", turnChars-3) + "end")
	if want := "…" + strings.Repeat("ž", turnChars-3) + "end"; got != want || !utf8.ValidString(got) {
		t.Errorf("clipped on runes, keeping the end: %d runes", utf8.RuneCountInString(got))
	}
	tr := &Transcript{}
	tr.addTurn("assistant", strings.Repeat("a", turnChars))
	tr.addTurn("assistant", "question?")
	if s := tr.Tail[0].Text; utf8.RuneCountInString(s) != turnChars+1 || !strings.HasPrefix(s, "…a") || !strings.HasSuffix(s, "a\nquestion?") {
		t.Errorf("a joined reply is clipped again: %q", s[:10])
	}
}

func TestReplyText(t *testing.T) {
	for in, want := range map[string]string{
		`"a plain prompt"`: "",
		`not json`:         "",
		`[]`:               "",
		`[{"type":"tool_use","name":"Bash"},{"type":"tool_result","content":"ok"}]`:                                      "",
		`[{"type":"text","text":"  one \n"},{"type":"text","text":"   "},{"type":"image"},{"type":"text","text":"two"}]`: "one\ntwo",
		`[{"type":"thinking","text":"hmm"},{"type":"text","text":"done"}]`:                                               "done",
	} {
		if got := replyText(json.RawMessage(in)); got != want {
			t.Errorf("replyText(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestTailSkipsSidechainsAndSynthetic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	os.WriteFile(path, []byte(`{"type":"user","message":{"content":"ask"}}
{"type":"assistant","isSidechain":true,"message":{"content":[{"type":"text","text":"subagent"}]}}
{"type":"assistant","message":{"model":"<synthetic>","content":[{"type":"text","text":"No response requested."}]}}
{"type":"user","message":{"content":[{"type":"tool_result","content":"ok"}]}}
{"type":"user","message":{"content":"  <local-command-stdout>x</local-command-stdout>"}}
{"type":"assistant","message":{"content":[{"type":"text","text":"answer"}]}}
`), 0o600)
	tr := &Transcript{Path: path}
	if err := tr.Feed(); err != nil {
		t.Fatal(err)
	}
	if want := []Turn{{"user", "ask"}, {"assistant", "answer"}}; !slices.Equal(tr.Tail, want) {
		t.Errorf("tail %+v", tr.Tail)
	}
}

func TestPlan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	body := `{"parentUuid":"a","attachment":{"type":"plan_mode","planFilePath":"/plans/first.md","planExists":false},"type":"attachment"}` + "\n" +
		`{"parentUuid":"b","attachment":{"type":"deferred_tools_record"},"type":"attachment"}` + "\n" +
		`{"type":"assistant","message":{"content":[{"type":"text","text":"here"},{"type":"tool_use","name":"ExitPlanMode","input":{"plan":"# Plan\n1. do it"}}]}}` + "\n"
	os.WriteFile(path, []byte(body), 0o600)
	tr := &Transcript{Path: path}
	if err := tr.Feed(); err != nil {
		t.Fatal(err)
	}
	if tr.PlanPath != "/plans/first.md" || tr.Plan != "# Plan\n1. do it" {
		t.Fatalf("plan path %q, plan %q", tr.PlanPath, tr.Plan)
	}
	add := func(lines ...string) {
		f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		f.WriteString(strings.Join(lines, "\n") + "\n")
		f.Close()
		tr.Feed()
	}
	add(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"ExitPlanMode","input":{"plan":"# Plan v2","planFilePath":"/plans/second.md"}},{"type":"tool_use","name":"Bash","input":{"plan":"not a plan"}}]}}`)
	if tr.PlanPath != "/plans/second.md" || tr.Plan != "# Plan v2" {
		t.Errorf("the latest ExitPlanMode wins: %q %q", tr.PlanPath, tr.Plan)
	}
	add(`{"type":"assistant","message":{"content":"plain text reply"}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"ExitPlanMode","input":{}}]}}`,
		`{"parentUuid":"c","attachment":{"type":"plan_mode"},"type":"attachment"}`,
		`{"type":"assistant","isSidechain":true,"message":{"content":[{"type":"tool_use","name":"ExitPlanMode","input":{"plan":"a subagent's"}}]}}`)
	if tr.PlanPath != "/plans/second.md" || tr.Plan != "# Plan v2" {
		t.Errorf("entries without a plan, and subagents', keep the last one: %q %q", tr.PlanPath, tr.Plan)
	}
}

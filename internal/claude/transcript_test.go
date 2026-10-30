package claude

import (
	"os"
	"path/filepath"
	"testing"
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

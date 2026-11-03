package claude

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/config"
)

func withConfig(t *testing.T, c config.Config) {
	old := siteConfig
	siteConfig = func() config.Config { return c }
	ticketOnce, ticketRe = sync.Once{}, nil
	t.Cleanup(func() {
		siteConfig = old
		ticketOnce, ticketRe = sync.Once{}, nil
	})
}

func TestTicketRegexpWithoutKeys(t *testing.T) {
	withConfig(t, config.Config{})
	if re := TicketRegexp(); re.String() != `\b([A-Z][A-Z0-9]+)-(\d+)\b` {
		t.Errorf("got %s", re)
	}
	if got := TicketFrom("feat/PAY-7/x feat/abc-1"); got != "PAY-7" {
		t.Errorf("got %q", got)
	}
	if got := TicketFrom("feat/abc-1"); got != "" {
		t.Errorf("lower case without keys: %q", got)
	}
}

func TestDefaultProfilesConfigured(t *testing.T) {
	dir := t.TempDir()
	withConfig(t, config.Config{Profiles: map[string]string{"work": dir, "gone": filepath.Join(dir, "missing")}})
	if got := DefaultProfiles(); len(got) != 1 || got[0] != (Profile{"work", dir}) {
		t.Errorf("got %+v", got)
	}
}

func TestReadRegistrySkipsUnreadable(t *testing.T) {
	dir := t.TempDir()
	writeLive(t, dir, Live{PID: os.Getpid(), SessionID: "live"})
	os.Mkdir(filepath.Join(dir, "sessions", "dir.json"), 0o755)
	if got := ReadRegistry(dir); len(got) != 1 || got[0].SessionID != "live" {
		t.Errorf("got %+v", got)
	}
}

func TestSessionsSkipsDanglingTranscript(t *testing.T) {
	dir := t.TempDir()
	writeTranscript(t, dir, "repo/aaaaaaaa-real.jsonl", `{"type":"user","message":{"content":"hi"}}`+"\n")
	os.Symlink(filepath.Join(dir, "nowhere"), filepath.Join(dir, "projects", "repo", "bbbbbbbb-gone.jsonl"))
	got := NewStore([]Profile{{Name: "p", Dir: dir}}, time.Hour).Sessions()
	if len(got) != 1 || got[0].ID != "aaaaaaaa-real" {
		t.Errorf("got %+v", got)
	}
}

func TestFeedMissingAndUnreadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	tr := &Transcript{Path: path}
	if err := tr.Feed(); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing: %v", err)
	}
	os.WriteFile(path, []byte(fixture), 0o000)
	if err := tr.Feed(); !errors.Is(err, os.ErrPermission) {
		t.Errorf("unreadable: %v", err)
	}
}

// fifo makes a named pipe with a writer waiting on it, so opening it doesn't
// block; seeking on it fails.
func fifo(t *testing.T, path string) {
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if w, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil {
			w.Close()
		}
	}()
	t.Cleanup(func() { <-done })
}

func TestFeedUnseekable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	fifo(t, path)
	tr := &Transcript{Path: path}
	if err := tr.Feed(); !errors.Is(err, syscall.ESPIPE) {
		t.Errorf("got %v", err)
	}
}

func TestFeedShrunkStartsOver(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	os.WriteFile(path, []byte(fixture), 0o600)
	tr := &Transcript{ID: "s1", Path: path}
	if err := tr.Feed(); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path, []byte(`{"type":"custom-title","customTitle":"fresh"}`+"\n"), 0o600)
	if err := tr.Feed(); err != nil {
		t.Fatal(err)
	}
	if tr.Title() != "fresh" || tr.AITitle != "" || tr.Usage.Requests != 0 || tr.ID != "s1" || tr.Path != path {
		t.Errorf("got %+v", tr)
	}
}

func TestFeedOddLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	body := "garbage\n" +
		`{"type":"last-prompt","lastPrompt":"ship it"}` + "\n" +
		`{"type":"pr-link","prNumber":7,"prUrl":"u7"}` + "\n" +
		`{"type":"pr-link","prNumber":7,"prUrl":"u7"}` + "\n" +
		`{"type":"user","message":{"content":[{"type":"text","text":"block"}]}}` + "\n" +
		`{"type":"user","message":{"content":"<command-name>/clear</command-name>"}}` + "\n" +
		`{"type":"user","message":{"content":"  real one  "}}` + "\n" +
		// A skipped line still being written stays for the next Feed.
		`{"type":"attachment","x":"unfinished`
	os.WriteFile(path, []byte(body), 0o600)
	tr := &Transcript{Path: path}
	if err := tr.Feed(); err != nil {
		t.Fatal(err)
	}
	if tr.LastPrompt != "ship it" || len(tr.PRs) != 1 || tr.FirstPrompt != "real one" {
		t.Errorf("got %+v", tr)
	}
	if st, _ := os.Stat(path); tr.offset != st.Size()-int64(len(`{"type":"attachment","x":"unfinished`)) {
		t.Errorf("offset %d of %d", tr.offset, st.Size())
	}
}

func TestIgnoreEOF(t *testing.T) {
	boom := errors.New("boom")
	if ignoreEOF(nil) != nil || ignoreEOF(io.EOF) != nil || ignoreEOF(boom) != boom {
		t.Error("ignoreEOF")
	}
}

func TestUsageScannerOddFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeTranscript(t, dir, "repo/ok.jsonl",
		`{"type":"assistant","message":{"model":"m"}}`+"\n"+
			usageLineJSON(now, "m1", "r1", "claude-opus-5-5", `{"output_tokens":4}`)+
			// Unfinished lines, skipped or not, wait for the next scan.
			`{"type":"attachment","x":"unfinished`)
	writeTranscript(t, dir, "repo/partial.jsonl", usageLineJSON(now, "m2", "r2", "claude-opus-5-5", `{"output_tokens":8}`)[:40])
	locked := writeTranscript(t, dir, "repo/locked.jsonl", usageLineJSON(now, "m3", "r3", "claude-opus-5-5", `{"output_tokens":16}`))
	os.Chmod(locked, 0o000)
	fifo(t, filepath.Join(dir, "projects", "repo", "pipe.jsonl"))
	got := NewUsageScanner([]Profile{{Dir: dir}}, 1).Scan()
	if len(got) != 1 || got[0].Output != 4 || got[0].Requests != 1 {
		t.Errorf("got %+v", got)
	}
}

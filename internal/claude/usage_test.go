package claude

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPriceOf(t *testing.T) {
	for model, want := range map[string]float64{
		"claude-opus-5-5":          4,
		"claude-opus-5-5[1m]":      4,
		"claude-opus-5-20260401":   5,
		"claude-haiku-4-5-2025":    1,
		"claude-sonnet-5-5-latest": 2,
	} {
		p, ok := PriceOf(model)
		if !ok || p.In != want {
			t.Errorf("%s: %+v %v", model, p, ok)
		}
	}
	if _, ok := PriceOf("gpt-5"); ok {
		t.Error("unknown model priced")
	}
	p, _ := PriceOf("claude-opus-5")
	if p.Write5m != 6.25 || p.Write1h != 10 || p.Out != 25 || p.Read != 0.5 {
		t.Errorf("cache prices %+v", p)
	}
}

func TestTokens(t *testing.T) {
	a := Tokens{Requests: 1, Input: 1_000_000, Output: 1_000_000}
	a.Add(Tokens{Requests: 2, Read: 1_000_000, Write5m: 1_000_000, Write1h: 1_000_000})
	if a.Requests != 3 || a.Total() != 5_000_000 {
		t.Errorf("%+v total %d", a, a.Total())
	}
	cost, ok := a.Cost("claude-opus-5")
	if want := 5 + 25 + 0.5 + 6.25 + 10.0; !ok || math.Abs(cost-want) > 1e-9 {
		t.Errorf("cost %v want %v", cost, want)
	}
	if _, ok := a.Cost("mystery"); ok {
		t.Error("cost of unknown model")
	}
}

func usageLineJSON(ts time.Time, msgID, reqID, model, usage string) string {
	return `{"type":"assistant","requestId":"` + reqID + `","timestamp":"` + ts.UTC().Format(time.RFC3339) +
		`","message":{"id":"` + msgID + `","model":"` + model + `","usage":` + usage + `}}` + "\n"
}

func writeTranscript(t *testing.T, dir, rel, body string) string {
	t.Helper()
	p := filepath.Join(dir, "projects", rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestUsageScanner(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	yesterday := now.AddDate(0, 0, -1)
	today, yday := now.Format("2006-01-02"), yesterday.Format("2006-01-02")
	body := `{"type":"api-request-blob","usage":"skip me"}` + "\n" +
		usageLineJSON(now, "m1", "r1", "claude-opus-5-5", `{"input_tokens":10,"output_tokens":100,"cache_read_input_tokens":1000,"cache_creation_input_tokens":50}`) +
		// The same response's second content block.
		usageLineJSON(now, "m1", "r1", "claude-opus-5-5", `{"input_tokens":10,"output_tokens":100}`) +
		usageLineJSON(now, "m2", "r2", "claude-opus-5-5", `{"input_tokens":1,"output_tokens":2,"cache_creation_input_tokens":30,"cache_creation":{"ephemeral_5m_input_tokens":10,"ephemeral_1h_input_tokens":20}}`) +
		usageLineJSON(now, "m3", "r3", "<synthetic>", `{"output_tokens":9}`) +
		usageLineJSON(now, "", "", "claude-opus-5-5", `{"output_tokens":9}`) +
		`{"type":"user","message":{"usage":{"output_tokens":9}}}` + "\n" +
		usageLineJSON(yesterday, "m4", "r4", "claude-haiku-4-5", `{"output_tokens":7}`)
	writeTranscript(t, dir, "repo/s1.jsonl", body)
	// A resumed session copies earlier lines; a subagent transcript lives deeper.
	writeTranscript(t, dir, "repo/s2.jsonl", usageLineJSON(now, "m1", "r1", "claude-opus-5-5", `{"output_tokens":100}`))
	writeTranscript(t, dir, "repo/s1/subagents/a.jsonl", usageLineJSON(now, "m5", "r5", "claude-haiku-4-5", `{"output_tokens":3}`))
	writeTranscript(t, dir, "repo/notes.txt", usageLineJSON(now, "m6", "r6", "claude-haiku-4-5", `{"output_tokens":1000}`))

	s := NewUsageScanner([]Profile{{Name: "p", Dir: dir}}, 7)
	got := s.Scan()
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Day != today || got[0].Model != "claude-opus-5-5" {
		t.Errorf("first row %+v", got[0])
	}
	want := Tokens{Requests: 2, Input: 11, Output: 102, Read: 1000, Write5m: 60, Write1h: 20}
	if got[0].Tokens != want {
		t.Errorf("opus today %+v want %+v", got[0].Tokens, want)
	}
	if got[1].Day != today || got[1].Model != "claude-haiku-4-5" || got[1].Output != 3 {
		t.Errorf("subagent row %+v", got[1])
	}
	if got[2].Day != yday || got[2].Output != 7 {
		t.Errorf("yesterday row %+v", got[2])
	}

	// Appending is picked up incrementally without recounting.
	f, err := os.OpenFile(filepath.Join(dir, "projects", "repo", "s1.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(usageLineJSON(now, "m7", "r7", "claude-opus-5-5", `{"output_tokens":1}`))
	f.Close()
	got = s.Scan()
	if got[0].Requests != 3 || got[0].Output != 103 {
		t.Errorf("after append %+v", got[0])
	}
	if again := s.Scan(); again[0] != got[0] {
		t.Errorf("unchanged files recounted: %+v", again[0])
	}
}

func TestUsageScannerRewrite(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	p := writeTranscript(t, dir, "repo/s1.jsonl",
		usageLineJSON(now, "m1", "r1", "claude-opus-5-5", `{"output_tokens":100}`)+
			usageLineJSON(now, "m2", "r2", "claude-opus-5-5", `{"output_tokens":100}`))
	s := NewUsageScanner([]Profile{{Dir: dir}}, 1)
	if got := s.Scan(); len(got) != 1 || got[0].Output != 200 {
		t.Fatalf("got %+v", got)
	}
	if err := os.WriteFile(p, []byte(usageLineJSON(now, "m3", "r3", "claude-opus-5-5", `{"output_tokens":5}`)), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := s.Scan(); len(got) != 1 || got[0].Output != 5 || got[0].Requests != 1 {
		t.Errorf("after shrink %+v", got)
	}
}

func TestUsageScannerSkipsOldFiles(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().AddDate(0, 0, -10)
	p := writeTranscript(t, dir, "repo/old.jsonl", usageLineJSON(old, "m1", "r1", "claude-opus-5-5", `{"output_tokens":1}`))
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	// A recent file whose lines are older than the window still drops them.
	writeTranscript(t, dir, "repo/new.jsonl", usageLineJSON(old, "m2", "r2", "claude-opus-5-5", `{"output_tokens":1}`))
	if got := NewUsageScanner([]Profile{{Dir: dir}}, 3).Scan(); len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}

func TestReadLimits(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "limits.json")
	os.WriteFile(p, []byte(`{"at": 1762160000, "rate_limits": {
		"five_hour": {"used_percentage": 42.5, "resets_at": 1762170000},
		"seven_day": {"used_percentage": 10, "resets_at": "2026-11-05T10:00:00Z"}}}`), 0o600)
	l, ok := ReadLimits(p)
	if !ok || l.FiveHour == nil || l.FiveHour.UsedPercentage != 42.5 || l.SpendLimit != nil {
		t.Fatalf("got %+v %v", l, ok)
	}
	if !l.At.Equal(time.Unix(1762160000, 0)) {
		t.Errorf("at %v", l.At)
	}
	if !l.FiveHour.Resets().Equal(time.Unix(1762170000, 0)) {
		t.Errorf("epoch reset %v", l.FiveHour.Resets())
	}
	if want := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC); !l.SevenDay.Resets().Equal(want) {
		t.Errorf("rfc3339 reset %v", l.SevenDay.Resets())
	}
	if !(Window{}).Resets().IsZero() {
		t.Error("missing reset isn't zero")
	}

	if _, ok := ReadLimits(filepath.Join(dir, "missing.json")); ok {
		t.Error("missing file read")
	}
	os.WriteFile(p, []byte("nope"), 0o600)
	if _, ok := ReadLimits(p); ok {
		t.Error("broken file read")
	}
}

func TestUsageScannerHugeSkippedLine(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	big := `{"type":"file-history-snapshot","blob":"` + strings.Repeat("x", 600*1024) + `"}` + "\n"
	writeTranscript(t, dir, "repo/s1.jsonl", big+usageLineJSON(now, "m1", "r1", "claude-opus-5-5", `{"output_tokens":4}`))
	if got := NewUsageScanner([]Profile{{Dir: dir}}, 1).Scan(); len(got) != 1 || got[0].Output != 4 {
		t.Errorf("got %+v", got)
	}
}

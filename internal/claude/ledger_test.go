package claude

import (
	"math"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/state"
)

func branchLineJSON(ts time.Time, branch, msgID, reqID, model, usage string) string {
	return `{"type":"assistant","gitBranch":"` + branch + `","requestId":"` + reqID + `","timestamp":"` + ts.UTC().Format(time.RFC3339) +
		`","message":{"id":"` + msgID + `","model":"` + model + `","usage":` + usage + `}}` + "\n"
}

func TestUsageScannerSessions(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	old := now.AddDate(0, -3, 0)
	writeTranscript(t, dir, "repo/s1.jsonl",
		branchLineJSON(now.Add(-time.Hour), "devel", "m1", "r1", "claude-opus-5", `{"output_tokens":1000000}`)+
			branchLineJSON(now, "feat/ABC-7/x", "m2", "r2", "claude-opus-5", `{"input_tokens":1000000}`)+
			branchLineJSON(now, "devel", "m3", "r3", "mystery-model", `{"output_tokens":5}`))
	// A subagent counts toward its parent; a fork copies m1 and only adds m4.
	writeTranscript(t, dir, "repo/s1/subagents/agent-1.jsonl", branchLineJSON(now, "", "m5", "r5", "claude-opus-5", `{"output_tokens":1000000}`))
	writeTranscript(t, dir, "repo/s2.jsonl",
		branchLineJSON(now, "feat/ABC-8/y", "m1", "r1", "claude-opus-5", `{"output_tokens":1000000}`)+
			branchLineJSON(now, "feat/ABC-8/y", "m4", "r4", "claude-haiku-4-5", `{"output_tokens":1000000}`))
	// Older than the window: still counted by the first scan.
	p := writeTranscript(t, dir, "repo/s3.jsonl", branchLineJSON(old, "", "m6", "r6", "claude-haiku-4-5", `{"input_tokens":1000000}`))
	os.Chtimes(p, old, old)
	// A fork that only copied lines cost nothing of its own.
	writeTranscript(t, dir, "repo/s4.jsonl", branchLineJSON(now, "feat/ABC-9/z", "m6", "r6", "claude-haiku-4-5", `{"input_tokens":1000000}`))

	s := NewUsageScanner([]Profile{{Name: "work", Dir: dir}}, 7)
	if s.Sessions() != nil {
		t.Error("sessions before a scan")
	}
	s.Scan()
	got := s.Sessions()
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	s1, s2, s3 := got[0], got[1], got[2]
	if s1.ID != "s1" || s1.Ticket != "ABC-7" || s1.Profile != "work" || !s1.Unknown || math.Abs(s1.USD-55) > 1e-9 {
		t.Errorf("s1 %+v", s1)
	}
	if s1.Models["claude-opus-5"].Requests != 3 || s1.Tokens != 3_000_005 {
		t.Errorf("s1 tokens %+v", s1)
	}
	if !s1.First.Equal(now.Add(-time.Hour).Truncate(time.Second)) || !s1.Last.Equal(now.Truncate(time.Second)) {
		t.Errorf("s1 span %v %v", s1.First, s1.Last)
	}
	if s2.ID != "s2" || s2.Ticket != "ABC-8" || s2.USD != 5 || len(s2.Models) != 1 {
		t.Errorf("fork %+v", s2)
	}
	if s3.ID != "s3" || s3.USD != 1 {
		t.Errorf("old file %+v", s3)
	}
	if NewUsageScanner(nil, 1).Sessions() != nil {
		t.Error("an empty scanner has sessions")
	}
	var nilScanner *UsageScanner
	if nilScanner.Sessions() != nil {
		t.Error("nil scanner")
	}
}

func TestUsageScannerLaterScansKeepToWindow(t *testing.T) {
	dir := t.TempDir()
	s := NewUsageScanner([]Profile{{Dir: dir}}, 7)
	s.Scan()
	old := time.Now().AddDate(0, -3, 0)
	p := writeTranscript(t, dir, "repo/late.jsonl", branchLineJSON(old, "", "m1", "r1", "claude-haiku-4-5", `{"input_tokens":1}`))
	os.Chtimes(p, old, old)
	s.Scan()
	if got := s.Sessions(); len(got) != 0 {
		t.Errorf("an old file read after the first scan: %+v", got)
	}
}

func cost(id, ticket string, out int, last time.Time) SessionCost {
	return SessionCost{ID: id, Ticket: ticket, Profile: "work", Models: map[string]Tokens{"claude-opus-5": {Requests: 1, Output: out}}, Last: last}.priced()
}

func TestLedger(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	now := time.Now().Truncate(time.Second)
	l := OpenLedger()
	if len(l.Tickets()) != 0 {
		t.Fatal("a new ledger isn't empty")
	}
	if !l.Merge([]SessionCost{cost("a", "ABC-1", 1_000_000, now), cost("b", "ABC-1", 2_000_000, now.Add(-time.Hour)), cost("c", "", 400_000, now)}) {
		t.Error("first merge changed nothing")
	}
	if l.Merge([]SessionCost{cost("a", "ABC-1", 1_000_000, now)}) {
		t.Error("the same scan again is a change")
	}
	// A shorter rescan (the transcript was cleaned up and rewritten) keeps the max.
	if l.Merge([]SessionCost{cost("b", "ABC-1", 10, now)}) {
		t.Error("a smaller rescan replaced the entry")
	}
	// A rescan without a ticket keeps the one already known.
	if !l.Merge([]SessionCost{cost("a", "", 1_500_000, now)}) {
		t.Error("growth isn't a change")
	}
	if got := l.Ticket("ABC-1"); got.Sessions != 2 || got.USD != 87.5 || got.Tokens != 3_500_000 || !got.Last.Equal(now) {
		t.Errorf("ABC-1 %+v", got)
	}

	// A ticket set by hand wins over the branch from then on.
	if !l.SetTicket("b", "ABC-2") || l.SetTicket("b", "ABC-2") || l.SetTicket("zzz", "ABC-2") {
		t.Error("set ticket")
	}
	l.Merge([]SessionCost{cost("b", "ABC-1", 3_000_000, now.Add(-time.Hour))})
	if s := l.Sessions("ABC-2"); len(s) != 1 || s[0].ID != "b" || s[0].USD != 75 {
		t.Errorf("ABC-2 sessions %+v", s)
	}
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}

	again := OpenLedger()
	tickets := again.Tickets()
	var keys []string
	for _, tc := range tickets {
		keys = append(keys, tc.Ticket)
	}
	if !slices.Equal(keys, []string{"ABC-2", "ABC-1", ""}) {
		t.Errorf("tickets by cost %v", keys)
	}
	var ids []string
	for c := range again.All() {
		ids = append(ids, c.ID)
	}
	if len(ids) != 3 || ids[2] != "b" {
		t.Errorf("all, newest first: %v", ids)
	}
	for range again.All() {
		break
	}

	epics := again.Epics(map[string]string{"ABC-1": "ABC-100", "ABC-2": "ABC-100"})
	if len(epics) != 2 || epics[0].Epic != "ABC-100" || epics[0].Sessions != 2 || epics[0].USD != 112.5 || len(epics[0].Tickets) != 2 || epics[1].Epic != "" {
		t.Errorf("epics %+v", epics)
	}
	epics = again.Epics(map[string]string{"ABC-1": "ABC-100", "ABC-2": "ABC-200"})
	if epics[0].Epic != "ABC-200" || epics[1].Epic != "ABC-100" || epics[2].Epic != "" {
		t.Errorf("epics by cost %+v", epics)
	}

	var none *Ledger
	if none.Sessions("ABC-1") != nil || none.Ticket("ABC-1").Sessions != 0 {
		t.Error("nil ledger")
	}
}

func TestLedgerBroken(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	os.MkdirAll(state.Dir(), 0o755)
	os.WriteFile(state.Path(ledgerFile), []byte("null"), 0o600)
	if l := OpenLedger(); l.Merge([]SessionCost{cost("a", "", 1, time.Now())}) != true {
		t.Error("a null ledger can't take entries")
	}
}

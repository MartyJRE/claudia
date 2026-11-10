package runs

import (
	"maps"
	"testing"
	"time"
)

func TestKV(t *testing.T) {
	s := open(t)
	if _, ok := s.Get("autopilot.paused"); ok {
		t.Error("a key that was never set")
	}
	if err := s.Set("autopilot.paused", "1"); err != nil {
		t.Fatal(err)
	}
	_ = s.Set("autopilot.paused", "yes")
	_ = s.Set("plan-ok:ABC-1", "x")
	_ = s.Set("plan-ok:ABC-2", "y")
	if v, ok := s.Get("autopilot.paused"); !ok || v != "yes" {
		t.Errorf("overwritten: %q %v", v, ok)
	}
	if got := s.Prefixed("plan-ok:"); !maps.Equal(got, map[string]string{"ABC-1": "x", "ABC-2": "y"}) {
		t.Errorf("prefixed %v", got)
	}
	if err := s.Set("autopilot.paused", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("autopilot.paused"); ok {
		t.Error("an empty value deletes")
	}
	s.Close()
	if len(s.Prefixed("plan-ok:")) != 0 || len(s.Overrides()) != 0 {
		t.Error("values from a closed store")
	}
}

func TestOverrides(t *testing.T) {
	s := open(t)
	_ = s.SetOverride("ABC-1", AnyStep, "ask", t0)
	_ = s.SetOverride("ABC-1", "merge", "auto", t0)
	_ = s.SetOverride("ABC-1", "merge", "off", t0)
	_ = s.SetOverride("ABC-2", "plan", "off", t0)
	got := s.Overrides()
	if len(got) != 2 || got["ABC-1"][AnyStep] != "ask" || got["ABC-1"]["merge"] != "off" || got["ABC-2"]["plan"] != "off" {
		t.Errorf("overrides %v", got)
	}
	_ = s.SetOverride("ABC-1", "merge", "", t0)
	if got := s.Overrides()["ABC-1"]; len(got) != 1 {
		t.Errorf("cleared one step %v", got)
	}
	_ = s.ClearOverrides("ABC-2")
	if _, ok := s.Overrides()["ABC-2"]; ok {
		t.Error("cleared the card")
	}
	_ = s.SetOverride("ABC-3", "plan", "off", t0)
	// ABC-1 is still on the board 40 days on; ABC-3 left it.
	later := t0.Add(40 * 24 * time.Hour)
	if err := s.PruneOverrides([]string{"ABC-1"}, later, later.Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := s.Overrides(); len(got) != 1 || got["ABC-1"] == nil {
		t.Errorf("pruned %v", got)
	}
	s.Close()
	if s.PruneOverrides(nil, t0, t0) == nil {
		t.Error("pruned a closed store")
	}
}

func TestPruneOverridesErrors(t *testing.T) {
	s := open(t)
	_ = s.SetOverride("ABC-1", "plan", "off", t0)
	if _, err := s.db.Exec(`CREATE TRIGGER no BEFORE UPDATE ON overrides BEGIN SELECT RAISE(ABORT, 'read only'); END`); err != nil {
		t.Fatal(err)
	}
	if s.PruneOverrides([]string{"ABC-1"}, t0, t0) == nil {
		t.Error("touch error")
	}
	if _, err := s.db.Exec(`DROP TRIGGER no; CREATE TRIGGER no BEFORE DELETE ON overrides BEGIN SELECT RAISE(ABORT, 'read only'); END`); err != nil {
		t.Fatal(err)
	}
	if s.PruneOverrides(nil, t0, t0.Add(time.Hour)) == nil {
		t.Error("delete error")
	}
}

func TestSinceAndAutopilotUSD(t *testing.T) {
	s := open(t)
	for _, r := range []Run{
		{ID: "a", Subject: "ABC-1", Step: "plan", Session: "s-a", Started: t0, StartedBy: ByAutopilot},
		{ID: "b", Subject: "ABC-1", Step: "implement", Session: "s-b", Started: t0.Add(time.Hour)},
		{ID: "c", Subject: "ABC-2", Step: "plan", Session: "s-c", Started: t0.Add(-24 * time.Hour), StartedBy: ByAutopilot},
		{ID: "d", Subject: "ABC-2", Step: "qa", Session: "s-a", Started: t0.Add(time.Minute), StartedBy: ByAutopilot},
		{ID: "e", Subject: "ABC-2", Step: "merge", Started: t0.Add(time.Minute), StartedBy: ByAutopilot},
	} {
		if err := s.Start(r); err != nil {
			t.Fatal(err)
		}
	}
	if got := s.Since(t0); len(got) != 4 || got[0].ID != "b" || got[3].StartedBy != ByAutopilot {
		t.Errorf("since %+v", got)
	}
	if _, err := s.db.Exec(`INSERT INTO costs (session, usd) VALUES ('s-a', 2.5), ('s-b', 7), ('s-c', 11)`); err != nil {
		t.Fatal(err)
	}
	// s-a counts once though two runs share it; s-b is mine and s-c was yesterday.
	if usd := s.AutopilotUSD(t0); usd != 2.5 {
		t.Errorf("autopilot spent %v", usd)
	}
}

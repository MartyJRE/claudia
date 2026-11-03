package ui

import (
	"encoding/json"
	"github.com/charmbracelet/lipgloss"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/state"
)

func usageModelViews(t *testing.T) *Model {
	m := newModelViews(t)
	day := func(ago int) string { return time.Now().AddDate(0, 0, -ago).Format("2006-01-02") }
	m.usage = []claude.DayModel{
		{Day: day(10), Model: "claude-haiku-5-5", Tokens: claude.Tokens{Requests: 4, Output: 2_000_000}},
		{Day: day(0), Model: "claude-opus-5-5", Tokens: claude.Tokens{Requests: 7, Input: 1_000_000, Output: 100_000}},
		{Day: day(1), Model: "gpt-x", Tokens: claude.Tokens{Requests: 1, Input: 500}},
		{Day: day(0), Model: "claude-sonnet-5-5", Tokens: claude.Tokens{Requests: 3, Read: 2_000_000, Write5m: 1_000, Write1h: 2_000}},
	}
	m.usageLoaded = true
	m.view = vUsage
	m.refilter()
	return m
}

func TestUsageRows(t *testing.T) {
	m := usageModelViews(t)
	rows := m.vis.usage
	if len(rows) != 3 {
		t.Fatalf("one row per day, got %d", len(rows))
	}
	today := rows[0]
	if today.day != time.Now().Format("2006-01-02") || rows[2].day >= rows[1].day {
		t.Errorf("newest day first: %s %s %s", rows[0].day, rows[1].day, rows[2].day)
	}
	if len(today.models) != 2 || today.tokens.Requests != 10 || today.tokens.Total() != 3_103_000 {
		t.Errorf("today sums its models: %+v", today.tokens)
	}
	if today.cost < 6.4 || today.cost > 6.42 || today.unknown {
		t.Errorf("today's cost %v unknown=%v", today.cost, today.unknown)
	}
	if !rows[1].unknown || rows[1].cost != 0 {
		t.Errorf("an unpriced model is flagged: %+v", rows[1])
	}
}

func TestUsageView(t *testing.T) {
	m := usageModelViews(t)
	s := screenViews(m)
	for _, want := range []string{"API-equivalent cost   today $6.41   last 7 days $6.41", time.Now().Format("January"), "Opus 5.5, Sonnet 5.5", "1.0M", "100k", "3k", "2.0M", "3.1M", "$0.00+?", "$1.00", "━", "priced at API list rates"} {
		if !strings.Contains(s, want) {
			t.Errorf("screen lacks %q:\n%s", want, s)
		}
	}
	d := plainViews(m.detailUsage())
	for _, want := range []string{time.Now().Format("Monday 2 January") + "  ·  10 requests", "Opus 5.5", "in 1.0M", "out 100k", "write 5m/1h 1k/2k", "read 2.0M", "$6.00", "$0.41"} {
		if !strings.Contains(d, want) {
			t.Errorf("detail lacks %q:\n%s", want, d)
		}
	}
	m.cur[vUsage].pos = 1
	if d := plainViews(m.detailUsage()); !strings.Contains(d, "no price") {
		t.Errorf("unpriced model detail:\n%s", d)
	}
}

func TestUsageEmpty(t *testing.T) {
	m := newModelViews(t)
	m.view = vUsage
	m.refilter()
	if !strings.Contains(screenViews(m), "reading transcripts") {
		t.Error("before the first scan")
	}
	m.Update(usageMsg(nil))
	if !m.usageLoaded || m.usageAt.IsZero() || !strings.Contains(screenViews(m), "no usage in the last 31 days") {
		t.Error("after an empty scan")
	}
	m.cur[vUsage].pos = 5
	if strings.Contains(plainViews(m.detailUsage()), "requests") {
		t.Error("no row, no detail")
	}
}

func TestUsageLimits(t *testing.T) {
	m := usageModelViews(t)
	m.store.Profiles = []claude.Profile{{Name: "work", Dir: "/x/.claude-work"}, {Name: "home", Dir: "/x/.claude"}}
	now := time.Now()
	limits := map[string]any{
		"rate_limits": map[string]any{
			"five_hour": map[string]any{"used_percentage": 42, "resets_at": float64(now.Add(2 * time.Hour).Unix())},
			"seven_day": map[string]any{"used_percentage": 91.4, "resets_at": now.Add(72 * time.Hour).Format(time.RFC3339)},
		},
		"at": float64(now.Add(-5 * time.Minute).Unix()),
	}
	b, _ := json.Marshal(limits)
	p := state.Path("limits-.claude-work.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	s := plainViews(m.viewLimits())
	lines := strings.Split(s, "\n")
	work, home := lines[0], lines[1]
	for _, want := range []string{"work", "5h ", "  42%", "resets " + now.Add(2*time.Hour).Format("15:04"), "7d ", "  91%", "resets " + now.Add(72*time.Hour).Format("Mon 15:04"), "as of 5m ago"} {
		if !strings.Contains(work, want) {
			t.Errorf("work limits lack %q: %s", want, work)
		}
	}
	if strings.Contains(work, "extra") {
		t.Error("no spend limit, no bar")
	}
	if !strings.Contains(home, "home") || !strings.Contains(home, "plan limits appear once") {
		t.Errorf("profile without limits: %s", home)
	}
}

func TestLimitBar(t *testing.T) {
	cases := []struct {
		pct  float64
		want string
	}{{0, "   0%"}, {50, "  50%"}, {74.6, "  75%"}, {130, " 130%"}}
	for _, c := range cases {
		s := plainViews(limitBar("5h", claude.Window{UsedPercentage: c.pct}))
		if n := strings.Count(s, "━"); n != 20 || !strings.HasSuffix(s, c.want) {
			t.Errorf("%v%%: %q", c.pct, s)
		}
	}
}

func TestMoney(t *testing.T) {
	for v, want := range map[float64]string{0: "$0.00", 12.5: "$12.50", 999.99: "$999.99", 1500.7: "$1501"} {
		if got := money(v); got != want {
			t.Errorf("money(%v) = %s, want %s", v, got, want)
		}
	}
}

func TestStackedBarByModel(t *testing.T) {
	prev := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	lipgloss.SetColorProfile(0) // termenv.TrueColor

	m := usageModelViews(t)
	today := m.usageRows()[0]
	bar := stackedBar(today, 40)
	if got := []rune(plainViews(bar)); len(got) != 40 {
		t.Errorf("segments add up to the whole bar, got %d", len(got))
	}
	// One coloured run per model: each segment opens with its own escape sequence.
	if n := strings.Count(bar, "\x1b[38;"); n != len(today.models) {
		t.Errorf("want %d coloured segments, got %d: %q", len(today.models), n, bar)
	}
	other := usageRow{cost: 1, models: []claude.DayModel{{Model: "gpt-x", Tokens: claude.Tokens{Input: 1}}}}
	if stackedBar(other, 10) != "" {
		t.Error("models without a price draw nothing")
	}
	if stackedBar(usageRow{}, 10) != "" {
		t.Error("no cost, no bar")
	}
}

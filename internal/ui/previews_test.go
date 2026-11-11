package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/brief"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/state"
	"github.com/MartyJRE/claudia/internal/tmux"
)

// TestWritePreviews renders the README screenshots from demo data:
//
//	CLAUDIA_PREVIEWS=assets go test ./internal/ui -run TestWritePreviews
func TestWritePreviews(t *testing.T) {
	dir := os.Getenv("CLAUDIA_PREVIEWS")
	if dir == "" {
		t.Skip("set CLAUDIA_PREVIEWS=<dir> to write the README screenshots")
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join("..", "..", dir) // tests run in internal/ui; the dir is relative to the repo root
	}
	square := false
	withConfig(t, func(c *config.Config) { c.RoundedPillsRaw = &square })
	prev := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	lipgloss.SetColorProfile(0) // termenv.TrueColor

	m := demoModel(t)
	shots := []struct {
		file string
		v    view
		pick func(*Model) int
	}{
		{"inbox.svg", vInbox, func(m *Model) int {
			return slices.IndexFunc(m.vis.inbox, func(it inboxItem) bool { return it.kind == kReview })
		}},
		{"sessions.svg", vSessions, func(m *Model) int {
			return slices.IndexFunc(m.vis.sessions, func(r srow) bool { return r.isSess && r.sess.live() && r.sess.Live.Status == claude.StatusWaiting })
		}},
		{"mrs.svg", vMine, func(*Model) int { return 0 }},
		{"jira.svg", vJira, func(m *Model) int {
			return slices.IndexFunc(m.vis.jira, func(r jrow) bool { return r.issue.Key == "ABC-412" })
		}},
		{"usage.svg", vUsage, func(*Model) int { return 0 }},
		// Last: what it adds would show in the other views.
		{"board.svg", vBoard, func(m *Model) int {
			demoBoard(m)
			m.boardFocus = true // stepped in, so a card and its detail show
			return m.cur[vBoard].pos
		}},
	}
	for _, s := range shots {
		m.switchView(s.v)
		m.cur[s.v].pos = s.pick(m)
		screen := m.View()
		if r, ok := privateUse(screen); ok {
			t.Fatalf("%s: %U is a Private Use Area glyph that GitHub's fonts don't have", s.file, r)
		}
		svg := screenSVG(screen, m.width, m.height)
		if err := os.WriteFile(filepath.Join(dir, s.file), []byte(svg), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func privateUse(s string) (rune, bool) {
	for _, r := range ansi.Strip(s) {
		if (r >= 0xE000 && r <= 0xF8FF) || r >= 0xF0000 {
			return r, true
		}
	}
	return 0, false
}

// demoModel is claudia on an invented team's busy afternoon.
func demoModel(t *testing.T) *Model {
	t.Helper()
	m := newModelShell(t)
	m.width, m.height = 132, 32
	m.atl, m.atlErr = &atlassian.Client{Base: "https://acme.atlassian.net"}, nil
	m.store = claude.NewStore([]claude.Profile{{Name: "work", Dir: "/demo/.claude-work"}, {Name: "personal", Dir: "/demo/.claude"}}, 0)
	now := time.Now()

	m.gl = demoGitlab(now)
	m.jira = demoJira(now)
	m.conf = atlassian.ConfluenceSnapshot{
		Mentions: []atlassian.Page{{
			ID: "981", Title: "Q4 payments architecture", Space: "ENG", URL: "https://acme.atlassian.net/wiki/spaces/ENG/pages/981",
			Excerpt: "@Jordan Blake can you sign off on the settlement section?", Modified: now.Add(-3 * time.Hour), OnPage: "981",
		}},
		FetchedAt: now,
	}
	m.sessions = demoSessions(now)
	m.usage = demoUsage(now)
	m.usageLoaded, m.usageAt = true, now
	m.brief = brief.Brief{At: now.Add(-25 * time.Minute), Text: "Maya has waited two days on ABC-398: she wants to know whether the refund cutoff stays at 30 days. " +
		"Oskar's payments !231 (settlement batches by currency) has been waiting on your review since yesterday; its pipeline is green. " +
		"Your billing !87 failed its pipeline and has two open threads from Priya and Tom about decimals. " +
		"Start with Maya's question, then review !231."}

	// What I had seen of the MRs before: chips show what moved since.
	for _, mr := range slices.Concat(m.gl.Mine, m.gl.Review[1:2]) {
		old := snapshotOf(mr)
		if mr.IID == 229 {
			old.Notes -= 2
		}
		m.seen[mr.WebURL] = old
	}
	for _, p := range m.store.Profiles {
		writeLimits(t, p, now)
	}
	m.fetchedAt[srcGitlab] = now.Add(-2 * time.Minute)
	m.fetchedAt[srcJira] = now.Add(-2 * time.Minute)
	m.fetchedAt[srcConfluence] = now.Add(-2 * time.Minute)
	m.reindex()
	return m
}

// demoBoard adds what the Board shows beyond the other views: an analysis
// session and what the tickets cost so far.
func demoBoard(m *Model) {
	analysis := demoSession("f7a8b9c0-d1e2-4f3a-b4c5-d6e7f8a9b0c1", "work", "billing", "devel", "Analyse ABC-403 PDF totals", "", "claude-opus-5-5", "", 4*time.Hour, 3*time.Hour)
	analysis.kind = "analysis"
	m.links.SetTicket(analysis.ID, "ABC-403")
	m.sessions = append(m.sessions, analysis)
	opus := func(out, read int) map[string]claude.Tokens {
		return map[string]claude.Tokens{"claude-opus-5-5": {Output: out, Read: read}, "claude-haiku-5-5": {Output: out / 10}}
	}
	m.ledger = claude.OpenLedger()
	m.ledger.Merge([]claude.SessionCost{
		{ID: "7c1e4b2a-5d3f-4e8a-9b61-2f0c8d4a7e19", Ticket: "ABC-412", Models: opus(420_000, 31_000_000)},
		{ID: "4e2d9a1b-7c3f-4b5e-a6d8-9f0e1c2b3a4d", Ticket: "ABC-401", Models: opus(260_000, 18_000_000)},
		{ID: "old-401-analysis", Ticket: "ABC-401", Models: opus(90_000, 5_000_000)},
		{ID: "0f1e2d3c-4b5a-4c6d-9e8f-7a6b5c4d3e2f", Ticket: "ABC-422", Models: opus(40_000, 2_500_000)},
		{ID: analysis.ID, Ticket: "ABC-403", Models: opus(60_000, 4_000_000)},
	})
	m.costs = ticketCosts(m.ledger)
	m.reindex()
}

func writeLimits(t *testing.T, p claude.Profile, now time.Time) {
	t.Helper()
	used := map[string][2]float64{"work": {38, 64}, "personal": {12, 21}}[p.Name]
	l := map[string]any{
		"rate_limits": map[string]any{
			"five_hour": map[string]any{"used_percentage": used[0], "resets_at": float64(now.Add(2*time.Hour + 10*time.Minute).Unix())},
			"seven_day": map[string]any{"used_percentage": used[1], "resets_at": float64(now.Add(3 * 24 * time.Hour).Unix())},
		},
		"at": float64(now.Unix()),
	}
	path := state.Path("limits-" + filepath.Base(p.Dir) + ".json")
	b, _ := json.Marshal(l)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func demoMR(repo string, iid int, title, author, branch string, created, updated time.Duration) gitlab.MR {
	mr := mrShell(repo, iid, title, author, branch, updated)
	mr.WebURL = "https://gitlab.acme.dev/acme/" + repo + "/-/merge_requests/" + strconv.Itoa(iid)
	mr.References = refShell("acme/" + repo + "!" + strconv.Itoa(iid))
	mr.CreatedAt = time.Now().Add(-created)
	mr.TargetBranch = "main"
	mr.UserNotesCount = 4
	mr.DiscussionsResolved = true
	return mr
}

func demoGitlab(now time.Time) gitlab.Snapshot {
	h := time.Hour
	settle := demoMR("payments", 231, "feat(ABC-405): split settlement batches by currency", "oskar.berg", "feat/ABC-405/settlement-currency", 27*h, 40*time.Minute)
	settle.Pipeline = &gitlab.Pipeline{Status: "success"}
	settle.ApprovalsLeft = 1
	retry502 := demoMR("gateway", 142, "fix: retry 502s from the card network", "hana.kral", "fix/card-network-502", 50*time.Minute, 20*time.Minute)
	retry502.Pipeline = &gitlab.Pipeline{Status: "running"}
	retry502.ApprovalsLeft = 2
	wip := demoMR("ledger", 318, "Draft: ledger snapshots per tenant", "tom.reed", "wip/ledger-snapshots", 5*h, 3*h)
	wip.Draft = true

	decimals := demoMR("billing", 87, "feat(ABC-401): invoice line items in decimals", "jordan", "feat/ABC-401/decimal-lines", 30*h, 25*time.Minute)
	decimals.Pipeline = &gitlab.Pipeline{Status: "failed", Failed: []gitlab.Job{{Name: "unit-tests", Stage: "test"}}}
	decimals.Reviewers = []gitlab.User{{Username: "priya.n"}, {Username: "tom.reed"}}
	decimals.ApprovedBy, decimals.ApprovalsLeft = []string{"tom.reed"}, 1
	decimals.DiscussionsResolved = false
	decimals.Threads = []gitlab.Thread{
		{Author: "priya.n", Body: "Should this be a decimal, not a float64? Cents get lost on large invoices.", Path: "internal/invoice/line.go", Line: 42, At: now.Add(-5 * h), Replies: 2, LastBy: "priya.n"},
		{Author: "tom.reed", Body: "Can we keep the old JSON field for one release?", Path: "api/invoice.go", Line: 118, At: now.Add(-4 * h)},
		{Author: "priya.n", Body: "nit: typo", Resolved: true, At: now.Add(-6 * h)},
	}
	refunds := demoMR("payments", 229, "feat(ABC-412): retry refund webhooks with backoff", "jordan", "feat/ABC-412/refund-retries", 6*h, 15*time.Minute)
	refunds.Pipeline = &gitlab.Pipeline{Status: "running"}
	refunds.Reviewers = []gitlab.User{{Username: "oskar.berg"}}
	refunds.ApprovalsLeft = 1
	refunds.MergeStatus = "need_rebase"
	refunds.UserNotesCount = 6
	logs := demoMR("gateway", 140, "chore: structured logs for upstream timeouts", "jordan", "chore/timeout-logs", 26*h, 2*h)
	logs.Pipeline = &gitlab.Pipeline{Status: "success"}
	logs.Reviewers = []gitlab.User{{Username: "hana.kral"}, {Username: "maya.lin"}}
	logs.ApprovedBy, logs.Approved = []string{"hana.kral", "maya.lin"}, true
	logs.Threads = []gitlab.Thread{}
	digest := demoMR("notifications", 55, "Draft: email digest templates", "jordan", "feat/ABC-422/digest", 3*h, 3*h)
	digest.Draft = true
	digest.Reviewers = []gitlab.User{{Username: "maya.lin"}}
	digest.Threads = []gitlab.Thread{}

	rounding := demoMR("ledger", 310, "fix(ABC-377): rounding on multi-currency totals", "jordan", "fix/ABC-377/rounding", 50*h, 20*h)
	rounding.State = "merged"
	rounding.Reviewers = []gitlab.User{{Username: "tom.reed"}}
	rounding.MergePipeline = &gitlab.Pipeline{Status: "success"}
	bump := demoMR("payments", 226, "chore: bump Go to 1.24", "jordan", "chore/go-1.24", 70*h, 44*h)
	bump.State = "merged"
	bump.Reviewers = []gitlab.User{{Username: "oskar.berg"}}

	return gitlab.Snapshot{
		Me:        gitlab.User{Username: "jordan"},
		Review:    []gitlab.MR{settle, retry502, wip},
		Mine:      []gitlab.MR{decimals, refunds, logs, digest},
		Merged:    []gitlab.MR{rounding, bump},
		FetchedAt: now,
	}
}

func demoJira(now time.Time) atlassian.JiraSnapshot {
	s42 := &atlassian.Sprint{ID: 42, Name: "Sprint 42", State: "active"}
	s43 := &atlassian.Sprint{ID: 43, Name: "Sprint 43", State: "future"}
	me := "Jordan Blake"
	return atlassian.JiraSnapshot{
		Sprint: []atlassian.Issue{
			{Key: "ABC-380", Summary: "Refunds v2", Type: "Epic", Status: "In Progress", Category: "indeterminate", Level: 1, Assignee: "Maya Lin"},
			{Key: "ABC-412", Summary: "Retry refund webhooks with backoff", Type: "Story", Status: "In Progress", Category: "indeterminate", Parent: "ABC-380", Sprint: s42, Assignee: me, Mine: true},
			{Key: "ABC-415", Summary: "Backoff limits per merchant", Type: "Subtask", Status: "To Do", Category: "new", Parent: "ABC-412", Level: -1, Sprint: s42, Assignee: me, Mine: true},
			{Key: "ABC-398", Summary: "Refund cutoff window", Type: "Story", Status: "Code Review", Category: "indeterminate", Parent: "ABC-380", Sprint: s42, Assignee: me, Mine: true},
			{Key: "ABC-390", Summary: "Invoices in decimals", Type: "Epic", Status: "In Progress", Category: "indeterminate", Level: 1, Assignee: "Priya Nair"},
			{Key: "ABC-401", Summary: "Invoice line items in decimals", Type: "Story", Status: "In Progress", Category: "indeterminate", Parent: "ABC-390", Sprint: s42, Assignee: me, Mine: true},
			{Key: "ABC-403", Summary: "PDF totals off by a cent", Type: "Bug", Status: "To Do", Category: "new", Parent: "ABC-390", Sprint: s42, Assignee: me, Mine: true},
			{Key: "ABC-405", Summary: "Split settlement batches by currency", Type: "Story", Status: "Code Review", Category: "indeterminate", Sprint: s42, Assignee: "Oskar Berg"},
			{Key: "ABC-377", Summary: "Rounding on multi-currency totals", Type: "Bug", Status: "Done", Category: "done", Sprint: s42, Assignee: me, Mine: true},
			{Key: "ABC-420", Summary: "Settlement report export", Type: "Task", Status: "To Do", Category: "new", Sprint: s43, Assignee: me, Mine: true},
			{Key: "ABC-422", Summary: "Weekly email digest", Type: "Story", Status: "To Do", Category: "new", Sprint: s43, Assignee: me, Mine: true},
		},
		Mentions: []atlassian.Mention{
			{Key: "ABC-398", Summary: "Refund cutoff window", Author: "Maya Lin", Text: "@Jordan Blake does the refund cutoff stay at 30 days for card payments?", Created: now.Add(-50 * time.Hour), CommentID: "c-7001", URL: "https://acme.atlassian.net/browse/ABC-398"},
			{Key: "ABC-405", Summary: "Split settlement batches by currency", Author: "Oskar Berg", Text: "@Jordan Blake the MR is up, ping me if the batching looks off", Created: now.Add(-26 * time.Hour), CommentID: "c-7002", URL: "https://acme.atlassian.net/browse/ABC-405"},
		},
		FetchedAt: now,
	}
}

func demoSession(id, profile, repo, branch, title, color, model string, status claude.Status, started, active time.Duration) row {
	now := time.Now()
	home, _ := os.UserHomeDir()
	r := row{Session: claude.Session{
		ID: id, Profile: claude.Profile{Name: profile},
		Transcript: &claude.Transcript{
			ID: id, Cwd: filepath.Join(home, "code", repo), Branch: branch, AITitle: title, Color: color, Model: model,
			Started: now.Add(-started), Updated: now.Add(-active),
		},
	}}
	if status != "" {
		r.Live = &claude.Live{SessionID: id, Cwd: r.Transcript.Cwd, Status: status, StartedAt: msAgoShell(started), UpdatedAt: msAgoShell(active)}
	}
	return r
}

func demoSessions(now time.Time) []row {
	m := time.Minute
	refunds := demoSession("7c1e4b2a-5d3f-4e8a-9b61-2f0c8d4a7e19", "work", "payments", "feat/ABC-412/refund-retries", "Retry refund webhooks with backoff", "purple", "claude-opus-5-5", claude.StatusWaiting, 95*m, 12*m)
	refunds.LastPrompt = "the retries should stop after 6 attempts, then park the webhook in the dead-letter queue"
	refunds.Models = []string{"claude-sonnet-5-5", "claude-opus-5-5"}
	refunds.Usage = claude.Usage{Requests: 64, Output: 48_000, CacheRead: 6_200_000}
	refunds.PRs = []claude.PRLink{{URL: "https://gitlab.acme.dev/acme/payments/-/merge_requests/229"}}
	refunds.pane = &tmux.Pane{ID: "%3", Session: "work", Window: "2", Index: "0"}

	review := demoSession("a93f0d6c-1b2e-4c7d-8e5f-6a4b3c2d1e0f", "work", "payments", "feat/ABC-405/settlement-currency", "Review !231 settlement batches", "cyan", "claude-opus-5-5", claude.StatusBusy, 20*m, 0)
	review.PRs = []claude.PRLink{{URL: "https://gitlab.acme.dev/acme/payments/-/merge_requests/231"}}
	review.pane = &tmux.Pane{ID: "%7", Session: "work", Window: "5", Index: "0"}

	decimals := demoSession("4e2d9a1b-7c3f-4b5e-a6d8-9f0e1c2b3a4d", "work", "billing", "feat/ABC-401/decimal-lines", "Fix review threads on !87", "blue", "claude-sonnet-5-5", claude.StatusBusy, 40*m, 0)
	decimals.pane = &tmux.Pane{ID: "%5", Session: "work", Window: "3", Index: "0"}

	limiter := demoSession("b5c4d3e2-f1a0-4b9c-8d7e-6f5a4b3c2d1e", "work", "gateway", "feat/ABC-430/rate-limit", "Rate limiter for the v2 API", "green", "claude-opus-5-5", claude.StatusIdle, 3*time.Hour, 8*m)
	limiter.pane = &tmux.Pane{ID: "%2", Session: "work", Window: "1", Index: "0"}

	digest := demoSession("0f1e2d3c-4b5a-4c6d-9e8f-7a6b5c4d3e2f", "personal", "notifications", "feat/ABC-422/digest", "Email digest templates", "orange", "claude-haiku-5-5", claude.StatusIdle, 2*time.Hour, 30*m)
	digest.host = "Ghostty"

	closed := []row{
		demoSession("c2d3e4f5-a6b7-4c8d-9e0f-1a2b3c4d5e6f", "work", "payments", "spike/idempotency", "Spike: idempotency keys", "", "claude-opus-5-5", "", 26*time.Hour, 22*time.Hour),
		demoSession("d4e5f6a7-b8c9-4d0e-8f1a-2b3c4d5e6f7a", "work", "ledger", "fix/flaky-snapshot-test", "Fix flaky ledger snapshot test", "yellow", "claude-sonnet-5-5", "", 7*time.Hour, 5*time.Hour),
		demoSession("e6f7a8b9-c0d1-4e2f-a3b4-c5d6e7f8a9b0", "work", "payments", "chore/go-1.24", "Bump Go to 1.24", "", "claude-haiku-5-5", "", 50*time.Hour, 45*time.Hour),
	}
	return append([]row{limiter, digest, refunds, decimals, review}, closed...)
}

func demoUsage(now time.Time) []claude.DayModel {
	type mix struct{ opus, sonnet, haiku float64 }
	days := []mix{
		{1.0, 0.6, 0.2}, {0.8, 0.9, 0.1}, {1.4, 0.3, 0.3}, {0.2, 0.3, 0.1}, {0.1, 0, 0.1},
		{0.9, 1.1, 0.2}, {1.6, 0.4, 0.4}, {0.6, 0.8, 0.1}, {1.2, 0.2, 0.2}, {0.7, 0.5, 0.3},
		{0.1, 0.2, 0}, {0, 0.1, 0.1}, {1.1, 0.7, 0.2}, {0.5, 1.2, 0.1}, {0.9, 0.3, 0.2},
	}
	var out []claude.DayModel
	for ago, d := range days {
		day := now.AddDate(0, 0, -ago).Format("2006-01-02")
		for _, x := range []struct {
			id string
			f  float64
		}{{"claude-opus-5-5", d.opus}, {"claude-sonnet-5-5", d.sonnet}, {"claude-haiku-5-5", d.haiku}} {
			if x.f == 0 {
				continue
			}
			out = append(out, claude.DayModel{Day: day, Model: x.id, Tokens: claude.Tokens{
				Requests: int(220 * x.f), Input: int(180_000 * x.f), Output: int(260_000 * x.f),
				Read: int(14_000_000 * x.f), Write5m: int(400_000 * x.f), Write1h: int(150_000 * x.f),
			}})
		}
	}
	return out
}

// An ANSI screen laid out on a monospace grid as SVG, in a macOS-style window.

const (
	svgFont   = 14.0
	svgCellW  = 8.4
	svgLineH  = 18.0
	svgPad    = 16.0
	svgTitleH = 34.0
	svgFg     = "#abb2bf"
)

type svgStyle struct {
	fg, bg                     string
	bold, faint, italic, under bool
}

type svgCell struct {
	text string
	svgStyle
	wide bool // the right half of a wide rune
}

func screenSVG(screen string, cols, rows int) string {
	lines := parseANSI(screen)
	rows = max(rows, len(lines))
	w := svgPad*2 + float64(cols)*svgCellW
	h := svgTitleH + svgPad + float64(rows)*svgLineH + svgPad
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%g" height="%g" viewBox="0 0 %g %g" font-family="SFMono-Regular, Menlo, Consolas, 'Liberation Mono', monospace" font-size="%g" xml:space="preserve">`, w, h, w, h, svgFont)
	fmt.Fprintf(&b, `<rect width="%g" height="%g" rx="12" fill="#282c34"/>`, w, h)
	fmt.Fprintf(&b, `<rect width="%g" height="%g" rx="12" fill="#21252b"/><rect y="%g" width="%g" height="12" fill="#21252b"/>`, w, svgTitleH, svgTitleH-12, w)
	for i, c := range []string{"#ff5f57", "#febc2e", "#28c840"} {
		fmt.Fprintf(&b, `<circle cx="%d" cy="17" r="6" fill="%s"/>`, 22+20*i, c)
	}
	fmt.Fprintf(&b, `<text x="%g" y="22" fill="#5c6370" font-size="12" text-anchor="middle">claudia</text>`, w/2)

	for y, line := range lines {
		top := svgTitleH + svgPad + float64(y)*svgLineH
		for x := 0; x < len(line); {
			bg := line[x].bg
			end := x + 1
			for end < len(line) && line[end].bg == bg {
				end++
			}
			if bg != "" {
				fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%g" fill="%s"/>`, svgPad+float64(x)*svgCellW, top, float64(end-x)*svgCellW+0.5, svgLineH+0.5, bg)
			}
			x = end
		}
		for x := 0; x < len(line); {
			st := line[x].svgStyle
			st.bg = ""
			end := x + 1
			for end < len(line) && sameInk(line[end].svgStyle, st) {
				end++
			}
			writeRun(&b, line[x:end], x, top+svgLineH*0.74, st)
			x = end
		}
	}
	b.WriteString("</svg>\n")
	return b.String()
}

func sameInk(a, b svgStyle) bool {
	a.bg = ""
	return a == b
}

// writeRun draws ASCII stretches as one text pinned to the grid with
// textLength, and every other rune on its own, since fallback fonts give those
// other widths.
func writeRun(b *strings.Builder, run []svgCell, x0 int, baseline float64, st svgStyle) {
	for i := 0; i < len(run); {
		c := run[i]
		if c.wide || c.text == " " {
			i++
			continue
		}
		if len(c.text) > 1 || c.text[0] >= utf8.RuneSelf {
			writeText(b, c.text, x0+i, 0, baseline, st)
			i++
			continue
		}
		end := i
		var text strings.Builder
		for end < len(run) && len(run[end].text) == 1 && run[end].text[0] < utf8.RuneSelf {
			text.WriteString(run[end].text)
			end++
		}
		s := strings.TrimRight(text.String(), " ")
		writeText(b, s, x0+i, len(s), baseline, st)
		i = end
	}
}

func writeText(b *strings.Builder, s string, x, cells int, baseline float64, st svgStyle) {
	fill := st.fg
	if fill == "" {
		fill = svgFg
	}
	fmt.Fprintf(b, `<text x="%.1f" y="%.1f" fill="%s"`, svgPad+float64(x)*svgCellW, baseline, fill)
	if cells > 1 {
		fmt.Fprintf(b, ` textLength="%.1f" lengthAdjust="spacing"`, float64(cells)*svgCellW)
	}
	if st.bold {
		b.WriteString(` font-weight="bold"`)
	}
	if st.faint {
		b.WriteString(` opacity="0.6"`)
	}
	if st.italic {
		b.WriteString(` font-style="italic"`)
	}
	if st.under {
		b.WriteString(` text-decoration="underline"`)
	}
	b.WriteString(">" + xmlEscape(s) + "</text>")
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(s)
}

// parseANSI splits a screen into lines of cells, following SGR colours and
// attributes and skipping every other escape sequence.
func parseANSI(s string) [][]svgCell {
	var lines [][]svgCell
	var line []svgCell
	var st svgStyle
	for i := 0; i < len(s); {
		switch c := s[i]; {
		case c == '\n':
			lines = append(lines, line)
			line = nil
			i++
		case c == '\r':
			i++
		case c == 0x1b && i+1 < len(s) && s[i+1] == '[':
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j < len(s) && s[j] == 'm' {
				st = applySGR(st, s[i+2:j])
			}
			i = j + 1
		case c == 0x1b && i+1 < len(s) && s[i+1] == ']':
			j := i + 2
			for j < len(s) && s[j] != 0x07 && !(s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\') {
				j++
			}
			if j < len(s) && s[j] == 0x1b {
				j++
			}
			i = j + 1
		case c == 0x1b:
			i += 2
		default:
			r, n := utf8.DecodeRuneInString(s[i:])
			i += n
			w := ansi.StringWidth(string(r))
			if w == 0 {
				if len(line) > 0 {
					line[len(line)-1].text += string(r)
				}
				continue
			}
			line = append(line, svgCell{text: string(r), svgStyle: st})
			if w > 1 {
				line = append(line, svgCell{svgStyle: st, wide: true})
			}
		}
	}
	return append(lines, line)
}

func applySGR(st svgStyle, params string) svgStyle {
	ps := strings.FieldsFunc(params, func(r rune) bool { return r == ';' || r == ':' })
	if len(ps) == 0 {
		return svgStyle{}
	}
	n := func(i int) int {
		if i >= len(ps) {
			return 0
		}
		v, _ := strconv.Atoi(ps[i])
		return v
	}
	for i := 0; i < len(ps); i++ {
		switch p := n(i); {
		case p == 0:
			st = svgStyle{}
		case p == 1:
			st.bold = true
		case p == 2:
			st.faint = true
		case p == 3:
			st.italic = true
		case p == 4:
			st.under = true
		case p == 22:
			st.bold, st.faint = false, false
		case p == 23:
			st.italic = false
		case p == 24:
			st.under = false
		case p >= 30 && p <= 37:
			st.fg = ansi16[p-30]
		case p >= 90 && p <= 97:
			st.fg = ansi16[p-90+8]
		case p == 39:
			st.fg = ""
		case p >= 40 && p <= 47:
			st.bg = ansi16[p-40]
		case p >= 100 && p <= 107:
			st.bg = ansi16[p-100+8]
		case p == 49:
			st.bg = ""
		case p == 38 || p == 48:
			var col string
			switch n(i + 1) {
			case 2:
				col = fmt.Sprintf("#%02x%02x%02x", n(i+2), n(i+3), n(i+4))
				i += 4
			case 5:
				col = ansi256(n(i + 2))
				i += 2
			}
			if p == 38 {
				st.fg = col
			} else {
				st.bg = col
			}
		}
	}
	return st
}

// ansi16 is One Dark's terminal palette.
var ansi16 = []string{
	"#282c34", "#e06c75", "#98c379", "#e5c07b", "#61afef", "#c678dd", "#56b6c2", "#abb2bf",
	"#5c6370", "#e06c75", "#98c379", "#e5c07b", "#61afef", "#c678dd", "#56b6c2", "#ffffff",
}

func ansi256(c int) string {
	switch {
	case c < 16:
		return ansi16[max(c, 0)]
	case c < 232:
		c -= 16
		level := func(v int) int {
			if v == 0 {
				return 0
			}
			return 55 + v*40
		}
		return fmt.Sprintf("#%02x%02x%02x", level(c/36), level(c/6%6), level(c%6))
	}
	g := 8 + (min(c, 255)-232)*10
	return fmt.Sprintf("#%02x%02x%02x", g, g, g)
}

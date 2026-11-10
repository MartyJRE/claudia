package ui

import (
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/claude"
)

func TestLayoutFitsContentShell(t *testing.T) {
	cols := []col{{"KEY", 10, 0}, {"SUMMARY", 0, 0}, {"STATUS", 12, 0}, {"MR", 8, 0}, {"WIDE", 10, 150}}
	for _, c := range []struct {
		name    string
		width   int
		natural []int
		want    []int
	}{
		{"no measurements keep the set widths", 100, nil, []int{10, 63, 12, 8, -1}},
		{"columns shrink to their content", 100, []int{5, 0, 7, 0, 0}, []int{5, 79, 7, 2, -1}},
		{"never narrower than the title", 100, []int{1, 0, 1, 0, 0}, []int{3, 82, 6, 2, -1}},
		{"wider content takes what the others gave back", 100, []int{5, 0, 20, 0, 0}, []int{5, 66, 20, 2, -1}},
		{"growth stops at twice the set width", 100, []int{0, 0, 40, 0, 0}, []int{3, 64, 24, 2, -1}},
		{"nothing given back, nothing to grow by", 100, []int{10, 0, 20, 8, 0}, []int{10, 63, 12, 8, -1}},
		{"growth leaves the stretching column its floor", 70, []int{5, 0, 20, 0, 0}, []int{5, 40, 16, 2, -1}},
		{"no growth once the stretching column is under its floor", 50, []int{5, 0, 20, 0, 0}, []int{5, 24, 12, 2, -1}},
		{"the stretching column never drops under 10", 20, []int{10, 0, 12, 8, 0}, []int{10, 10, 12, 8, -1}},
		{"a hidden column's content is ignored", 100, []int{5, 0, 7, 0, 99}, []int{5, 79, 7, 2, -1}},
		{"a shown column measures like any other", 160, []int{5, 0, 7, 0, 4}, []int{5, 134, 7, 2, 4}},
		{"short measurements leave the rest at their set width", 100, []int{5}, []int{5, 68, 12, 8, -1}},
	} {
		if got := layout(cols, c.width, c.natural); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}

	two := []col{{"A", 10, 0}, {"B", 0, 0}, {"C", 10, 0}, {"D", 10, 0}, {"E", 10, 0}}
	if got, want := layout(two, 200, []int{1, 0, 15, 15, 1}), []int{1, 160, 15, 15, 1}; !slices.Equal(got, want) {
		t.Errorf("two wide columns both grow: %v, want %v", got, want)
	}
	if got, want := layout(two, 200, []int{4, 0, 30, 30, 10}), []int{4, 152, 16, 10, 10}; !slices.Equal(got, want) {
		t.Errorf("the room goes left to right: %v, want %v", got, want)
	}
	if got := layout([]col{{"A", 5, 0}}, 80, []int{9}); !slices.Equal(got, []int{5}) {
		t.Errorf("without a stretching column nothing grows: %v", got)
	}
	if got := layout([]col{{"", 5, 0}, {"B", 0, 0}}, 80, []int{0, 0}); got[0] != 1 {
		t.Errorf("an empty untitled column keeps one cell: %v", got)
	}
	if got := layout([]col{{"LONG TITLE", 4, 0}, {"B", 0, 0}}, 80, []int{1, 0}); got[0] != 4 {
		t.Errorf("a title wider than the set width is cut as before: %v", got)
	}
}

// TestLayoutPropertiesShell checks the rules on many random tables.
func TestLayoutPropertiesShell(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for n := range 20000 {
		cols := make([]col, 1+rng.Intn(8))
		flex := rng.Intn(len(cols) + 1) // len(cols) means none
		for i := range cols {
			cols[i] = col{title: strings.Repeat("T", rng.Intn(12)), w: 1 + rng.Intn(30)}
			if rng.Intn(4) == 0 {
				cols[i].min = 60 + rng.Intn(150)
			}
			if i == flex {
				cols[i].w, cols[i].min = 0, 0
			}
		}
		natural := make([]int, len(cols))
		for i := range natural {
			natural[i] = rng.Intn(70)
		}
		width := 20 + rng.Intn(230)
		before, got := layout(cols, width, nil), layout(cols, width, natural)
		fail := func(why string) {
			t.Fatalf("case %d, %s:\ncols %v width %d natural %v\nbefore %v\ngot    %v", n, why, cols, width, natural, before, got)
		}
		grew := false
		for i, c := range cols {
			switch {
			case before[i] < 0 || got[i] < 0:
				if before[i] != got[i] {
					fail("hidden columns stay hidden, shown ones shown")
				}
			case c.w == 0:
				if got[i] < before[i] {
					fail("the stretching column never gets narrower than it was")
				}
			default:
				fitted := min(c.w, max(1, lipgloss.Width(c.title), natural[i]))
				if natural[i] <= c.w && got[i] != fitted {
					fail("a column holding less than its width fits its content and title")
				}
				if got[i] < fitted || got[i] > max(fitted, min(natural[i], 2*c.w)) {
					fail("a column only grows towards its content, at most to twice its width")
				}
				grew = grew || got[i] > fitted
			}
		}
		if flex < len(cols) {
			line := 3
			for _, w := range got {
				if w >= 0 {
					line += w + 1
				}
			}
			line--
			if got[flex] > 10 && line != width-1 {
				fail(fmt.Sprintf("the row fills the width: %d", line))
			}
			if grew && got[flex] < flexFloor {
				fail("growing never pushes the stretching column under its floor")
			}
		}
	}
}

func TestMeasureShell(t *testing.T) {
	cols := []col{{"A", 5, 0}, {"B", 0, 0}, {"C", 5, 0}}
	rows := [][]string{
		{sKey.Render("abc"), "x", "日本"},
		{"a single cell is a header and is not measured at all"},
		{"abcdef", ""},
		{"", "", "", "a cell past the columns is ignored"},
		{"✻ 2", "y", "é"},
	}
	cell := func(i int, sel bool) []string {
		if sel {
			t.Error("measuring draws rows unselected")
		}
		return rows[i]
	}
	if got, want := measure(cols, len(rows), cell), []int{6, 1, 4}; !slices.Equal(got, want) {
		t.Errorf("measure: %v, want %v (styling ignored, wide runes count double)", got, want)
	}
	if got := measure(cols, 0, cell); !slices.Equal(got, []int{0, 0, 0}) {
		t.Errorf("no rows: %v", got)
	}
}

// viewLayoutShell is how the current view's table lays out right now.
func viewLayoutShell(m *Model) ([]col, []int) {
	var cols []col
	var natural []int
	switch m.view {
	case vInbox:
		cols, natural = inboxCols, m.vis.widths.inbox
	case vSessions:
		cols, natural = sessionCols, m.vis.widths.sessions
	case vReview, vMine:
		cols, natural = m.mrCols(), m.vis.widths.mrs
	case vJira:
		cols, natural = jiraCols, m.vis.widths.jira
	case vMentions:
		cols, natural = mentionCols, m.vis.widths.mentions
	case vConfluence:
		cols, natural = pageCols, m.vis.widths.pages
	case vNotes:
		cols, natural = noteCols, m.vis.widths.notes
	case vUsage:
		cols, natural = usageCols, m.vis.widths.usage
		if m.usageByTicket() {
			cols, natural = costCols, m.vis.widths.costs
		}
	}
	return cols, layout(cols, m.width, natural)
}

func listLinesShell(m *Model) []string {
	return strings.Split(plainShell(m.viewList(m.height-10)), "\n")
}

// headerShell finds the table's title row and checks every title sits where
// the layout puts its column, so what's drawn is what layout says.
func headerShell(t *testing.T, m *Model) string {
	t.Helper()
	cols, ws := viewLayoutShell(m)
	var titles []int
	for i, c := range cols {
		if c.title != "" && ws[i] > 0 {
			titles = append(titles, i)
		}
	}
	for _, l := range listLinesShell(m) {
		if !strings.Contains(l, cols[titles[0]].title) || !strings.Contains(l, cols[titles[len(titles)-1]].title) {
			continue
		}
		for _, i := range titles {
			title := ansi.Truncate(cols[i].title, ws[i], "…")
			at := 3 + colStart(ws, i)
			if got := ansi.Cut(l, at, at+ansi.StringWidth(title)); got != title {
				t.Fatalf("%s should start at %d (widths %v), found %q in\n%q", cols[i].title, at, ws, got, l)
			}
		}
		return l
	}
	t.Fatalf("no title row in:\n%s", strings.Join(listLinesShell(m), "\n"))
	return ""
}

func TestColumnsFitTheirContentShell(t *testing.T) {
	m := jiraModelViews(t)
	m.width = 160
	m.jira.Sprint[2].Status = "Waiting on another user"
	m.reindex()
	cols, ws := viewLayoutShell(m)
	at := func(title string) int { return slices.IndexFunc(cols, func(c col) bool { return c.title == title }) }
	if ws[at("MR")] != 2 || ws[at("CLAUDE")] != 6 {
		t.Errorf("empty columns shrink to their titles: %v", ws)
	}
	if ws[at("STATUS")] != 23 || ws[at("ASSIGNEE")] != len("ASSIGNEE") {
		t.Errorf("STATUS fits its longest value, ASSIGNEE its title: %v", ws)
	}
	if ws[at("KEY")] >= jiraCols[0].w {
		t.Errorf("KEY fits the keys: %v", ws)
	}
	header := headerShell(t, m)
	s := screenViews(m)
	if !strings.Contains(s, "Waiting on another user") || strings.Contains(s, "Waiting on another us…") {
		t.Errorf("the long status shows whole:\n%s", s)
	}
	for _, l := range listLinesShell(m) {
		if strings.Contains(l, "Refund flow") && widthToShell(l, "Waiting") != widthToShell(header, "STATUS") {
			t.Errorf("a row's cells sit under their titles:\n%q\n%q", header, l)
		}
	}
}

// widthToShell is how many cells into l sub starts.
func widthToShell(l, sub string) int {
	before, _, ok := strings.Cut(l, sub)
	if !ok {
		return -1
	}
	return ansi.StringWidth(before)
}

// stableShell runs steps on the current view and checks after each that the
// columns haven't moved.
func stableShell(t *testing.T, m *Model, steps []func() string) {
	t.Helper()
	_, base := viewLayoutShell(m)
	header := headerShell(t, m)
	for _, step := range steps {
		what := step()
		if _, got := viewLayoutShell(m); !slices.Equal(got, base) {
			t.Errorf("%s moved the columns: %v, was %v", what, got, base)
		}
		if got := headerShell(t, m); got != header {
			t.Errorf("%s moved the titles:\n%q\n%q", what, got, header)
		}
	}
}

// foldStepsShell folds and filters the view, checking each step really
// changed what's listed so the test can't pass by doing nothing.
func foldStepsShell(t *testing.T, m *Model, fold []string, filter string) []func() string {
	t.Helper()
	n := m.count()
	return []func() string{
		func() string {
			pressShell(m, fold...)
			if m.count() >= n {
				t.Fatalf("folding hid nothing: %d rows, was %d", m.count(), n)
			}
			return "folding"
		},
		func() string {
			pressShell(m, "+")
			if m.count() != n {
				t.Fatalf("unfolding brought back %d rows of %d", m.count(), n)
			}
			return "unfolding"
		},
		func() string {
			pressShell(m, "/")
			typeShell(m, filter)
			if m.count() >= n || m.count() == 0 {
				t.Fatalf("the filter %q kept %d rows of %d", filter, m.count(), n)
			}
			return "filtering"
		},
		func() string {
			pressShell(m, "esc", "esc")
			if m.count() != n {
				t.Fatalf("clearing the filter brought back %d rows of %d", m.count(), n)
			}
			return "clearing the filter"
		},
	}
}

func TestColumnsIgnoreFoldsAndFiltersShell(t *testing.T) {
	t.Run("jira", func(t *testing.T) {
		m := jiraModelViews(t)
		m.jira.Sprint[4].Status = "Waiting on another user" // ABC-4, alone in Sprint 3
		m.reindex()
		cursorToViews(t, m, "#Sprint 2")
		stableShell(t, m, foldStepsShell(t, m, []string{" "}, "refund"))
		stableShell(t, m, foldStepsShell(t, m, []string{"-"}, "card"))
	})
	t.Run("sessions", func(t *testing.T) {
		m := newModelShell(t)
		m.switchView(vSessions)
		stableShell(t, m, foldStepsShell(t, m, []string{"-"}, "invoice"))
	})
	t.Run("inbox", func(t *testing.T) {
		m := newModelShell(t)
		m.gl.Review = append(m.gl.Review, mrShell("ledger", 14, "fix: totals", "bob", "fix/totals", 5*time.Hour))
		m.reindex()
		m.switchView(vInbox)
		pressShell(m, "v")
		stableShell(t, m, foldStepsShell(t, m, []string{"-"}, "export"))
	})
	t.Run("review", func(t *testing.T) {
		m := newModelShell(t)
		m.gl.Review = append(m.gl.Review, mrShell("payments", 15, "fix: a much longer title for the second payments MR", "zed-the-reviewer", "fix/x", 3*time.Hour))
		m.reindex()
		m.switchView(vReview)
		pressShell(m, "v")
		stableShell(t, m, foldStepsShell(t, m, []string{"-"}, "export"))
	})
	t.Run("mentions and confluence", func(t *testing.T) {
		m := newModelShell(t)
		m.atlErr = nil
		m.jira.Mentions = append(m.jira.Mentions, atlassian.Mention{Key: "ABC-1", Summary: "Payments export", Author: "Gus", Text: "and this?", CommentID: "c3", Created: time.Now().Add(-2 * time.Hour)})
		m.conf = atlassian.ConfluenceSnapshot{
			Mine: []atlassian.Page{
				{Title: "Deploys", Space: "OPERATIONS", Modified: time.Now().Add(-time.Hour)},
				{Title: "Roadmap", Space: "ENG", Modified: time.Now().Add(-2 * time.Hour)},
				{Title: "Oncall", Space: "OPERATIONS", Modified: time.Now().Add(-3 * time.Hour)},
			},
			FetchedAt: time.Now(),
		}
		m.reindex()
		m.switchView(vMentions)
		pressShell(m, "v")
		stableShell(t, m, foldStepsShell(t, m, []string{"-"}, "gus"))
		m.switchView(vConfluence)
		pressShell(m, "v")
		stableShell(t, m, foldStepsShell(t, m, []string{"-"}, "roadmap"))
	})
	t.Run("notes", func(t *testing.T) {
		m := notesModelViews(t)
		m.width = 160
		cursorToNoteViews(t, m, "#01 - Projects")
		n := m.count()
		stableShell(t, m, []func() string{
			func() string {
				pressViews(m, " ")
				if m.count() == n {
					t.Fatal("folding the folder changed nothing")
				}
				return "folding a folder"
			},
			func() string {
				pressViews(m, " ")
				return "unfolding it"
			},
			func() string {
				pressViews(m, "/")
				typeShell(m, "teams")
				if m.count() != 1 {
					t.Fatalf("filter kept %d", m.count())
				}
				return "filtering"
			},
		})
	})
}

func TestColumnsCountWhatAFoldHidesShell(t *testing.T) {
	m := jiraModelViews(t)
	cursorToViews(t, m, "#Sprint 2")
	pressViews(m, " ")
	m.jira.Sprint[2].Status = "Waiting on another user" // ABC-3, under the folded sprint
	m.reindex()
	if got := rowKeysViews(m.vis.jira); !strings.Contains(got, "#Sprint 2+") || strings.Contains(got, "ABC-3") {
		t.Fatalf("Sprint 2 should be folded: %s", got)
	}
	if _, ws := viewLayoutShell(m); ws[3] != 23 {
		t.Errorf("STATUS fits a status no row shows: %v", ws)
	}
	pressViews(m, "/")
	typeShell(m, "next")
	if _, ws := viewLayoutShell(m); ws[3] != 23 {
		t.Errorf("STATUS fits a status the filter hides: %v", ws)
	}
}

func TestColumnsFollowTheDataShell(t *testing.T) {
	m := jiraModelViews(t)
	_, before := viewLayoutShell(m)
	m.jira.Sprint = append(m.jira.Sprint, atlassian.Issue{Key: "ABC-11", Summary: "New", Status: "Waiting on another user", Category: "indeterminate", Sprint: m.jira.Sprint[1].Sprint})
	m.reindex()
	if _, ws := viewLayoutShell(m); ws[3] <= before[3] || ws[3] != 23 {
		t.Errorf("a longer status widens STATUS on refresh: %v, was %v", ws, before)
	}
	m.jira.Sprint = m.jira.Sprint[:len(m.jira.Sprint)-1]
	m.reindex()
	if _, ws := viewLayoutShell(m); !slices.Equal(ws, before) {
		t.Errorf("and narrows it again once it's gone: %v, was %v", ws, before)
	}
	pressViews(m, "a")
	headerShell(t, m)
	if _, ws := viewLayoutShell(m); ws[3] != len("In Review") {
		t.Errorf("the team's list measures the team's tickets: %v", ws)
	}

	s := newModelShell(t)
	s.switchView(vReview)
	_, review := viewLayoutShell(s)
	s.switchView(vMine)
	headerShell(t, s)
	if _, mine := viewLayoutShell(s); mine[2] < len("REVIEWERS") || review[2] != len("AUTHOR") {
		t.Errorf("My MRs measures its reviewers, not Review's authors: mine %v review %v", mine, review)
	}
}

func TestGroupHeadersFollowTheirColumnShell(t *testing.T) {
	m := newModelShell(t)
	m.switchView(vSessions)
	if _, ws := viewLayoutShell(m); ws[0] != sessionCols[0].w {
		t.Errorf("the animated STATUS keeps its width: %v", ws)
	}
	m.sessions[0].Live.Status = claude.StatusIdle
	m.refilter()
	if _, ws := viewLayoutShell(m); ws[0] != sessionCols[0].w {
		t.Errorf("whatever the statuses say: %v", ws)
	}
	glyphAt := func(title string) {
		t.Helper()
		header := headerShell(t, m)
		want := widthToShell(header, title)
		found := false
		for _, l := range listLinesShell(m) {
			before, _, ok := strings.Cut(l, "▾ ")
			if !ok {
				continue
			}
			found = true
			if got := ansi.StringWidth(before); got != want && strings.Trim(before, " ▌▏") == "" {
				t.Errorf("a group header starts under %s (%d), not at %d:\n%q\n%q", title, want, got, header, l)
			}
		}
		if !found {
			t.Fatalf("no group header:\n%s", strings.Join(listLinesShell(m), "\n"))
		}
	}
	glyphAt("SESSION")

	m.atlErr = nil
	m.jira.Mentions = append(m.jira.Mentions, atlassian.Mention{Key: "ABC-1", Summary: "Payments export", Author: "Gus", Text: "and this?", CommentID: "c3", Created: time.Now().Add(-2 * time.Hour)})
	m.reindex()
	m.switchView(vMentions)
	pressShell(m, "v")
	glyphAt("WHERE")
}

func TestUsageBarsFillTheirColumnShell(t *testing.T) {
	m := usageModelViews(t)
	for _, width := range []int{90, 120, 200} {
		m.width = width
		_, ws := viewLayoutShell(m)
		headerShell(t, m)
		bar := ws[len(ws)-1]
		longest := 0
		for _, l := range listLinesShell(m) {
			if strings.Contains(l, "━") {
				longest = max(longest, ansi.StringWidth(strings.TrimRight(ansi.TruncateLeft(l, 3+colStart(ws, len(ws)-1), ""), " ")))
			}
		}
		if longest != bar {
			t.Errorf("width %d: the costliest day's bar is %d wide, its column %d", width, longest, bar)
		}
	}
}

// TestTablesNeverWidenShell draws every table at many widths, with content
// longer than any column, and checks no line got wider than it used to.
func TestTablesNeverWidenShell(t *testing.T) {
	long := strings.Repeat("very long ", 12)
	m := newModelShell(t)
	m.atlErr = nil
	m.jira.Sprint = append(m.jira.Sprint, atlassian.Issue{Key: "ABC-12345", Summary: long, Status: long, Category: "new", Type: long, Assignee: long, Mine: true})
	m.jira.Mentions = append(m.jira.Mentions, atlassian.Mention{Key: "ABC-1", Summary: long, Author: long, Text: long, CommentID: "c9", Created: time.Now()})
	m.gl.Review = append(m.gl.Review, mrShell("a-repository-with-a-long-name", 99999, long, long, long, time.Hour))
	m.conf = atlassian.ConfluenceSnapshot{Mine: []atlassian.Page{{Title: long, Space: long, Modified: time.Now()}}, FetchedAt: time.Now()}
	m.sessions[0].Transcript.Cwd = "/code/" + strings.ReplaceAll(long, " ", "-")
	m.sessions[0].Transcript.Branch = strings.ReplaceAll(long, " ", "/")
	m.reindex()
	// Usage is left out: the plan limits above its table overflow narrow terminals on their own.
	for _, v := range []view{vInbox, vSessions, vReview, vMine, vJira, vMentions, vConfluence} {
		m.switchView(v)
		for width := 40; width <= 260; width += 7 {
			m.width = width
			cols, ws := viewLayoutShell(m)
			old := 3 - 1
			for _, w := range layout(cols, width, nil) {
				if w >= 0 {
					old += w + 1
				}
			}
			limit := max(width, old)
			for _, l := range listLinesShell(m) {
				if w := ansi.StringWidth(l); w > limit {
					t.Fatalf("view %d at width %d: a %d-wide line, limit %d (widths %v):\n%q", v, width, w, limit, ws, l)
				}
			}
			if width >= 110 {
				headerShell(t, m)
			}
		}
	}
}

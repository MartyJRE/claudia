package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/repos"
)

func mrIIDsShell(m *Model) []int {
	var out []int
	for _, r := range m.vis.mrs {
		out = append(out, r.IID)
	}
	return out
}

func eqIntsShell(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestReviewListOrderShell(t *testing.T) {
	m := newModelShell(t)
	newer := mrShell("ledger", 14, "feat: newest", "bob", "x", time.Minute)
	draftNewest := mrShell("ledger", 15, "Draft: newest draft", "bob", "y", 0)
	draftNewest.Draft = true
	m.gl.Review = append(m.gl.Review, newer, draftNewest)
	m.reindex()
	m.switchView(vReview)
	if got, want := mrIIDsShell(m), []int{14, 11, 15, 12}; !eqIntsShell(got, want) {
		t.Errorf("newest first, drafts last, approved hidden: got %v want %v", got, want)
	}
	pressShell(m, "a")
	if got, want := mrIIDsShell(m), []int{14, 11, 13, 15, 12}; !eqIntsShell(got, want) {
		t.Errorf("a shows approved too: got %v want %v", got, want)
	}
	if s := screenShell(m); !strings.Contains(s, "✓ you") || !strings.Contains(s, "all review requests") {
		t.Errorf("my approval shows")
	}
}

func TestMineListShell(t *testing.T) {
	m := newModelShell(t)
	m.switchView(vMine)
	if got, want := mrIIDsShell(m), []int{21, 22}; !eqIntsShell(got, want) {
		t.Fatalf("open then merged: %v", got)
	}
	if !m.vis.mrs[1].merged || m.vis.mrs[0].merged {
		t.Errorf("merged flag")
	}
	pressShell(m, "/")
	typeShell(m, "!22")
	if got := mrIIDsShell(m); !eqIntsShell(got, []int{22}) {
		t.Errorf("filter matches the !iid: %v", got)
	}
}

func TestMRListEmptyShell(t *testing.T) {
	m := newModelShell(t)
	m.gl = gitlab.Snapshot{}
	m.reindex()
	for _, c := range []struct {
		v    view
		set  func()
		want string
	}{
		{vReview, func() {}, "asking GitLab…"},
		{vReview, func() { m.errs[srcGitlab] = errors.New("glab: not logged in") }, "glab: not logged in"},
		{vReview, func() { m.gl.FetchedAt = time.Now() }, "nothing waiting for your review"},
		{vMine, func() {}, "no open or recently merged MRs"},
	} {
		c.set()
		m.switchView(c.v)
		if s := screenShell(m); !strings.Contains(s, c.want) {
			t.Errorf("%s: missing %q", viewNames[c.v], c.want)
		}
	}
}

func TestPipelineTextShell(t *testing.T) {
	for _, c := range []struct {
		status string
		want   string
	}{
		{"success", "✓ passed"},
		{"failed", "✗ failed"},
		{"running", "● running"},
		{"waiting_for_resource", "● waiting_for_resource"},
		{"canceled", "○ canceled"},
		{"skipped", "○ skipped"},
		{"manual", "▶ manual"},
		{"scheduled", "scheduled"},
	} {
		if got := plainShell(pipelineStatus(c.status)); got != c.want {
			t.Errorf("%s: %q", c.status, got)
		}
	}
	p := &gitlab.Pipeline{Status: "success"}
	for _, c := range []struct {
		r    mrRow
		want string
	}{
		{mrRow{gitlab.MR{}, false}, "—"},
		{mrRow{gitlab.MR{Pipeline: p}, false}, "✓ passed"},
		{mrRow{gitlab.MR{Pipeline: p}, true}, "merged"},
		{mrRow{gitlab.MR{MergePipeline: &gitlab.Pipeline{Status: "failed"}}, true}, "✗ failed"},
	} {
		if got := plainShell(pipelineText(c.r)); got != c.want {
			t.Errorf("pipelineText: %q, want %q", got, c.want)
		}
	}
}

func TestApprovalTextShell(t *testing.T) {
	m := newModelShell(t)
	for _, c := range []struct {
		mr   gitlab.MR
		want string
	}{
		{gitlab.MR{ApprovedBy: []string{"x", "me"}, Approved: true}, "✓ you"},
		{gitlab.MR{ApprovedBy: []string{"x", "y"}, Approved: true}, "✓ 2"},
		{gitlab.MR{ApprovedBy: []string{"x"}, ApprovalsLeft: 1}, "1, 1 left"},
		{gitlab.MR{ApprovalsLeft: 2}, "2 needed"},
		{gitlab.MR{}, ""},
	} {
		if got := plainShell(m.approvalText(c.mr)); got != c.want {
			t.Errorf("%+v: %q, want %q", c.mr.ApprovedBy, got, c.want)
		}
	}
}

func TestChipsShell(t *testing.T) {
	m := newModelShell(t)
	mr := m.gl.Review[0]
	if got := plainShell(m.chipsText(mrRow{mr, false})); got != "new" {
		t.Errorf("never opened: %q", got)
	}
	m.seen.mark(mr)
	if got := plainShell(m.chipsText(mrRow{mr, false})); got != "" {
		t.Errorf("just seen: %q", got)
	}
	mr.SHA, mr.UserNotesCount = "other", 2
	mr.Pipeline = &gitlab.Pipeline{Status: "failed"}
	mr.MergeStatus = "need_rebase"
	if got, want := plainShell(m.chipsText(mrRow{mr, false})), "new commits · +2 comments · pipeline failed · needs rebase"; got != want {
		t.Errorf("changes since seen:\n got %q\nwant %q", got, want)
	}
	m.seen.mark(mr)
	if got := plainShell(m.chipsText(mrRow{mr, false})); got != "rebase" {
		t.Errorf("a seen MR still needing a rebase says so: %q", got)
	}
	mr.MergeStatus = "conflict"
	m.seen.mark(mr)
	if got := plainShell(m.chipsText(mrRow{mr, false})); got != "conflict" {
		t.Errorf("conflict: %q", got)
	}
	if got := plainShell(m.chipsText(mrRow{mr, true})); got != "merged" {
		t.Errorf("merged MRs say so instead of showing chips: %q", got)
	}

	m.view = vMine
	own := gitlab.MR{WebURL: "own"}
	m.seen.mark(own)
	if got := plainShell(m.chipsText(mrRow{own, false})); got != "open threads" {
		t.Errorf("unresolved discussions before threads load: %q", got)
	}
	own.DiscussionsResolved = true
	if got := plainShell(m.chipsText(mrRow{own, false})); got != "" {
		t.Errorf("resolved: %q", got)
	}
	own.Threads = []gitlab.Thread{{}, {Resolved: true}, {}}
	if got := plainShell(m.chipsText(mrRow{own, false})); got != "2 open threads" {
		t.Errorf("counted threads: %q", got)
	}
}

func TestReviewersAndThreadsShell(t *testing.T) {
	if got := plainShell(reviewers(gitlab.MR{})); got != "—" {
		t.Errorf("no reviewers: %q", got)
	}
	mr := gitlab.MR{Reviewers: []gitlab.User{{Username: "a"}, {Username: "b"}}, ApprovedBy: []string{"b"}}
	if got := plainShell(reviewers(mr)); got != "a, ✓ b" {
		t.Errorf("reviewers: %q", got)
	}
	for _, c := range []struct {
		th   gitlab.Thread
		want string
	}{
		{gitlab.Thread{Author: "a", Body: "x  y"}, "● a x y"},
		{gitlab.Thread{Author: "a", Body: "x", Resolved: true, Path: "go.mod", Line: 3}, "✓ a go.mod:3 x"},
		{gitlab.Thread{Author: "a", Body: "x", Path: "a/b/c.go", Line: 1, Replies: 1, LastBy: "b"}, "● a …/b/c.go:1 x  (1 replies, last b)"},
	} {
		if got := plainShell(threadLine(c.th)); got != c.want {
			t.Errorf("threadLine: %q, want %q", got, c.want)
		}
	}
	for p, want := range map[string]string{"a.go": "a.go", "a/b.go": "a/b.go", "a/b/c/d.go": "…/c/d.go"} {
		if got := lastPath(p); got != want {
			t.Errorf("lastPath(%q) = %q", p, got)
		}
	}
}

func TestMRDetailShell(t *testing.T) {
	m := newModelShell(t)
	sibling := mrShell("frontend", 31, "feat(ABC-1): export button", "me", "feat/ABC-1/export", time.Hour)
	m.gl.Mine = append(m.gl.Mine, sibling)
	m.sessions[0].PRs = []claude.PRLink{{URL: m.gl.Review[0].WebURL}}
	m.reindex()
	m.switchView(vReview)
	s := plainShell(m.detailMR())
	containsAllShell(t, "review detail", s, "!11 feat(ABC-1): add payments export", "g/payments", "feat/ABC-1/export → devel", "by alice",
		"ABC-1 Payments export · Dev in progress", "same change: !31 frontend", "sessions: ● export csv")

	pressShell(m, "a", "G")
	pressShell(m, "k")
	if r, _ := m.currentMR(); r.IID != 13 {
		t.Fatalf("cursor on !%d", r.IID)
	}
	if s := plainShell(m.detailMR()); !strings.Contains(s, "approved by me") {
		t.Errorf("review detail lists approvers: %s", s)
	}

	m.switchView(vMine)
	m.cur[vMine].pos = 1
	if r, _ := m.currentMR(); r.IID != 31 {
		t.Fatalf("cursor on !%d", r.IID)
	}
	if s := plainShell(m.detailMR()); !strings.Contains(s, "no reviewer assigned") {
		t.Errorf("own MR without reviewers: %s", s)
	}

	m.gl = gitlab.Snapshot{}
	m.reindex()
	if s := plainShell(m.detailMR()); !strings.Contains(s, "—") {
		t.Errorf("empty detail: %q", s)
	}
}

func TestMRKeysShell(t *testing.T) {
	m := newModelShell(t)
	m.switchView(vReview)

	if cmd := pressShell(m, "s"); cmd != nil {
		t.Errorf("s just marks seen")
	}
	if _, ok := m.seen[m.gl.Review[0].WebURL]; !ok {
		t.Errorf("s marks the MR seen")
	}
	if cmd := pressShell(m, "p"); cmd == nil {
		t.Errorf("p opens the pipeline")
	}
	if cmd := pressShell(m, "o"); cmd == nil {
		t.Errorf("o opens the MR")
	}
	if cmd := pressShell(m, "y"); cmd == nil {
		t.Errorf("y copies the link")
	}

	pressShell(m, "A")
	if !strings.HasPrefix(m.flash, "approve !11 payments") {
		t.Errorf("approve asks: %q", m.flash)
	}
	if pressShell(m, "A") == nil {
		t.Errorf("second A approves")
	}
	pressShell(m, "U")
	if !strings.HasPrefix(m.flash, "unapprove !11 payments") {
		t.Errorf("unapprove asks: %q", m.flash)
	}

	pressShell(m, "c")
	if m.flash != "no Claude sessions linked yet" || m.chooser != nil {
		t.Errorf("c without sessions: %q", m.flash)
	}

	pressShell(m, "j")
	pressShell(m, "p")
	if m.flash != "no pipeline" {
		t.Errorf("draft has no pipeline: %q", m.flash)
	}

	pressShell(m, "k", "enter")
	if m.chooser == nil || m.chooser.title != "review payments in" {
		t.Fatalf("enter reviews with claude in a picked repo when there is no clone")
	}
	pressShell(m, "esc")

	m.repos = []repos.Repo{{Dir: "/code/payments", Project: "g/payments"}, {Dir: "/code/payments-2", Project: "g/payments", Branch: "feat/ABC-1/export"}}
	pressShell(m, "enter")
	if m.chooser == nil || m.chooser.title != "review in clone" {
		t.Fatalf("several clones: pick one")
	}
	if it, _ := m.chooser.selected(); it.value != "/code/payments-2" {
		t.Errorf("the clone on the MR's branch comes first: %v", it.value)
	}
	pressShell(m, "esc")

	pressShell(m, "J")
	if m.view != vJira {
		t.Errorf("J opens the MR's ticket")
	}
}

func TestMineKeysShell(t *testing.T) {
	m := newModelShell(t)
	m.switchView(vMine)

	if cmd := pressShell(m, "A"); cmd != nil || m.flash != "" {
		t.Errorf("no approving your own MRs")
	}
	pressShell(m, "v")
	if m.chooser == nil || m.chooser.title != "threads on !21" {
		t.Fatal("v lists threads")
	}
	pressShell(m, "esc")

	pressShell(m, "f")
	if m.chooser == nil || m.chooser.title != "fix billing in" {
		t.Fatalf("f addresses threads")
	}
	pressShell(m, "esc")

	if cmd := pressShell(m, "enter"); cmd == nil {
		t.Errorf("enter without a session opens the MR")
	}
	m.sessions[1].PRs = []claude.PRLink{{URL: m.gl.Mine[0].WebURL}}
	m.reindex()
	pressShell(m, "enter")
	if m.chooser == nil || m.chooser.title != "sessions for !21" || len(m.chooser.all) != 1 {
		t.Fatalf("enter with linked sessions picks one")
	}
	if it, _ := m.chooser.selected(); !strings.HasPrefix(it.hint, "busy · ") {
		t.Errorf("hint shows status and age: %q", it.hint)
	}
	pressShell(m, "esc")
	pressShell(m, "c")
	if m.chooser == nil {
		t.Errorf("c lists sessions too")
	}
	pressShell(m, "esc")

	pressShell(m, "j")
	for _, k := range []string{"f", "B"} {
		if cmd := pressShell(m, k); cmd != nil || m.chooser != nil {
			t.Errorf("%s does nothing on a merged MR", k)
		}
	}
	if cmd := pressShell(m, "p"); cmd == nil {
		t.Errorf("p on a merged MR opens its merge pipeline")
	}

	m.gl.Mine[0].Threads = nil
	m.reindex()
	m.cur[vMine].pos = 0
	pressShell(m, "f")
	if m.flash != "no open review threads" {
		t.Errorf("f without threads: %q", m.flash)
	}
	pressShell(m, "v")
	if m.flash != "no review threads" {
		t.Errorf("v without threads: %q", m.flash)
	}
}

func TestFailedPipelinesShell(t *testing.T) {
	m := newModelShell(t)
	if open, merged := m.failedSplit(); open != 1 || merged != 1 {
		t.Errorf("own failed head pipeline and failed merge pipeline: %d open, %d merged", open, merged)
	}
	if n := m.pendingReviews(); n != 1 {
		t.Errorf("drafts and approved don't count: %d", n)
	}
}

func TestFailedPillWording(t *testing.T) {
	for _, c := range []struct {
		open, merged int
		want         string
	}{
		{2, 3, "2 failed pipelines · 3 after merge"},
		{1, 0, "1 failed pipeline"},
		{0, 1, "1 pipeline failed after merge"},
		{0, 2, "2 pipelines failed after merge"},
		{0, 0, ""},
	} {
		if got := failedPill(c.open, c.merged); got != c.want {
			t.Errorf("failedPill(%d, %d) = %q, want %q", c.open, c.merged, got, c.want)
		}
	}
}

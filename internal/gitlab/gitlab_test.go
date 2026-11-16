package gitlab

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMRAccessors(t *testing.T) {
	for _, c := range []struct {
		mr            MR
		project, repo string
	}{
		{MR{References: struct {
			Full string `json:"full"`
		}{"acme/backend/payments!12"}}, "acme/backend/payments", "payments"},
		{MR{WebURL: "https://gitlab.example.com/acme/infra/-/merge_requests/3"}, "acme/infra", "infra"},
		{MR{WebURL: "https://gitlab.example.com/acme/infra"}, "", ""},
	} {
		if p, r := c.mr.Project(), c.mr.Repo(); p != c.project || r != c.repo {
			t.Errorf("%+v: got %q %q", c.mr, p, r)
		}
	}
	m := MR{ApprovedBy: []string{"ann", "bob"}, Threads: []Thread{{Resolved: true}, {}, {}}}
	if !m.ApprovedByMe("bob") || m.ApprovedByMe("cy") {
		t.Error("ApprovedByMe")
	}
	if m.PipelineStatus() != "" || m.OpenThreads() != 2 {
		t.Errorf("pipeline %q threads %d", m.PipelineStatus(), m.OpenThreads())
	}
	m.Pipeline = &Pipeline{Status: "running"}
	if m.PipelineStatus() != "running" {
		t.Error("PipelineStatus")
	}
}

const discussions = `[
	{"id":"d1","notes":[{"id":101,"body":"nit","resolvable":true,"resolved":true,"created_at":"2026-11-01T10:00:00Z","author":{"username":"ann"},
		"position":{"new_path":"a.go","new_line":0,"old_line":7}}]},
	{"id":"d2","notes":[{"id":201,"body":"why?","resolvable":true,"created_at":"2026-11-01T11:00:00Z","author":{"username":"bob"},
		"position":{"new_path":"b.go","new_line":3}},{"id":202,"body":"because","author":{"username":"me"}}]},
	{"id":"d3","notes":[{"id":301,"body":"added commit","system":true,"author":{"username":"me"}}]},
	{"id":"d4","notes":[{"id":401,"body":"self note","author":{"username":"me"}}]},
	{"id":"d5","notes":[]},
	{"id":"d6","notes":[{"id":601,"body":"general","author":{"username":"cy"}}]}
]`

func TestFetch(t *testing.T) {
	log := useFakeGlab(t, map[string]string{
		"GET user": `{"id":1,"username":"me","name":"Me"}`,
		"GET merge_requests?per_page=100&reviewer_username=me&scope=all&state=opened": `[
			{"id":11,"iid":5,"project_id":7,"title":"review me","web_url":"https://gitlab.example.com/acme/pay/-/merge_requests/5"}]`,
		"GET merge_requests?per_page=100&scope=created_by_me&state=opened": `[
			{"id":12,"iid":6,"project_id":7,"title":"mine","web_url":"https://gitlab.example.com/acme/pay/-/merge_requests/6","author":{"username":"me"}}]`,
		"GET merge_requests?per_page=30&scope=created_by_me&state=merged": `[
			{"id":13,"iid":4,"project_id":7,"title":"merged","target_branch":"main","merge_commit_sha":"m1","squash_commit_sha":"s1","sha":"h1"},
			{"id":14,"iid":3,"project_id":8,"title":"no ci","target_branch":"main","sha":"h2"}]`,
		"GET projects/7/merge_requests/5":                          `{"head_pipeline":{"id":90,"status":"failed","web_url":"https://p/90"},"detailed_merge_status":"mergeable","sha":"abc"}`,
		"GET projects/7/merge_requests/5/approvals":                `{"approved":true,"approvals_left":0,"approved_by":[{"user":{"username":"ann"}},{"user":{"username":"me"}}]}`,
		"GET projects/7/merge_requests/6/approvals":                `{"approved":false,"approvals_left":1,"approved_by":[]}`,
		"GET projects/7/merge_requests/5/reviewers":                `[{"user":{"username":"ann"},"state":"approved"},{"user":{"username":"me"},"state":"requested_changes"}]`,
		"GET projects/7/merge_requests/6/discussions?per_page=100": discussions,
		"GET projects/7/pipelines?per_page=1&ref=main&sha=m1":      `[]`,
		"GET projects/7/pipelines?per_page=1&ref=main&sha=s1":      `[{"id":91,"status":"success","web_url":"https://p/91"}]`,
	})
	before := time.Now()
	s, err := Fetch()
	if err != nil {
		t.Fatal(err)
	}
	if s.Me.Username != "me" || len(s.Review) != 1 || len(s.Mine) != 1 || len(s.Merged) != 2 {
		t.Fatalf("got %+v", s)
	}
	r := s.Review[0]
	if r.PipelineStatus() != "failed" || r.MergeStatus != "mergeable" || r.SHA != "abc" || !r.Approved || !slices.Equal(r.ApprovedBy, []string{"ann", "me"}) {
		t.Errorf("enriched review %+v", r)
	}
	if r.Threads != nil {
		t.Error("threads fetched for a review MR")
	}
	if r.MyReview != "requested_changes" || !r.ReviewedByMe() {
		t.Errorf("my review state %q", r.MyReview)
	}
	m := s.Mine[0]
	if m.Pipeline != nil || m.Approved || m.ApprovalsLeft != 1 {
		t.Errorf("mine %+v", m)
	}
	var ids []string
	for _, th := range m.Threads {
		ids = append(ids, th.ID)
	}
	if strings.Join(ids, ",") != "d2,d6,d1" {
		t.Errorf("threads %v", ids)
	}
	d2, d1 := m.Threads[0], m.Threads[2]
	if d2.Author != "bob" || d2.Replies != 1 || d2.LastBy != "me" || d2.Path != "b.go" || d2.Line != 3 || d2.Resolved ||
		d2.URL != "https://gitlab.example.com/acme/pay/-/merge_requests/6#note_201" {
		t.Errorf("d2 %+v", d2)
	}
	if !d1.Resolved || d1.Line != 7 {
		t.Errorf("d1 %+v", d1)
	}
	if m.OpenThreads() != 2 {
		t.Errorf("open threads %d", m.OpenThreads())
	}
	if p := s.Merged[0].MergePipeline; p == nil || p.ID != 91 {
		t.Errorf("merge pipeline %+v", p)
	}
	if s.Merged[1].MergePipeline != nil {
		t.Errorf("pipeline for an MR without CI: %+v", s.Merged[1].MergePipeline)
	}
	if s.FetchedAt.Before(before) {
		t.Error("fetched at")
	}

	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "gitlab.example.com GET merge_requests?per_page=30&scope=created_by_me&state=merged&updated_after=") {
		t.Errorf("merged query: %s", calls)
	}
}

func TestFetchErrors(t *testing.T) {
	useFakeGlab(t, map[string]string{})
	if _, err := Fetch(); err == nil || err.Error() != "glab user: 404 Not Found: user" {
		t.Errorf("user: %v", err)
	}
	for _, failing := range []string{"review", "mine", "merged"} {
		routes := map[string]string{
			"GET user": `{"username":"me"}`,
			"GET merge_requests?per_page=100&reviewer_username=me&scope=all&state=opened": `[]`,
			"GET merge_requests?per_page=100&scope=created_by_me&state=opened":            `[]`,
			"GET merge_requests?per_page=30&scope=created_by_me&state=merged":             `[]`,
		}
		for k := range routes {
			if (failing == "review" && strings.Contains(k, "reviewer")) ||
				(failing == "mine" && strings.Contains(k, "opened") && !strings.Contains(k, "reviewer")) ||
				(failing == "merged" && strings.Contains(k, "merged")) {
				delete(routes, k)
			}
		}
		useFakeGlab(t, routes)
		if _, err := Fetch(); err == nil {
			t.Errorf("%s: no error", failing)
		}
	}
	useFakeGlab(t, map[string]string{"GET user": `not json`})
	if _, err := Fetch(); err == nil {
		t.Error("broken JSON")
	}
	t.Setenv("PATH", t.TempDir())
	if err := api("user", nil); err == nil || !strings.HasPrefix(err.Error(), "glab user: ") {
		t.Errorf("no glab: %v", err)
	}
}

func TestActions(t *testing.T) {
	log := useFakeGlab(t, map[string]string{
		"POST projects/7/merge_requests/5/approve":   `{}`,
		"POST projects/7/merge_requests/5/unapprove": `{}`,
		"PUT projects/7/merge_requests/5/rebase":     `{"rebase_in_progress":true}`,
	})
	m := MR{ProjectID: 7, IID: 5}
	for _, f := range []func(MR) error{Approve, Unapprove, Rebase} {
		if err := f(m); err != nil {
			t.Error(err)
		}
	}
	if err := Approve(MR{ProjectID: 7, IID: 6}); err == nil {
		t.Error("approve of a missing MR")
	}
	calls, _ := os.ReadFile(log)
	want := "gitlab.example.com POST projects/7/merge_requests/5/approve\n" +
		"gitlab.example.com POST projects/7/merge_requests/5/unapprove\n" +
		"gitlab.example.com PUT projects/7/merge_requests/5/rebase\n" +
		"gitlab.example.com POST projects/7/merge_requests/6/approve\n"
	if string(calls) != want {
		t.Errorf("calls:\n%s", calls)
	}
}

func TestThreadsWhenGlabFails(t *testing.T) {
	useFakeGlab(t, map[string]string{})
	if got := threads(&MR{ProjectID: 7, IID: 5}); got != nil {
		t.Errorf("got %+v", got)
	}
}

func TestFetchMR(t *testing.T) {
	useFakeGlab(t, map[string]string{
		"GET projects/acme%2Finfra/merge_requests/9": `{"iid":9,"project_id":3,"title":"infra"}`,
		"GET projects/3/merge_requests/9":            `{"head_pipeline":{"status":"success"}}`,
	})
	m, err := FetchMR("acme/infra", 9)
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "infra" || m.PipelineStatus() != "success" || m.Approved {
		t.Errorf("got %+v", m)
	}
	if _, err := FetchMR("acme/none", 1); err == nil {
		t.Error("missing MR")
	}
}

func TestReviewState(t *testing.T) {
	useFakeGlab(t, map[string]string{
		"GET projects/7/merge_requests/1/reviewers": `[{"user":{"username":"ann"},"state":"reviewed"}]`,
	})
	if got := reviewState(&MR{ProjectID: 7, IID: 1}, "me"); got != "" {
		t.Errorf("not a reviewer: %q", got)
	}
	if got := reviewState(&MR{ProjectID: 7, IID: 2}, "me"); got != "" {
		t.Errorf("failed call: %q", got)
	}
	for state, want := range map[string]bool{"reviewed": true, "requested_changes": true, "unreviewed": false, "approved": false, "": false} {
		if (MR{MyReview: state}).ReviewedByMe() != want {
			t.Errorf("ReviewedByMe(%q) != %v", state, want)
		}
	}
}

func TestFailedJobs(t *testing.T) {
	q := "per_page=100&scope%5B%5D=failed"
	useFakeGlab(t, map[string]string{
		"GET projects/7/pipelines/1/jobs?" + q: `[{"name":"audit","stage":"install","allow_failure":true},
			{"name":"bump-version","stage":"git-commit","web_url":"https://j/2"}]`,
		"GET projects/7/pipelines/2/jobs?" + q:    `[{"name":"audit","stage":"install","allow_failure":true}]`,
		"GET projects/7/pipelines/2/bridges?" + q: `[{"name":"deploy-child","stage":"deploy","web_url":"https://j/3"}]`,
	})
	p := &Pipeline{ID: 1, Status: "failed"}
	failedJobs(7, p)
	if len(p.Failed) != 1 || p.Failed[0] != (Job{"bump-version", "git-commit", "https://j/2"}) {
		t.Errorf("jobs allowed to fail only warn: %+v", p.Failed)
	}
	child := &Pipeline{ID: 2, Status: "failed"}
	failedJobs(7, child)
	if len(child.Failed) != 1 || child.Failed[0].Name != "deploy-child" {
		t.Errorf("no failing job of its own: name the child pipeline's trigger, got %+v", child.Failed)
	}
	ok := &Pipeline{ID: 3, Status: "success"}
	failedJobs(7, ok)
	failedJobs(7, nil)
	if ok.Failed != nil {
		t.Error("only failed pipelines are looked into")
	}
}

package gitlab

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MartyJRE/claudia/internal/config"
)

// Everything goes through the `glab` login; claudia keeps no GitLab token of its own.
func Host() string { return config.Get().GitLabHost }

func api(path string, v any) error {
	return call("GET", path, v)
}

func call(method, path string, v any) error {
	out, err := exec.Command("glab", "api", "--hostname", Host(), "-X", method, path).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return fmt.Errorf("glab %s: %s", path, strings.TrimSpace(string(ee.Stderr)))
		}
		return fmt.Errorf("glab %s: %w", path, err)
	}
	if v == nil {
		return nil
	}
	return json.Unmarshal(out, v)
}

type User struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

type Pipeline struct {
	ID     int    `json:"id"`
	Status string `json:"status"`
	WebURL string `json:"web_url"`
}

type MR struct {
	ID                  int       `json:"id"`
	IID                 int       `json:"iid"`
	ProjectID           int       `json:"project_id"`
	Title               string    `json:"title"`
	WebURL              string    `json:"web_url"`
	SourceBranch        string    `json:"source_branch"`
	TargetBranch        string    `json:"target_branch"`
	State               string    `json:"state"`
	Draft               bool      `json:"draft"`
	SHA                 string    `json:"sha"`
	MergeCommitSHA      string    `json:"merge_commit_sha"`
	SquashCommitSHA     string    `json:"squash_commit_sha"`
	MergeStatus         string    `json:"detailed_merge_status"`
	HasConflicts        bool      `json:"has_conflicts"`
	DiscussionsResolved bool      `json:"blocking_discussions_resolved"`
	UserNotesCount      int       `json:"user_notes_count"`
	Author              User      `json:"author"`
	Reviewers           []User    `json:"reviewers"`
	MyReview            string    `json:"my_review,omitempty"` // my reviewer state: unreviewed, reviewed, requested_changes, …
	UpdatedAt           time.Time `json:"updated_at"`
	CreatedAt           time.Time `json:"created_at"`
	MergedAt            time.Time `json:"merged_at"`
	References          struct {
		Full string `json:"full"`
	} `json:"references"`

	Pipeline      *Pipeline `json:"head_pipeline"`
	MergePipeline *Pipeline `json:"merge_pipeline,omitempty"`
	Approved      bool      `json:"approved"`
	ApprovalsLeft int       `json:"approvals_left"`
	ApprovedBy    []string  `json:"approved_by_usernames"`
	Threads       []Thread  `json:"threads,omitempty"`
}

// Project is the path with namespace, e.g. group/backend/payments.
func (m MR) Project() string {
	full := m.References.Full
	if i := strings.LastIndex(full, "!"); i >= 0 {
		return full[:i]
	}
	if i := strings.Index(m.WebURL, "/-/"); i >= 0 {
		u, _ := url.Parse(m.WebURL[:i])
		if u != nil {
			return strings.TrimPrefix(u.Path, "/")
		}
	}
	return ""
}

func (m MR) Repo() string {
	p := m.Project()
	return p[strings.LastIndex(p, "/")+1:]
}

// ReviewedByMe: I reviewed it without approving (left comments or asked for
// changes). GitLab resets that when the author asks for another review.
func (m MR) ReviewedByMe() bool {
	return m.MyReview == "requested_changes" || m.MyReview == "reviewed"
}

func (m MR) ApprovedByMe(me string) bool {
	for _, u := range m.ApprovedBy {
		if u == me {
			return true
		}
	}
	return false
}

func (m MR) PipelineStatus() string {
	if m.Pipeline == nil {
		return ""
	}
	return m.Pipeline.Status
}

type Snapshot struct {
	Me        User      `json:"me"`
	Review    []MR      `json:"review"`
	Mine      []MR      `json:"mine"`
	Merged    []MR      `json:"merged"`
	FetchedAt time.Time `json:"fetched_at"`
}

func Fetch() (Snapshot, error) {
	var s Snapshot
	if err := api("user", &s.Me); err != nil {
		return s, err
	}
	q := url.Values{"state": {"opened"}, "per_page": {"100"}, "scope": {"all"}, "reviewer_username": {s.Me.Username}}
	if err := api("merge_requests?"+q.Encode(), &s.Review); err != nil {
		return s, err
	}
	q = url.Values{"state": {"opened"}, "per_page": {"100"}, "scope": {"created_by_me"}}
	if err := api("merge_requests?"+q.Encode(), &s.Mine); err != nil {
		return s, err
	}
	q = url.Values{"state": {"merged"}, "per_page": {"30"}, "scope": {"created_by_me"},
		"updated_after": {time.Now().Add(-5 * 24 * time.Hour).Format(time.RFC3339)}}
	if err := api("merge_requests?"+q.Encode(), &s.Merged); err != nil {
		return s, err
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	each := func(list []MR, fn func(*MR)) {
		for i := range list {
			wg.Add(1)
			go func(m *MR) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				fn(m)
			}(&list[i])
		}
	}
	each(s.Review, func(m *MR) { enrich(m); m.MyReview = reviewState(m, s.Me.Username) })
	each(s.Mine, func(m *MR) { enrich(m); m.Threads = threads(m) })
	each(s.Merged, mergePipeline)
	wg.Wait()
	s.FetchedAt = time.Now()
	return s, nil
}

// enrich adds what the list endpoint leaves out: head pipeline and approvals.
func enrich(m *MR) {
	var full MR
	if api(fmt.Sprintf("projects/%d/merge_requests/%d", m.ProjectID, m.IID), &full) == nil {
		m.Pipeline = full.Pipeline
		m.MergeStatus = full.MergeStatus
		m.SHA = full.SHA
	}
	var ap struct {
		Approved      bool `json:"approved"`
		ApprovalsLeft int  `json:"approvals_left"`
		ApprovedBy    []struct {
			User User `json:"user"`
		} `json:"approved_by"`
	}
	if api(fmt.Sprintf("projects/%d/merge_requests/%d/approvals", m.ProjectID, m.IID), &ap) == nil {
		m.Approved, m.ApprovalsLeft = ap.Approved, ap.ApprovalsLeft
		m.ApprovedBy = nil
		for _, a := range ap.ApprovedBy {
			m.ApprovedBy = append(m.ApprovedBy, a.User.Username)
		}
	}
}

// reviewState is my state among the MR's reviewers.
func reviewState(m *MR, me string) string {
	var rs []struct {
		User  User   `json:"user"`
		State string `json:"state"`
	}
	if api(fmt.Sprintf("projects/%d/merge_requests/%d/reviewers", m.ProjectID, m.IID), &rs) != nil {
		return ""
	}
	for _, r := range rs {
		if r.User.Username == me {
			return r.State
		}
	}
	return ""
}

// mergePipeline finds the pipeline that ran on what the MR merged into the target
// branch. Some projects (infra) only run CI on the merge commit.
func mergePipeline(m *MR) {
	for _, sha := range []string{m.MergeCommitSHA, m.SquashCommitSHA, m.SHA} {
		if sha == "" {
			continue
		}
		var ps []Pipeline
		q := url.Values{"sha": {sha}, "ref": {m.TargetBranch}, "per_page": {"1"}}
		if api(fmt.Sprintf("projects/%d/pipelines?%s", m.ProjectID, q.Encode()), &ps) == nil && len(ps) > 0 {
			m.MergePipeline = &ps[0]
			return
		}
	}
}

func Approve(m MR) error {
	return call("POST", fmt.Sprintf("projects/%d/merge_requests/%d/approve", m.ProjectID, m.IID), nil)
}

func Unapprove(m MR) error {
	return call("POST", fmt.Sprintf("projects/%d/merge_requests/%d/unapprove", m.ProjectID, m.IID), nil)
}

func Rebase(m MR) error {
	return call("PUT", fmt.Sprintf("projects/%d/merge_requests/%d/rebase", m.ProjectID, m.IID), nil)
}

// Thread is one review discussion on an MR.
type Thread struct {
	ID       string    `json:"id"`
	Author   string    `json:"author"`
	Body     string    `json:"body"`
	Path     string    `json:"path"`
	Line     int       `json:"line"`
	Replies  int       `json:"replies"`
	Resolved bool      `json:"resolved"`
	LastBy   string    `json:"last_by"`
	At       time.Time `json:"at"`
	URL      string    `json:"url"`
}

// threads returns the human review discussions, unresolved first.
func threads(m *MR) []Thread {
	var ds []struct {
		ID    string `json:"id"`
		Notes []struct {
			ID         int       `json:"id"`
			Body       string    `json:"body"`
			System     bool      `json:"system"`
			Resolvable bool      `json:"resolvable"`
			Resolved   bool      `json:"resolved"`
			CreatedAt  time.Time `json:"created_at"`
			Author     User      `json:"author"`
			Position   *struct {
				NewPath string `json:"new_path"`
				NewLine int    `json:"new_line"`
				OldLine int    `json:"old_line"`
			} `json:"position"`
		} `json:"notes"`
	}
	if api(fmt.Sprintf("projects/%d/merge_requests/%d/discussions?per_page=100", m.ProjectID, m.IID), &ds) != nil {
		return nil
	}
	var out []Thread
	for _, d := range ds {
		if len(d.Notes) == 0 || d.Notes[0].System {
			continue
		}
		first, last := d.Notes[0], d.Notes[len(d.Notes)-1]
		t := Thread{
			ID:       d.ID,
			Author:   first.Author.Username,
			Body:     first.Body,
			Replies:  len(d.Notes) - 1,
			Resolved: first.Resolvable && first.Resolved,
			LastBy:   last.Author.Username,
			At:       first.CreatedAt,
			URL:      fmt.Sprintf("%s#note_%d", m.WebURL, first.ID),
		}
		if !first.Resolvable && first.Author.Username == m.Author.Username {
			continue // the author's own general notes aren't review remarks
		}
		if p := first.Position; p != nil {
			t.Path, t.Line = p.NewPath, p.NewLine
			if t.Line == 0 {
				t.Line = p.OldLine
			}
		}
		out = append(out, t)
	}
	sort.SliceStable(out, func(i, j int) bool { return !out[i].Resolved && out[j].Resolved })
	return out
}

func (m MR) OpenThreads() int {
	n := 0
	for _, t := range m.Threads {
		if !t.Resolved {
			n++
		}
	}
	return n
}

// ParseMRURL reads an MR link on the configured host:
// https://host/group/repo/-/merge_requests/123[/diffs…].
func ParseMRURL(raw string) (project string, iid int, ok bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host != Host() {
		return "", 0, false
	}
	path, rest, found := strings.Cut(strings.TrimPrefix(u.Path, "/"), "/-/merge_requests/")
	if !found {
		return "", 0, false
	}
	num, _, _ := strings.Cut(rest, "/")
	if _, err := fmt.Sscanf(num, "%d", &iid); err != nil || path == "" {
		return "", 0, false
	}
	return path, iid, true
}

// FetchMR loads any MR, whether or not it involves you.
func FetchMR(project string, iid int) (MR, error) {
	var m MR
	if err := api(fmt.Sprintf("projects/%s/merge_requests/%d", url.PathEscape(project), iid), &m); err != nil {
		return m, err
	}
	enrich(&m)
	return m, nil
}

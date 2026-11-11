package atlassian

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const jiraTime = "2006-01-02T15:04:05.000-0700"

type fakeJira struct {
	*httptest.Server
	mu    sync.Mutex
	posts []string
}

func newFakeJira(t *testing.T, routes map[string]func(w http.ResponseWriter, r *http.Request)) (*Client, *fakeJira) {
	t.Helper()
	f := &fakeJira{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "me@acme.io" || p != "tok" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != "GET" {
			b, _ := io.ReadAll(r.Body)
			f.mu.Lock()
			f.posts = append(f.posts, r.Method+" "+r.URL.Path+" "+r.Header.Get("Content-Type")+" "+string(b))
			f.mu.Unlock()
		}
		h, ok := routes[r.URL.Path]
		if !ok {
			http.Error(w, `{"errorMessages":["no such thing"]}`, http.StatusNotFound)
			return
		}
		h(w, r)
	}))
	t.Cleanup(f.Close)
	return &Client{Base: f.URL, email: "me@acme.io", token: "tok", http: f.Client()}, f
}

func reply(body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, body) }
}

func newestFirst(t *testing.T, body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("orderBy"); got != "-created" {
			t.Errorf("comments ordered by %q; the oldest 100 would hide recent mentions", got)
		}
		io.WriteString(w, body)
	}
}

func TestNew(t *testing.T) {
	t.Setenv("ATLASSIAN_EMAIL", "")
	t.Setenv("ATLASSIAN_API_TOKEN", "")
	if _, err := New(); !errors.Is(err, ErrNoCredentials) {
		t.Errorf("without credentials: %v", err)
	}
	t.Setenv("ATLASSIAN_EMAIL", "me@acme.io")
	t.Setenv("ATLASSIAN_API_TOKEN", "tok")
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if c.Base != "https://acme.atlassian.net" || c.IssueURL("PAY-1") != "https://acme.atlassian.net/browse/PAY-1" {
		t.Errorf("base %q", c.Base)
	}
}

func TestCurrentSprint(t *testing.T) {
	for _, c := range []struct {
		in   []Sprint
		want int
	}{
		{[]Sprint{{ID: 1, State: "closed"}, {ID: 3, State: "future"}, {ID: 2, State: "active"}}, 2},
		{[]Sprint{{ID: 5, State: "future"}, {ID: 4, State: "future"}, {ID: 1, State: "closed"}}, 4},
		{[]Sprint{{ID: 1, State: "closed"}, {ID: 7, State: "closed"}, {ID: 3, State: "closed"}}, 7},
	} {
		if got := currentSprint(c.in); got == nil || got.ID != c.want {
			t.Errorf("%+v: got %+v want %d", c.in, got, c.want)
		}
	}
	if currentSprint(nil) != nil {
		t.Error("sprint from nothing")
	}
}

func issueJSON(key, summary, status, cat, assignee string, level int, extra string) string {
	a := "null"
	if assignee != "" {
		a = `{"accountId":"` + assignee + `","displayName":"` + strings.ToUpper(assignee) + `"}`
	}
	return fmt.Sprintf(`{"key":%q,"fields":{"summary":%q,"status":{"name":%q,"statusCategory":{"key":%q}},
		"issuetype":{"name":"Story","hierarchyLevel":%d},"priority":{"name":"High"},"assignee":%s,
		"updated":"2026-11-02T10:30:00.000+0100"%s}}`, key, summary, status, cat, level, a, extra)
}

func TestFetchJira(t *testing.T) {
	now := time.Now()
	recent := now.Add(-time.Hour).Format(jiraTime)
	later := now.Add(-time.Minute).Format(jiraTime)
	ancient := now.Add(-40 * 24 * time.Hour).Format(jiraTime)
	mention := func(id string) string {
		return `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"mention","attrs":{"id":"` + id + `","text":"@Me"}},{"type":"text","text":" can you\n  look?"}]}]}`
	}
	var (
		mu          sync.Mutex
		fieldsAsked []string
	)
	c, _ := newFakeJira(t, map[string]func(http.ResponseWriter, *http.Request){
		"/rest/api/3/myself": reply(`{"accountId":"me"}`),
		"/rest/api/3/field": reply(`[{"id":"summary","schema":{}},
			{"id":"customfield_10020","schema":{"custom":"com.pyxis.greenhopper.jira:gh-sprint"}}]`),
		"/rest/api/3/search/jql": func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			mu.Lock()
			fieldsAsked = append(fieldsAsked, q.Get("fields"))
			mu.Unlock()
			jql := q.Get("jql")
			switch {
			case strings.HasPrefix(jql, "assignee = currentUser()") && q.Get("nextPageToken") == "":
				io.WriteString(w, `{"nextPageToken":"p2","issues":[`+issueJSON("PAY-1", "one", "In Progress", "indeterminate", "me", 0,
					`,"customfield_10020":[{"id":1,"name":"S1","state":"closed"},{"id":2,"name":"S2","state":"active"}],
					"parent":{"key":"PAY-100","fields":{"summary":"Epic","status":{"name":"Open","statusCategory":{"key":"new"}},"issuetype":{"name":"Epic"}}}`)+`]}`)
			case strings.HasPrefix(jql, "assignee = currentUser()"):
				blocked := func(dir, key, cat string) string {
					return `{"type":{"inward":"is blocked by","outward":"blocks"},"` + dir + `":{"key":"` + key + `","fields":{"summary":"x","status":{"name":"s","statusCategory":{"key":"` + cat + `"}}}}}`
				}
				io.WriteString(w, `{"issues":[`+issueJSON("PAY-2", "two", "To Do", "new", "me", -1,
					`,"issuelinks":[`+blocked("inwardIssue", "PAY-5", "indeterminate")+","+blocked("inwardIssue", "PAY-6", "done")+","+blocked("outwardIssue", "PAY-7", "new")+`]`)+`]}`)
			case strings.HasPrefix(jql, "project = PAY"):
				io.WriteString(w, `{"issues":[`+issueJSON("PAY-3", "three", "Done", "done", "bob", 0, "")+","+
					issueJSON("PAY-4", "four", "To Do", "new", "", 0, "")+`]}`)
			case strings.HasPrefix(jql, "comment ~ currentUser()"):
				io.WriteString(w, `{"issues":[`+issueJSON("PAY-9", "asked", "To Do", "new", "", 0, "")+","+
					issueJSON("PAY-8", "broken", "To Do", "new", "", 0, "")+`]}`)
			default:
				http.Error(w, "bad jql "+jql, 400)
			}
		},
		"/rest/api/3/issue/PAY-9/comment": newestFirst(t, `{"comments":[
			{"id":"10","author":{"accountId":"bob","displayName":"Bob"},"created":"`+recent+`","body":`+mention("me")+`},
			{"id":"11","author":{"accountId":"me","displayName":"Me"},"created":"`+later+`","body":`+mention("bob")+`},
			{"id":"12","author":{"accountId":"ann","displayName":"Ann"},"created":"`+later+`","body":`+mention("me")+`},
			{"id":"13","author":{"accountId":"ann","displayName":"Ann"},"created":"`+later+`","body":`+mention("bob")+`},
			{"id":"14","author":{"accountId":"ann","displayName":"Ann"},"created":"`+ancient+`","body":`+mention("me")+`}]}`),
	})

	s, err := c.FetchJira()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Sprint) != 2 || s.Sprint[0].Key != "PAY-1" || s.Sprint[1].Key != "PAY-2" {
		t.Fatalf("sprint %+v", s.Sprint)
	}
	one := s.Sprint[0]
	if one.Sprint == nil || one.Sprint.Name != "S2" || !one.Mine || one.Assignee != "ME" || one.Category != "indeterminate" || one.Priority != "High" {
		t.Errorf("PAY-1 %+v", one)
	}
	if one.Parent != "PAY-100" || one.ParentSummary != "Epic" || one.ParentType != "Epic" || one.ParentStatus != "Open" || one.ParentCategory != "new" {
		t.Errorf("parent %+v", one)
	}
	if want := time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC); !one.Updated.Equal(want) {
		t.Errorf("updated %v", one.Updated)
	}
	if s.Sprint[1].Level != -1 || s.Sprint[1].Sprint != nil || !slices.Equal(s.Sprint[1].BlockedBy, []string{"PAY-5"}) {
		t.Errorf("PAY-2 %+v", s.Sprint[1])
	}
	if len(s.Team) != 2 || s.Team[0].Mine || s.Team[0].Assignee != "BOB" || s.Team[1].Assignee != "" {
		t.Errorf("team %+v", s.Team)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.HasSuffix(fieldsAsked[0], ",updated,issuelinks,customfield_10020") {
		t.Errorf("fields %q", fieldsAsked[0])
	}
	if len(s.Mentions) != 2 {
		t.Fatalf("mentions %+v", s.Mentions)
	}
	if m := s.Mentions[0]; m.CommentID != "12" || m.Answered || m.Author != "Ann" || m.Text != "@Me can you look?" {
		t.Errorf("unanswered first: %+v", m)
	}
	if m := s.Mentions[1]; m.CommentID != "10" || !m.Answered || m.URL != c.Base+"/browse/PAY-9?focusedCommentId=10" || m.Summary != "asked" {
		t.Errorf("answered: %+v", m)
	}
	if s.FetchedAt.IsZero() {
		t.Error("fetched at")
	}
}

func TestSearchStopsAt300(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	c, _ := newFakeJira(t, map[string]func(http.ResponseWriter, *http.Request){
		"/rest/api/3/search/jql": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			calls++
			n := calls
			mu.Unlock()
			var is []string
			for i := range 100 {
				is = append(is, issueJSON(fmt.Sprintf("PAY-%d", n*1000+i), "", "", "", "", 0, ""))
			}
			io.WriteString(w, `{"nextPageToken":"more","issues":[`+strings.Join(is, ",")+`]}`)
		},
	})
	got, err := c.search("x", "me")
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 300 || calls != 3 {
		t.Errorf("got %d issues in %d calls", len(got), calls)
	}
}

func TestErrors(t *testing.T) {
	c, _ := newFakeJira(t, map[string]func(http.ResponseWriter, *http.Request){
		"/rest/api/3/search/jql": func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "jql broken", 400) },
	})
	if _, err := c.FetchJira(); err == nil || !strings.Contains(err.Error(), "/rest/api/3/myself: 404 Not Found") {
		t.Errorf("myself: %v", err)
	}
	if _, err := c.search("x", ""); err == nil || !strings.Contains(err.Error(), "400 Bad Request jql broken") {
		t.Errorf("search: %v", err)
	}
	if _, err := c.Mentions("me"); err == nil {
		t.Error("mentions without search")
	}
	if _, err := c.Transitions("PAY-1"); err == nil {
		t.Error("transitions")
	}
	if _, err := c.FetchConfluence(); err == nil {
		t.Error("confluence")
	}
	if _, err := c.ExportBrief("PAY-1", t.TempDir()); err == nil {
		t.Error("export")
	}
	bad := &Client{Base: "http://127.0.0.1:1", http: http.DefaultClient}
	if _, err := bad.Me(); err == nil {
		t.Error("unreachable")
	}
	if err := c.do("GET", "/x", nil, func() {}, nil); err == nil {
		t.Error("unmarshalable body")
	}
}

func TestTransitions(t *testing.T) {
	c, f := newFakeJira(t, map[string]func(http.ResponseWriter, *http.Request){
		"/rest/api/3/issue/PAY-1/transitions": func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "POST" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			io.WriteString(w, `{"transitions":[{"id":"21","name":"Start","to":{"name":"In Progress"}},{"id":"31","name":"Finish","to":{"name":"Done"}}]}`)
		},
	})
	ts, err := c.Transitions("PAY-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 2 || ts[1] != (Transition{"31", "Finish", "Done"}) {
		t.Errorf("got %+v", ts)
	}
	if err := c.Transition("PAY-1", "31"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.posts) != 1 || f.posts[0] != `POST /rest/api/3/issue/PAY-1/transitions application/json {"transition":{"id":"31"}}` {
		t.Errorf("posted %v", f.posts)
	}
}

func TestFetchConfluence(t *testing.T) {
	var (
		mu   sync.Mutex
		cqls []string
	)
	c, _ := newFakeJira(t, map[string]func(http.ResponseWriter, *http.Request){
		"/wiki/rest/api/search": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			cqls = append(cqls, r.URL.Query().Get("cql"))
			mu.Unlock()
			if r.URL.Query().Get("expand") != "content.space,content.container" {
				http.Error(w, "query", 400)
				return
			}
			if strings.HasPrefix(r.URL.Query().Get("cql"), "type = comment") {
				if r.URL.Query().Get("limit") != "100" {
					http.Error(w, "limit", 400)
					return
				}
				io.WriteString(w, `{"results":[{"content":{"id":"9","type":"comment","container":{"id":"7"}},"lastModified":"2026-11-03T17:28:25Z"}]}`)
				return
			}
			io.WriteString(w, `{"results":[{"content":{"id":"1","type":"comment","space":{"key":"ENG"},"container":{"id":7}},
				"title":"Q&amp;A","excerpt":"hi","url":"/spaces/ENG/pages/1","lastModified":"2026-11-01T12:00:00Z"},
				{"content":{"id":"2","type":"page","space":{"key":"OPS"}},"title":"Runbook","url":"/x","lastModified":"junk"}]}`)
		},
	})
	s, err := c.FetchConfluence()
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(cqls) != 3 || !strings.HasPrefix(cqls[0], "mention = currentUser()") || !strings.HasPrefix(cqls[1], "contributor = currentUser()") ||
		!strings.HasPrefix(cqls[2], "type = comment and creator = currentUser()") {
		t.Errorf("cql %q", cqls)
	}
	if p := s.Mentions[0]; p.OnPage != "7" {
		t.Errorf("a comment is on its container page, got %q", p.OnPage)
	}
	if q := s.Mentions[1]; q.OnPage != "2" {
		t.Errorf("a page is on itself, got %q", q.OnPage)
	}
	if len(s.Comments) != 1 || s.Comments[0].OnPage != "7" {
		t.Errorf("my comments %+v", s.Comments)
	}
	p := s.Mentions[0]
	if p.Title != "Q&A" || !p.Comment || p.Space != "ENG" || p.URL != c.Base+"/wiki/spaces/ENG/pages/1" || !p.Modified.Equal(time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("page %+v", p)
	}
	if q := s.Mine[1]; q.Comment || !q.Modified.IsZero() || q.ID != "2" {
		t.Errorf("page %+v", q)
	}
}

func TestExportBrief(t *testing.T) {
	png := []byte("\x89PNG fake")
	var c *Client
	var srv *fakeJira
	c, srv = newFakeJira(t, map[string]func(http.ResponseWriter, *http.Request){
		"/rest/api/3/issue/PAY-7": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("fields") != "*all" || r.URL.Query().Get("expand") != "names" {
				http.Error(w, "query", 400)
				return
			}
			para := func(s string) string {
				return `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"` + s + `"}]}]}`
			}
			b, _ := json.Marshal(map[string]any{
				"names": map[string]string{"customfield_1": "Acceptance", "customfield_2": "Zeta notes", "customfield_3": "Plain"},
				"fields": map[string]any{
					"summary":       "Pay faster",
					"status":        map[string]string{"name": "To Do"},
					"issuetype":     map[string]string{"name": "Story"},
					"parent":        map[string]any{"key": "PAY-1", "fields": map[string]string{"summary": "Epic"}},
					"description":   json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"See"}]},{"type":"mediaSingle","content":[{"type":"media","attrs":{"alt":"inline.png"}}]}]}`),
					"customfield_2": json.RawMessage(para("last")),
					"customfield_1": json.RawMessage(para("first")),
					"customfield_3": "not adf",
					"issuelinks": []any{
						map[string]any{"type": map[string]string{"inward": "is blocked by", "outward": "blocks"},
							"outwardIssue": map[string]any{"key": "PAY-8", "fields": map[string]any{"summary": "Next", "status": map[string]string{"name": "Open"}}}},
						map[string]any{"type": map[string]string{"inward": "is blocked by", "outward": "blocks"},
							"inwardIssue": map[string]any{"key": "PAY-6", "fields": map[string]any{"summary": "Prev", "status": map[string]string{"name": "Done"}}}},
					},
					"attachment": []any{
						map[string]any{"filename": "inline.png", "mimeType": "image/png", "size": 10, "content": srv.URL + "/att/1"},
						map[string]any{"filename": "other.png", "mimeType": "image/png", "size": 10, "content": srv.URL + "/att/2"},
						map[string]any{"filename": "other.png", "mimeType": "image/png", "size": 10, "content": srv.URL + "/att/2"},
						map[string]any{"filename": "gone.png", "mimeType": "image/png", "size": 10, "content": srv.URL + "/att/404"},
						map[string]any{"filename": "huge.png", "mimeType": "image/png", "size": maxImageSize + 1, "content": srv.URL + "/att/1"},
						map[string]any{"filename": "spec.pdf", "mimeType": "application/pdf", "size": 10, "content": srv.URL + "/att/1"},
					},
					"comment": map[string]any{"comments": []any{
						map[string]any{"author": map[string]string{"displayName": "Bob"}, "created": "2026-11-01T10:00:00.000+0000", "body": json.RawMessage(para("ship it"))},
					}},
				},
			})
			w.Write(b)
		},
		"/att/1": func(w http.ResponseWriter, _ *http.Request) { w.Write(png) },
		"/att/2": func(w http.ResponseWriter, _ *http.Request) { w.Write(png) },
	})
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "PAY-7"), 0o755)
	os.WriteFile(filepath.Join(dir, "PAY-7", "stale.txt"), nil, 0o644)

	path, err := c.ExportBrief("PAY-7", dir)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "PAY-7")
	if path != filepath.Join(out, "story.md") {
		t.Errorf("path %s", path)
	}
	entries, _ := os.ReadDir(out)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != "inline.png,other (2).png,other.png,story.md" {
		t.Errorf("files %v", names)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "inline.png")); string(b) != string(png) {
		t.Errorf("image %q", b)
	}
	md, _ := os.ReadFile(path)
	inline, other := filepath.Join(out, "inline.png"), filepath.Join(out, "other.png")
	want := "# PAY-7: Pay faster\n\n" +
		"- Type: Story\n- Status: To Do\n- Link: " + c.Base + "/browse/PAY-7\n- Parent: PAY-1 Epic\n\n" +
		"## Description\n\nSee\n\n![inline.png](" + inline + ")\n\n" +
		"## Acceptance\n\nfirst\n\n" +
		"## Zeta notes\n\nlast\n\n" +
		"## Linked issues\n\n- blocks PAY-8: Next (Open)\n- is blocked by PAY-6: Prev (Done)\n\n" +
		"## Comments\n\n### Bob, 2026-11-01\n\nship it\n\n" +
		"## Other attached images\n\n![other.png](" + filepath.Join(out, "other (2).png") + ")\n![other.png](" + other + ")\n"
	if string(md) != want {
		t.Errorf("got:\n%s\nwant:\n%s", md, want)
	}
}

func TestDownloadError(t *testing.T) {
	c, _ := newFakeJira(t, nil)
	var b strings.Builder
	if err := c.download(c.Base+"/missing", &b); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("got %v", err)
	}
	if err := c.download("://bad", &b); err == nil {
		t.Error("bad url")
	}
}

func TestTrimDate(t *testing.T) {
	if trimDate("2026-11-01T10:00") != "2026-11-01" || trimDate("short") != "short" {
		t.Error("trimDate")
	}
}

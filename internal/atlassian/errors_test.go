package atlassian

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/config"
)

func TestNewWithoutSite(t *testing.T) {
	old := siteConfig
	siteConfig = func() config.Config { return config.Config{} }
	t.Cleanup(func() { siteConfig = old })
	t.Setenv("ATLASSIAN_EMAIL", "me@acme.io")
	t.Setenv("ATLASSIAN_API_TOKEN", "tok")
	if _, err := New(); err == nil || err.Error() != "set atlassian_url in "+config.Path() {
		t.Errorf("got %v", err)
	}
}

func TestBadBaseAndUnreachableDownload(t *testing.T) {
	c := &Client{Base: "://bad", http: http.DefaultClient}
	if err := c.do("GET", "/x", nil, nil, nil); err == nil || !strings.Contains(err.Error(), "missing protocol scheme") {
		t.Errorf("do: %v", err)
	}
	var b strings.Builder
	if err := c.download("http://127.0.0.1:1/x", &b); err == nil || b.Len() != 0 {
		t.Errorf("download: %v %q", err, b.String())
	}
}

func TestRawIssueRejectsBadJSON(t *testing.T) {
	var r rawIssue
	if err := json.Unmarshal([]byte(`{"key": 5}`), &r); err == nil {
		t.Error("number as key")
	}
}

// failing serves the FetchJira routes, failing the search whose jql starts with prefix.
func failing(t *testing.T, prefix string) *Client {
	c, _ := newFakeJira(t, map[string]func(http.ResponseWriter, *http.Request){
		"/rest/api/3/myself": reply(`{"accountId":"me"}`),
		"/rest/api/3/search/jql": func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Query().Get("jql"), prefix) {
				http.Error(w, "no "+prefix, 400)
				return
			}
			io.WriteString(w, `{"issues":[]}`)
		},
	})
	return c
}

func TestFetchJiraStopsAtFirstFailingSearch(t *testing.T) {
	for _, prefix := range []string{"assignee = currentUser()", "project = PAY", "comment ~ currentUser()"} {
		_, err := failing(t, prefix).FetchJira()
		if err == nil || !strings.Contains(err.Error(), "no "+prefix) {
			t.Errorf("%s: %v", prefix, err)
		}
	}
}

func TestFetchJiraWithoutTeamProject(t *testing.T) {
	old := siteConfig
	siteConfig = func() config.Config { return config.Config{} }
	t.Cleanup(func() { siteConfig = old })
	s, err := failing(t, "project = ").FetchJira()
	if err != nil || s.Team != nil || s.FetchedAt.IsZero() {
		t.Errorf("got %+v %v", s, err)
	}
}

func TestFetchConfluenceSecondQueryFails(t *testing.T) {
	c, _ := newFakeJira(t, map[string]func(http.ResponseWriter, *http.Request){
		"/wiki/rest/api/search": func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Query().Get("cql"), "contributor") {
				http.Error(w, "mine broken", 500)
				return
			}
			io.WriteString(w, `{"results":[{"content":{"id":"1"},"title":"T"}]}`)
		},
	})
	s, err := c.FetchConfluence()
	if err == nil || !strings.Contains(err.Error(), "mine broken") || len(s.Mentions) != 1 || !s.FetchedAt.IsZero() {
		t.Errorf("got %+v %v", s, err)
	}
}

func TestMentionsNewestFirstWithinAnswered(t *testing.T) {
	now := time.Now()
	body := func(id, at string) string {
		return `{"comments":[{"id":"` + id + `","author":{"accountId":"bob","displayName":"Bob"},"created":"` + at + `",` +
			`"body":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"mention","attrs":{"id":"me"}}]}]}}]}`
	}
	c, _ := newFakeJira(t, map[string]func(http.ResponseWriter, *http.Request){
		"/rest/api/3/search/jql": reply(`{"issues":[` + issueJSON("PAY-1", "old", "To Do", "new", "", 0, "") + "," +
			issueJSON("PAY-2", "new", "To Do", "new", "", 0, "") + "," + issueJSON("PAY-3", "mid", "To Do", "new", "", 0, "") + `]}`),
		"/rest/api/3/issue/PAY-1/comment": reply(body("1", now.Add(-3*time.Hour).Format(jiraTime))),
		"/rest/api/3/issue/PAY-2/comment": reply(body("2", now.Add(-time.Hour).Format(jiraTime))),
		"/rest/api/3/issue/PAY-3/comment": reply(body("3", now.Add(-2*time.Hour).Format(jiraTime))),
	})
	ms, err := c.Mentions("me")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range ms {
		got = append(got, m.Key)
	}
	if strings.Join(got, ",") != "PAY-2,PAY-3,PAY-1" {
		t.Errorf("got %v", got)
	}
}

func exportServer(t *testing.T, fields string, routes map[string]func(http.ResponseWriter, *http.Request)) *Client {
	if routes == nil {
		routes = map[string]func(http.ResponseWriter, *http.Request){}
	}
	routes["/rest/api/3/issue/PAY-7"] = reply(`{"fields":` + fields + `}`)
	c, _ := newFakeJira(t, routes)
	return c
}

func TestExportBriefBadFields(t *testing.T) {
	c := exportServer(t, `{"summary": 5}`, nil)
	if _, err := c.ExportBrief("PAY-7", t.TempDir()); err == nil {
		t.Error("summary as number")
	}
}

func readOnly(t *testing.T, dir string) {
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
}

func TestExportBriefCantReplaceFolder(t *testing.T) {
	c := exportServer(t, `{"summary": "s"}`, nil)
	dir := t.TempDir()
	out := filepath.Join(dir, "PAY-7")
	os.MkdirAll(out, 0o755)
	os.WriteFile(filepath.Join(out, "stale.txt"), nil, 0o644)
	readOnly(t, out)
	if _, err := c.ExportBrief("PAY-7", dir); err == nil {
		t.Error("stale folder that can't be emptied")
	}
}

func TestExportBriefCantCreateFolder(t *testing.T) {
	c := exportServer(t, `{"summary": "s"}`, nil)
	dir := t.TempDir()
	readOnly(t, dir)
	if _, err := c.ExportBrief("PAY-7", dir); err == nil {
		t.Error("read-only parent")
	}
}

func TestExportBriefSkipsImageItCantCreate(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "PAY-7")
	c, _ := newFakeJira(t, map[string]func(http.ResponseWriter, *http.Request){
		"/rest/api/3/issue/PAY-7": func(w http.ResponseWriter, r *http.Request) {
			att := func(name string) map[string]any {
				return map[string]any{"filename": name, "mimeType": "image/png", "size": 3, "content": "http://" + r.Host + "/att/1"}
			}
			json.NewEncoder(w).Encode(map[string]any{"fields": map[string]any{"summary": "s", "attachment": []any{att("a.png"), att("b.png")}}})
		},
		// While the first image downloads, the second one's name becomes a link
		// into a folder that doesn't exist, so creating it fails.
		"/att/1": func(w http.ResponseWriter, _ *http.Request) {
			os.Symlink(filepath.Join(dir, "missing", "b.png"), filepath.Join(out, "b.png"))
			io.WriteString(w, "png")
		},
	})
	path, err := c.ExportBrief("PAY-7", dir)
	if err != nil {
		t.Fatal(err)
	}
	md, _ := os.ReadFile(path)
	if !strings.Contains(string(md), "![a.png]") || strings.Contains(string(md), "![b.png]") {
		t.Errorf("got:\n%s", md)
	}
}

func TestUniquePathGivesUpOnUnstatableNames(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("x", 300) + ".png"
	done := make(chan string, 1)
	go func() { done <- uniquePath(dir, long) }()
	select {
	case p := <-done:
		if p != filepath.Join(dir, long) {
			t.Errorf("got %q", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("uniquePath loops on a name the filesystem can't stat")
	}
}

package teams

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
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/state"
)

func TestMain(m *testing.M) {
	dir, _ := os.MkdirTemp("", "claudia-test")
	os.Setenv("XDG_CONFIG_HOME", dir)
	os.Setenv("XDG_STATE_HOME", dir)
	os.Setenv("CLAUDIA_GITLAB_HOST", "gitlab.example.com")
	os.MkdirAll(filepath.Join(dir, "claudia"), 0o755)
	os.WriteFile(filepath.Join(dir, "claudia", "config.json"), []byte(`{"teams":{"tenant":"tn","client_id":"app","team_id":"T","channel_id":"C"}}`), 0o600)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

const card = `{\"type\":\"AdaptiveCard\",\"actions\":[{\"type\":\"Action.OpenUrl\",\"title\":\"Open MR !134\",\"url\":\"https://gitlab.example.com/libs/lb4-party/-/merge_requests/134\"},{\"type\":\"Action.OpenUrl\",\"url\":\"https://gitlab.example.com/libs/mono/-/merge_requests/9\"}]}`

var graph = `{"value": [
  {"id": "1730650000000", "createdDateTime": "2026-11-03T18:44:00Z", "webUrl": "https://teams.microsoft.com/l/message/x/1730650000000",
   "from": {"user": {"displayName": "Lukas Laczi"}},
   "body": {"contentType": "html", "content": "<at id=\"0\">Martin Prazak</at> review for !134 &mdash; refactor<attachment id=\"a\"></attachment>"},
   "mentions": [{"id": 0, "mentioned": {"user": {"displayName": "Martin Prazak"}}}],
   "attachments": [{"contentType": "application/vnd.microsoft.card.adaptive", "content": "` + card + `"}],
   "replies": [
     {"id": "2", "createdDateTime": "2026-11-03T19:10:00Z", "from": {"user": {"displayName": "Martin Prazak"}}, "body": {"content": "<p>approved</p>"}},
     {"id": "1", "createdDateTime": "2026-11-03T19:00:00Z", "from": {"user": {"displayName": "Lukas Laczi"}}, "body": {"content": "ping"}},
     {"id": "3", "createdDateTime": "2026-11-03T19:20:00Z", "deletedDateTime": "2026-11-03T19:21:00Z", "from": null, "body": {"content": ""}}
   ]},
  {"id": "old", "createdDateTime": "2026-11-01T10:00:00Z", "from": {"application": {"displayName": "GitLab"}},
   "body": {"content": "see https://gitlab.example.com/libs/lb4-party/-/merge_requests/134 and https://gitlab.example.com/libs/lb4-party/-/merge_requests/134"}},
  {"id": "gone", "createdDateTime": "2026-11-02T10:00:00Z", "deletedDateTime": "2026-11-02T11:00:00Z", "from": null, "body": {"content": ""}}
]}`

func TestParse(t *testing.T) {
	posts, err := Parse([]byte(graph), "gitlab.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 2 {
		t.Fatalf("deleted posts should be dropped, got %d", len(posts))
	}
	p := posts[0]
	if p.From != "Lukas Laczi" || p.Text != "Martin Prazak review for !134 — refactor" {
		t.Errorf("post = %q / %q", p.From, p.Text)
	}
	want := []string{"https://gitlab.example.com/libs/lb4-party/-/merge_requests/134", "https://gitlab.example.com/libs/mono/-/merge_requests/9"}
	if strings.Join(p.MRURLs, " ") != strings.Join(want, " ") {
		t.Errorf("MRs from the card = %v", p.MRURLs)
	}
	if len(p.Replies) != 2 || p.Replies[0].Text != "ping" || p.Replies[1].Text != "approved" {
		t.Errorf("replies should be oldest first without deleted ones: %+v", p.Replies)
	}
	if !p.Mentions("martin prazak") || !p.RepliedBy("Martin Prazak") || p.RepliedBy("") || p.Mentions("") {
		t.Error("mention / replied-by checks")
	}
	if posts[1].From != "GitLab" || len(posts[1].MRURLs) != 1 {
		t.Errorf("app post = %+v", posts[1])
	}

	s := Snapshot{Posts: posts}
	if got := s.ForMR(want[0]); len(got) != 2 || got[0].ID != "1730650000000" {
		t.Errorf("ForMR should list both posts, newest first: %+v", got)
	}
	if _, ok := s.Post("old"); !ok {
		t.Error("Post by id")
	}
	if _, ok := s.Post("nope"); ok {
		t.Error("unknown id")
	}
}

func TestParseBareArrayAndGarbage(t *testing.T) {
	posts, err := Parse([]byte(`[{"id":"1","body":{"content":"hi"}}]`), "gitlab.example.com")
	if err != nil || len(posts) != 1 || posts[0].From != "" {
		t.Fatalf("bare array: %v %+v", err, posts)
	}
	if _, err := Parse([]byte(`"accepted"`), "gitlab.example.com"); err == nil {
		t.Error("a non-list answer should fail")
	}
}

type fake struct {
	t        *testing.T
	pending  int // device-code polls answered with authorization_pending
	refresh  string
	expired  bool // the refresh token was revoked
	requests []string
	bodies   []map[string]any
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	switch {
	case r.URL.Path == "/login/devicecode":
		if r.FormValue("client_id") != "app" || r.FormValue("scope") != scopes {
			f.t.Errorf("devicecode form: %v", r.Form)
		}
		io.WriteString(w, `{"device_code":"dc","message":"go to example.com/devicelogin and enter ABC","interval":1,"expires_in":60}`)
	case r.URL.Path == "/login/token":
		switch r.FormValue("grant_type") {
		case "refresh_token":
			if f.expired {
				w.WriteHeader(400)
				io.WriteString(w, `{"error":"invalid_grant","error_description":"AADSTS700082: expired\r\nTrace ID: x"}`)
				return
			}
			f.refresh = r.FormValue("refresh_token")
			io.WriteString(w, `{"access_token":"at2","expires_in":3600,"scope":"`+allScopes+`"}`)
		default:
			if f.pending > 0 {
				f.pending--
				w.WriteHeader(400)
				io.WriteString(w, `{"error":"authorization_pending"}`)
				return
			}
			io.WriteString(w, `{"access_token":"at","refresh_token":"rt","expires_in":3600,"scope":"`+allScopes+`"}`)
		}
	case r.URL.Path == "/graph/me":
		io.WriteString(w, `{"id":"me-id","displayName":"Martin Prazak"}`)
	case r.URL.Path == "/graph/me/chats":
		io.WriteString(w, chatsJSON)
	case r.URL.Path == "/graph/me/calendarView":
		if r.Header.Get("Prefer") != `outlook.timezone="UTC"` {
			f.t.Errorf("calendar times should come in UTC")
		}
		io.WriteString(w, eventsJSON)
	case r.URL.Path == "/graph/me/joinedTeams":
		io.WriteString(w, `{"value":[{"id":"T","displayName":"Technology"}]}`)
	case r.URL.Path == "/graph/teams/T/channels":
		io.WriteString(w, `{"value":[{"id":"C","displayName":"Code Review"},{"id":"P","displayName":"Private"}]}`)
	case r.URL.Path == "/graph/teams/T/channels/P/messages":
		w.WriteHeader(403)
	case strings.HasPrefix(r.URL.Path, "/graph/chats/"):
		var v map[string]any
		_ = json.NewDecoder(r.Body).Decode(&v)
		f.bodies = append(f.bodies, v)
		io.WriteString(w, `{"id":"new"}`)
	case strings.HasPrefix(r.URL.Path, "/graph/teams/T/channels/C/messages"):
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(401)
			return
		}
		var v map[string]any
		_ = json.NewDecoder(r.Body).Decode(&v)
		f.bodies = append(f.bodies, v)
		if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/messages") {
			if r.URL.Query().Get("$expand") != "replies" {
				f.t.Errorf("list should expand replies: %s", r.URL)
			}
			io.WriteString(w, graph)
			return
		}
		if r.Method == "GET" {
			io.WriteString(w, `{"id":"r1","body":{"content":"root"}}`)
			return
		}
		if strings.Contains(r.URL.Path, "/bad/") {
			http.Error(w, `{"error":{"code":"Forbidden"}}`, http.StatusForbidden)
			return
		}
		io.WriteString(w, `{"id":"new"}`)
	default:
		f.t.Errorf("unexpected %s %s", r.Method, r.URL)
		w.WriteHeader(404)
	}
}

func newTestClient(t *testing.T, f *fake) *Client {
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	os.Remove(state.Path(tokenFile))
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	c.login, c.graph = srv.URL+"/login", srv.URL+"/graph"
	return c
}

func TestLoginFetchReplyReact(t *testing.T) {
	fixedNow(t)
	sleep = func(time.Duration) {}
	f := &fake{t: t, pending: 2}
	c := newTestClient(t, f)

	if _, err := c.Fetch(); !errors.Is(err, ErrSignedOut) {
		t.Fatalf("before login: %v", err)
	}
	var shown string
	if err := c.Login(func(s string) { shown = s }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shown, "ABC") {
		t.Errorf("login should show the code: %q", shown)
	}
	var saved token
	if !state.Load(tokenFile, &saved) || saved.Refresh != "rt" {
		t.Errorf("token should be cached: %+v", saved)
	}

	s, err := c.Fetch()
	if err != nil || len(s.Posts) != 2 || len(s.Waiting) != 5 || s.FetchedAt.IsZero() {
		t.Fatalf("fetch: %v %+v", err, s)
	}
	if err := c.Reply("1730650000000", "approved <3\nthanks"); err != nil {
		t.Fatal(err)
	}
	if got := f.bodies[len(f.bodies)-1]["body"].(map[string]any)["content"]; got != "approved &lt;3<br>thanks" {
		t.Errorf("reply content = %v", got)
	}
	if err := c.React("1730650000000", "👍"); err != nil {
		t.Fatal(err)
	}
	if got := f.bodies[len(f.bodies)-1]["reactionType"]; got != "👍" {
		t.Errorf("reaction = %v", got)
	}
	if err := c.Reply("bad", "x"); err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("rejected reply: %v", err)
	}
	want := []string{"POST /graph/teams/T/channels/C/messages/1730650000000/replies", "POST /graph/teams/T/channels/C/messages/1730650000000/setReaction"}
	for _, w := range want {
		if !slices.Contains(f.requests, w) {
			t.Errorf("missing %s in %v", w, f.requests)
		}
	}

	// An expired access token is refreshed with the cached refresh token, which is kept.
	c2, _ := New()
	c2.login, c2.graph = c.login, c.graph
	c2.tok.Expires = time.Now().Add(-time.Hour)
	if _, err := c2.Fetch(); err != nil || f.refresh != "rt" || c2.tok.Access != "at2" || c2.tok.Refresh != "rt" {
		t.Errorf("refresh: %v %q %+v", err, f.refresh, c2.tok)
	}

	// A revoked refresh token means signing in again.
	f.expired = true
	c2.tok.Expires = time.Now().Add(-time.Hour)
	if _, err := c2.Fetch(); !errors.Is(err, ErrSignedOut) {
		t.Errorf("revoked: %v", err)
	}
}

func TestLoginDenied(t *testing.T) {
	sleep = func(time.Duration) {}
	c := newTestClient(t, &fake{t: t})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/devicecode") {
			io.WriteString(w, `{"device_code":"dc","message":"m","interval":1,"expires_in":60}`)
			return
		}
		w.WriteHeader(400)
		io.WriteString(w, `{"error":"authorization_declined","error_description":"the user declined\r\nTrace ID: 1"}`)
	}))
	defer srv.Close()
	c.login = srv.URL
	err := c.Login(func(string) {})
	if err == nil || err.Error() != "Teams sign-in: authorization_declined: the user declined" {
		t.Errorf("declined: %v", err)
	}
}

func TestUnconfigured(t *testing.T) {
	siteConfig = func() config.Config { return config.Config{} }
	defer func() { siteConfig = config.Get }()
	if _, err := New(); !errors.Is(err, ErrUnconfigured) {
		t.Errorf("%v", err)
	}
}

var chatsJSON = `{"value":[
  {"id":"19:a@unq.gbl.spaces","chatType":"oneOnOne","webUrl":"https://teams/a","viewpoint":{"lastMessageReadDateTime":"2026-11-03T08:00:00Z"},
   "lastMessagePreview":{"id":"m1","createdDateTime":"2026-11-03T09:00:00Z","messageType":"message","from":{"user":{"id":"lukas","displayName":"Lukas Laczi"}},"body":{"content":"<p>got a sec?</p>"}}},
  {"id":"19:b@thread.v2","chatType":"group","topic":"Backend","viewpoint":{"lastMessageReadDateTime":"2026-11-03T10:00:00Z"},
   "lastMessagePreview":{"id":"m2","createdDateTime":"2026-11-03T09:30:00Z","messageType":"message","from":{"user":{"id":"pavel","displayName":"Pavel Porazil"}},"body":{"content":"deployed"}}},
  {"id":"19:c@thread.v2","chatType":"group",
   "lastMessagePreview":{"id":"m3","createdDateTime":"2026-11-03T09:40:00Z","messageType":"message","from":{"user":{"id":"filip","displayName":"Filip"}},"body":{"content":"hi all"}}},
  {"id":"19:d@unq.gbl.spaces","chatType":"oneOnOne",
   "lastMessagePreview":{"id":"m4","createdDateTime":"2026-11-03T09:00:00Z","messageType":"message","from":{"user":{"id":"me-id","displayName":"Martin Prazak"}},"body":{"content":"mine"}}},
  {"id":"19:e@thread.v2","chatType":"meeting",
   "lastMessagePreview":{"id":"m5","createdDateTime":"2026-11-03T09:00:00Z","messageType":"message","from":{"user":{"id":"x","displayName":"X"}},"body":{"content":"meeting chat"}}},
  {"id":"19:f@unq.gbl.spaces","chatType":"oneOnOne",
   "lastMessagePreview":{"id":"m6","createdDateTime":"2026-10-01T09:00:00Z","messageType":"message","from":{"user":{"id":"x","displayName":"X"}},"body":{"content":"too old"}}},
  {"id":"19:g@unq.gbl.spaces","chatType":"oneOnOne",
   "lastMessagePreview":{"id":"m7","createdDateTime":"2026-11-03T09:00:00Z","messageType":"systemEventMessage","from":null,"body":{"content":""}}},
  {"id":"19:h@unq.gbl.spaces","chatType":"oneOnOne"}
]}`

func fixedNow(t *testing.T) {
	old := now
	now = func() time.Time { return time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { now = old })
}

func TestWaitingChats(t *testing.T) {
	fixedNow(t)
	ws, err := waitingChats([]byte(chatsJSON), "me-id")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, w := range ws {
		got = append(got, fmt.Sprintf("%s|%s|%s|%s|%v", w.Ref, w.Where, w.From, w.Text, w.Unread))
	}
	want := []string{
		"chat/19:a@unq.gbl.spaces/m1|chat with Lukas Laczi|Lukas Laczi|got a sec?|true",
		"chat/19:b@thread.v2/m2|Backend|Pavel Porazil|deployed|false",
		"chat/19:c@thread.v2/m3|group chat|Filip|hi all|true",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s", strings.Join(got, "\n"))
	}
	if _, err := waitingChats([]byte(`nope`), "me"); err == nil {
		t.Error("garbage")
	}
}

const channelJSON = `{"value":[
  {"id":"r1","createdDateTime":"2026-11-03T08:00:00Z","webUrl":"https://teams/r1","from":{"user":{"id":"lukas","displayName":"Lukas Laczi"}},
   "body":{"content":"<at id=\"0\">Martin</at> can you look? https://gitlab.example.com/a/b/-/merge_requests/7"},
   "mentions":[{"id":0,"mentioned":{"user":{"id":"me-id","displayName":"Martin Prazak"}}}],
   "replies":[{"id":"r1a","createdDateTime":"2026-11-03T08:30:00Z","from":{"user":{"id":"pavel","displayName":"Pavel"}},"body":{"content":"+1"}}]},
  {"id":"r2","createdDateTime":"2026-11-03T07:00:00Z","from":{"user":{"id":"lukas","displayName":"Lukas Laczi"}},"body":{"content":"no mention"},
   "replies":[{"id":"r2a","createdDateTime":"2026-11-03T07:10:00Z","webUrl":"https://teams/r2a","from":{"user":{"id":"pavel","displayName":"Pavel"}},
     "body":{"content":"<at id=\"0\">Martin</at>?"},"mentions":[{"id":0,"mentioned":{"user":{"id":"me-id"}}}]}]},
  {"id":"r3","createdDateTime":"2026-11-03T06:00:00Z","from":{"user":{"id":"lukas","displayName":"Lukas"}},"body":{"content":"answered"},
   "mentions":[{"id":0,"mentioned":{"user":{"id":"me-id"}}}],
   "replies":[{"id":"r3a","createdDateTime":"2026-11-03T06:10:00Z","from":{"user":{"id":"me-id","displayName":"Martin Prazak"}},"body":{"content":"done"}}]},
  {"id":"r4","createdDateTime":"2026-10-01T06:00:00Z","from":{"user":{"id":"lukas","displayName":"Lukas"}},"body":{"content":"old"},
   "mentions":[{"id":0,"mentioned":{"user":{"id":"me-id"}}}]},
  {"id":"r5","createdDateTime":"2026-11-03T06:00:00Z","from":{"user":{"id":"me-id","displayName":"Martin Prazak"}},"body":{"content":"I mention myself"},
   "mentions":[{"id":0,"mentioned":{"user":{"id":"me-id"}}}]},
  {"id":"r6","createdDateTime":"2026-11-03T06:00:00Z","from":{"application":{"displayName":"Bot"}},"body":{"content":"@team"},
   "mentions":[{"id":0,"mentioned":{"conversation":{"id":"x"}}}]}
]}`

func TestUnansweredMentions(t *testing.T) {
	fixedNow(t)
	ch := Channel{Team: "Technology", TeamID: "T", Channel: "General", ChannelID: "C"}
	ws, err := unansweredMentions([]byte(channelJSON), ch, "me-id", "gitlab.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 2 {
		t.Fatalf("want the root mention and the reply mention, got %+v", ws)
	}
	a, b := ws[0], ws[1]
	if a.Ref != "T/C/r1" || a.Where != "Technology › General" || a.From != "Lukas Laczi" || a.WebURL != "https://teams/r1" ||
		len(a.MRURLs) != 1 || a.Kind != "mention" {
		t.Errorf("root mention: %+v", a)
	}
	if b.Ref != "T/C/r2" || b.From != "Pavel" || b.WebURL != "https://teams/r2a" || b.Text != "Martin?" {
		t.Errorf("a mention in a reply is answered in the root's thread: %+v", b)
	}
	if _, err := unansweredMentions([]byte(`x`), ch, "me", ""); err == nil {
		t.Error("garbage")
	}
}

func TestRefsShowAndChatReply(t *testing.T) {
	f := &fake{t: t}
	c := newTestClient(t, f)
	c.tok = token{Access: "at", Expires: time.Now().Add(time.Hour), Scopes: allScopes}

	if err := c.Reply("chat/19:a@unq.gbl.spaces/m1", "sure"); err != nil {
		t.Fatal(err)
	}
	if err := c.React("chat/19:a@unq.gbl.spaces/m1", "👍"); err != nil {
		t.Fatal(err)
	}
	if err := c.Reply("T/C/r1", "on it"); err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{
		"POST /graph/chats/19:a@unq.gbl.spaces/messages",
		"POST /graph/chats/19:a@unq.gbl.spaces/messages/m1/setReaction",
		"POST /graph/teams/T/channels/C/messages/r1/replies",
	} {
		if !slices.Contains(f.requests, w) {
			t.Errorf("missing %s in %v", w, f.requests)
		}
	}
	for _, bad := range []string{"", "a/b", "a/b/c/d"} {
		if err := c.Reply(bad, "x"); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
	c.cfg.TeamID = ""
	if err := c.Reply("r1", "x"); !errors.Is(err, ErrNoChannel) {
		t.Errorf("bare id without a review channel: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/chats/X/messages":
			io.WriteString(w, `{"value":[
			  {"id":"2","createdDateTime":"2026-11-03T09:01:00Z","messageType":"message","from":{"user":{"id":"me","displayName":"Martin"}},"body":{"content":"yes"}},
			  {"id":"1","createdDateTime":"2026-11-03T09:00:00Z","messageType":"message","from":{"user":{"id":"l","displayName":"Lukas"}},"body":{"content":"<p>sec?</p>"}},
			  {"id":"0","createdDateTime":"2026-11-03T08:00:00Z","messageType":"systemEventMessage","body":{"content":"added"}}]}`)
		case "/teams/T/channels/C/messages/r1":
			io.WriteString(w, `{"id":"r1","createdDateTime":"2026-11-03T08:00:00Z","from":{"user":{"displayName":"Lukas"}},"body":{"content":"root"}}`)
		case "/teams/T/channels/C/messages/r1/replies":
			io.WriteString(w, `{"value":[{"id":"x","createdDateTime":"2026-11-03T08:05:00Z","from":{"user":{"displayName":"Pavel"}},"body":{"content":"reply"}}]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c.graph = srv.URL
	lines, err := c.Show("chat/X/1")
	if err != nil || len(lines) != 2 || lines[0].Text != "sec?" || lines[1].From != "Martin" {
		t.Errorf("chat: %v %+v", err, lines)
	}
	lines, err = c.Show("T/C/r1")
	if err != nil || len(lines) != 2 || lines[0].Text != "root" || lines[1].Text != "reply" {
		t.Errorf("thread: %v %+v", err, lines)
	}
	for _, bad := range []string{"T/C/nope", "chat/nope/1", "x/y"} {
		if _, err := c.Show(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

func TestFetchWithoutReviewChannel(t *testing.T) {
	fixedNow(t)
	f := &fake{t: t}
	c := newTestClient(t, f)
	c.tok = token{Access: "at", Expires: time.Now().Add(time.Hour), Scopes: allScopes}
	c.cfg.TeamID, c.cfg.ChannelID = "", ""
	s, err := c.Fetch()
	if err != nil || len(s.Posts) != 0 || len(s.Waiting) != 5 {
		t.Fatalf("chats still come without the review channel: %v %+v", err, s.Waiting)
	}
	if _, err := c.FetchReview(); !errors.Is(err, ErrNoChannel) {
		t.Errorf("%v", err)
	}
	n := len(f.requests)
	if _, err := c.Fetch(); err != nil {
		t.Fatal(err)
	}
	for _, r := range f.requests[n:] {
		if strings.Contains(r, "joinedTeams") || strings.HasSuffix(r, "/graph/me") {
			t.Errorf("channels and me are cached, but %s was asked again", r)
		}
	}
}

// failing answers every path in fail with that status, and the rest like fake.
type failing struct {
	*fake
	fail map[string]int
	body string
}

func (f *failing) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if code, ok := f.fail[r.URL.Path]; ok {
		w.WriteHeader(code)
		io.WriteString(w, f.body)
		return
	}
	f.fake.ServeHTTP(w, r)
}

func failingClient(t *testing.T, fail map[string]int, body string) *Client {
	srv := httptest.NewServer(&failing{&fake{t: t}, fail, body})
	t.Cleanup(srv.Close)
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	c.login, c.graph = srv.URL+"/login", srv.URL+"/graph"
	c.tok = token{Access: "at", Expires: time.Now().Add(time.Hour), Scopes: allScopes}
	return c
}

func TestErrors(t *testing.T) {
	fixedNow(t)
	sleep = func(time.Duration) {}
	for _, p := range []string{"/graph/me", "/graph/me/chats", "/graph/me/joinedTeams", "/graph/teams/T/channels", "/graph/teams/T/channels/C/messages"} {
		c := failingClient(t, map[string]int{p: 500}, "")
		if _, err := c.Fetch(); err == nil && p != "/graph/teams/T/channels/C/messages" {
			t.Errorf("%s failing should fail the fetch", p)
		}
	}
	// A channel that can't be read is skipped when scanning for mentions.
	c := failingClient(t, map[string]int{}, "")
	c.cfg.TeamID = ""
	if s, err := c.Fetch(); err != nil || len(s.Waiting) != 5 {
		t.Errorf("unreadable channel: %v %+v", err, s.Waiting)
	}

	c = failingClient(t, map[string]int{"/graph/me/chats": 500}, strings.Repeat("x", 600))
	if _, err := c.fetchWaiting(); err == nil || len(err.Error()) > 560 {
		t.Errorf("long error bodies are cut: %d", len(err.Error()))
	}
	c = failingClient(t, map[string]int{}, "")
	c.graph = "http://bad host"
	if _, err := c.FetchReview(); err == nil {
		t.Error("a bad URL fails")
	}
	c.graph = "http://127.0.0.1:1"
	if _, err := c.FetchReview(); err == nil {
		t.Error("an unreachable Graph fails")
	}
	if err := c.React("a/b", "x"); err == nil {
		t.Error("bad ref")
	}
	c = failingClient(t, map[string]int{"/graph/me/chats": 200}, "not json")
	if _, err := c.fetchWaiting(); err == nil {
		t.Error("garbage chats")
	}
	c = failingClient(t, map[string]int{"/graph/teams/T/channels/C/messages/r1/replies": 500}, "")
	if _, err := c.Show("T/C/r1"); err == nil {
		t.Error("replies failing")
	}

	// Sign-in errors.
	c = failingClient(t, map[string]int{"/login/devicecode": 400}, `{"error":"invalid_client","error_description":"no such app"}`)
	if err := c.Login(func(string) {}); err == nil || !strings.Contains(err.Error(), "invalid_client") {
		t.Errorf("devicecode: %v", err)
	}
	c = failingClient(t, map[string]int{"/login/devicecode": 503}, "down")
	if err := c.Login(func(string) {}); err == nil || err.Error() != "Teams sign-in: 503 Service Unavailable" {
		t.Errorf("non-OAuth error: %v", err)
	}
	c.login = "http://127.0.0.1:1"
	if err := c.Login(func(string) {}); err == nil {
		t.Error("unreachable login")
	}
	var polls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/devicecode") {
			io.WriteString(w, `{"device_code":"dc","message":"m","interval":1,"expires_in":60}`)
			return
		}
		polls++
		w.WriteHeader(400)
		if polls == 1 {
			io.WriteString(w, `{"error":"slow_down"}`)
			return
		}
		io.WriteString(w, `{"error":"expired_token","error_description":"too late"}`)
	}))
	defer srv.Close()
	c.login = srv.URL
	if err := c.Login(func(string) {}); err == nil || !strings.Contains(err.Error(), "expired_token") || polls != 2 {
		t.Errorf("slow_down then expired: %v after %d polls", err, polls)
	}
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"device_code":"dc","message":"m","interval":1,"expires_in":0}`)
	}))
	defer srv2.Close()
	c.login = srv2.URL
	if err := c.Login(func(string) {}); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Errorf("a code that expires before anyone signs in: %v", err)
	}

	var nobody *identity
	if nobody.name() != "" || (&identity{}).name() != "" {
		t.Error("no sender has no name")
	}
}

func TestMentionWithoutLink(t *testing.T) {
	fixedNow(t)
	ws, _ := unansweredMentions([]byte(`{"value":[{"id":"r","createdDateTime":"2026-11-03T08:00:00Z","webUrl":"https://teams/root","from":{"user":{"id":"l","displayName":"L"}},
	  "body":{"content":"x"},"replies":[{"id":"a","createdDateTime":"2026-11-03T08:10:00Z","from":{"user":{"id":"p","displayName":"P"}},"body":{"content":"hey"},
	  "mentions":[{"mentioned":{"user":{"id":"me"}}}]}]}]}`), Channel{TeamID: "T", ChannelID: "C"}, "me", "gitlab.example.com")
	if len(ws) != 1 || ws[0].WebURL != "https://teams/root" {
		t.Errorf("a reply without its own link falls back to the thread's: %+v", ws)
	}
}

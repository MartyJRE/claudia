package teams

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const eventsJSON = `{"value":[
  {"id":"e2","subject":"Planning","start":{"dateTime":"2026-11-04T09:00:00.0000000","timeZone":"UTC"},"end":{"dateTime":"2026-11-04T10:00:00.0000000"},
   "organizer":{"emailAddress":{"name":"Vaclav"}},"responseStatus":{"response":"notResponded"},"webLink":"https://outlook/e2",
   "onlineMeeting":{"joinUrl":"https://teams/join"},"location":{"displayName":"Room 1"}},
  {"id":"e1","subject":"Standup","start":{"dateTime":"2026-11-03T13:00:00.0000000"},"end":{"dateTime":"2026-11-03T13:15:00.0000000"},
   "organizer":{"emailAddress":{"name":"Martin Prazak"}},"responseStatus":{"response":"organizer"}},
  {"id":"e0","subject":"Cancelled","isCancelled":true,"start":{"dateTime":"2026-11-03T14:00:00.0000000"},"end":{"dateTime":"2026-11-03T15:00:00.0000000"},
   "responseStatus":{"response":"notResponded"}},
  {"id":"s1","seriesMasterId":"S","subject":"Daily","start":{"dateTime":"2026-11-05T09:00:00.0000000"},"end":{"dateTime":"2026-11-05T09:15:00.0000000"},
   "organizer":{"emailAddress":{"name":"Vaclav"}},"responseStatus":{"response":"notResponded"}},
  {"id":"s2","seriesMasterId":"S","subject":"Daily","start":{"dateTime":"2026-11-06T09:00:00.0000000"},"end":{"dateTime":"2026-11-06T09:15:00.0000000"},
   "organizer":{"emailAddress":{"name":"Vaclav"}},"responseStatus":{"response":"notResponded"}},
  {"id":"e3","subject":"Already started","start":{"dateTime":"2026-11-03T11:00:00.0000000"},"end":{"dateTime":"2026-11-03T13:00:00.0000000"},
   "responseStatus":{"response":"notResponded"}}
]}`

func TestCalendar(t *testing.T) {
	fixedNow(t)
	evs, err := parseEvents([]byte(eventsJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 5 || evs[0].ID != "e3" || evs[1].ID != "e1" || evs[2].ID != "e2" || evs[3].Series != "S" {
		t.Fatalf("by start, cancelled left out: %+v", evs)
	}
	e := evs[2]
	if e.Subject != "Planning" || !e.Start.Equal(time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)) || e.Online != "https://teams/join" ||
		e.Organizer != "Vaclav" || e.Location != "Room 1" || e.Ref() != "event/e2" {
		t.Errorf("event: %+v", e)
	}
	inv := invites(evs)
	if len(inv) != 2 || inv[0].Ref != "event/e2" || inv[0].Kind != "invite" || inv[0].From != "Vaclav" || inv[0].Text != "Planning" {
		t.Errorf("only unanswered invites still ahead: %+v", inv)
	}
	if inv[1].Ref != "event/S" || !strings.HasSuffix(inv[1].Where, ", recurring") || !strings.HasPrefix(inv[1].Where, "from Thu 5 Nov") {
		t.Errorf("a recurring meeting is one invite, for its series: %+v", inv[1])
	}
	if _, err := parseEvents([]byte("x")); err == nil {
		t.Error("garbage")
	}

	f := &fake{t: t}
	c := newTestClient(t, f)
	c.tok = token{Access: "at", Expires: time.Now().Add(time.Hour)}
	s, err := c.Fetch()
	if err != nil || len(s.Events) != 5 {
		t.Fatalf("fetch reads the calendar: %v %+v", err, s.Events)
	}
	if last := s.Waiting[len(s.Waiting)-1]; last.Kind != "invite" {
		t.Errorf("waiting is sorted, the invite (tomorrow) last: %+v", s.Waiting)
	}
	c2 := failingClient(t, map[string]int{"/graph/me/calendarView": 500}, "")
	if _, err := c2.Fetch(); err == nil {
		t.Error("calendar failing fails the fetch")
	}
}

// graphSrv routes path → handler; unknown paths 404 and are recorded.
type graphSrv struct {
	t      *testing.T
	routes map[string]http.HandlerFunc
	log    []string
	bodies map[string]map[string]any
	raw    map[string][]byte
}

func (g *graphSrv) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.log = append(g.log, r.Method+" "+r.URL.Path)
	b, _ := io.ReadAll(r.Body)
	var v map[string]any
	if json.Unmarshal(b, &v) == nil {
		g.bodies[r.Method+" "+r.URL.Path] = v
	} else {
		g.raw[r.Method+" "+r.URL.Path+" "+r.Header.Get("Content-Range")] = b
	}
	if h, ok := g.routes[r.Method+" "+r.URL.Path]; ok {
		h(w, r)
		return
	}
	w.WriteHeader(404)
}

func reply(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, body) }
}

func graphClient(t *testing.T, routes map[string]http.HandlerFunc) (*Client, *graphSrv) {
	g := &graphSrv{t: t, routes: routes, bodies: map[string]map[string]any{}, raw: map[string][]byte{}}
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	c.graph, c.login = srv.URL, srv.URL
	c.tok = token{Access: "at", Expires: time.Now().Add(time.Hour)}
	c.me = me{ID: "me-id", DisplayName: "Martin Prazak"}
	return c, g
}

func TestRespond(t *testing.T) {
	c, g := graphClient(t, map[string]http.HandlerFunc{"POST /me/events/e2/tentativelyAccept": reply("")})
	if err := c.Respond("event/e2", "tentative", "running late"); err != nil {
		t.Fatal(err)
	}
	if b := g.bodies["POST /me/events/e2/tentativelyAccept"]; b["comment"] != "running late" || b["sendResponse"] != true {
		t.Errorf("body %+v", b)
	}
	for _, bad := range [][2]string{{"chat/x/1", "accept"}, {"event/", "accept"}, {"event/e2", "maybe"}, {"event/zz", "accept"}} {
		if err := c.Respond(bad[0], bad[1], ""); err == nil {
			t.Errorf("%v should fail", bad)
		}
	}
	c.graph = "http://127.0.0.1:1"
	if _, err := c.Calendar(time.Now(), time.Now()); err == nil {
		t.Error("unreachable")
	}
}

func TestPerson(t *testing.T) {
	var filters []string
	c, _ := graphClient(t, map[string]http.HandlerFunc{"GET /users": func(w http.ResponseWriter, r *http.Request) {
		f := r.URL.Query().Get("$filter")
		filters = append(filters, f)
		switch {
		case strings.Contains(f, "lukas@x.com"):
			io.WriteString(w, `{"value":[{"id":"l1","displayName":"Lukas Laczi","userPrincipalName":"lukas@x.com"}]}`)
		case strings.Contains(f, "'Lukas'"):
			io.WriteString(w, `{"value":[{"id":"l1","displayName":"Lukas Laczi","mail":"lukas@x.com"},{"id":"l2","displayName":"Lukas Flek","mail":"flek@x.com"}]}`)
		case strings.Contains(f, "'O''Brien'"):
			io.WriteString(w, `{"value":[{"id":"o","displayName":"Pat O'Brien","mail":"pat@x.com"}]}`)
		case strings.Contains(f, "'boom'"):
			w.WriteHeader(500)
		default:
			io.WriteString(w, `{"value":[]}`)
		}
	}})
	p, err := c.Person("lukas@x.com")
	if err != nil || p.ID != "l1" || p.Email != "lukas@x.com" || !strings.HasPrefix(filters[0], "mail eq") {
		t.Errorf("by email, UPN as the fallback address: %v %+v %q", err, p, filters[0])
	}
	if _, err := c.Person("Lukas"); err == nil || !strings.Contains(err.Error(), "Lukas Flek <flek@x.com>") {
		t.Errorf("ambiguous names list the candidates: %v", err)
	}
	if p, err := c.Person("O'Brien"); err != nil || p.Email != "pat@x.com" {
		t.Errorf("quotes are escaped: %v %+v", err, p)
	}
	if _, err := c.Person("nobody"); err == nil || !strings.Contains(err.Error(), "nobody called") {
		t.Errorf("no match: %v", err)
	}
	if _, err := c.Person("boom"); err == nil {
		t.Error("Graph failing")
	}
}

func TestChatAndPost(t *testing.T) {
	users := func(w http.ResponseWriter, r *http.Request) {
		id := strings.Split(r.URL.Query().Get("$filter"), "'")[1]
		if id == "ghost" {
			io.WriteString(w, `{"value":[]}`)
			return
		}
		io.WriteString(w, `{"value":[{"id":"`+id+`-id","displayName":"`+id+`"}]}`)
	}
	c, g := graphClient(t, map[string]http.HandlerFunc{
		"GET /users":                                  users,
		"POST /chats":                                 reply(`{"id":"19:new"}`),
		"POST /chats/19:new/messages":                 reply(`{"id":"m9"}`),
		"GET /me/joinedTeams":                         reply(`{"value":[{"id":"T","displayName":"Technology"}]}`),
		"GET /teams/T/channels":                       reply(`{"value":[{"id":"C","displayName":"General"}]}`),
		"POST /teams/T/channels/C/messages":           reply(`{"id":"p1"}`),
		"POST /teams/T/channels/C/messages/x/replies": reply(`{"id":"r1"}`),
	})
	ref, err := c.Chat([]string{"lukas"}, "hi\nthere")
	if err != nil || ref != "chat/19:new/m9" {
		t.Fatalf("chat: %v %q", err, ref)
	}
	chat := g.bodies["POST /chats"]
	members := chat["members"].([]any)
	if chat["chatType"] != "oneOnOne" || len(members) != 2 || members[0].(map[string]any)["user@odata.bind"] != "https://graph.microsoft.com/v1.0/users('me-id')" ||
		members[1].(map[string]any)["user@odata.bind"] != "https://graph.microsoft.com/v1.0/users('lukas-id')" {
		t.Errorf("chat body %+v", chat)
	}
	if b := g.bodies["POST /chats/19:new/messages"]["body"].(map[string]any)["content"]; b != "hi<br>there" {
		t.Errorf("message %v", b)
	}
	c.Chat([]string{"lukas", "pavel"}, "all")
	if g.bodies["POST /chats"]["chatType"] != "group" {
		t.Error("several people make a group chat")
	}
	for _, who := range [][]string{nil, {"ghost"}} {
		if _, err := c.Chat(who, "x"); err == nil {
			t.Errorf("%v should fail", who)
		}
	}

	for _, name := range []string{"technology/general", "T/C"} {
		if ref, err := c.Post(name, "news"); err != nil || ref != "T/C/p1" {
			t.Errorf("post to %s: %v %q", name, err, ref)
		}
	}
	for _, bad := range []string{"General", "Technology/Random"} {
		if _, err := c.Post(bad, "x"); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
	if err := c.Reply("T/C/x", "ok"); err != nil {
		t.Error(err)
	}

	c2, _ := graphClient(t, map[string]http.HandlerFunc{})
	if _, err := c2.Post("a/b", "x"); err == nil {
		t.Error("channels failing")
	}
	c2.me = me{}
	if _, err := c2.Chat([]string{"x"}, "x"); err == nil {
		t.Error("who am I failing")
	}
	c3, _ := graphClient(t, map[string]http.HandlerFunc{"GET /users": users})
	if _, err := c3.Chat([]string{"x"}, "x"); err == nil {
		t.Error("creating the chat failing")
	}
}

func TestFiles(t *testing.T) {
	dir := t.TempDir()
	small := filepath.Join(dir, "notes.txt")
	os.WriteFile(small, []byte("hello"), 0o644)
	big := filepath.Join(dir, "big log.bin")
	old := uploadChunk
	uploadChunk = 2 << 20
	t.Cleanup(func() { uploadChunk = old })
	os.WriteFile(big, make([]byte, 5<<20), 0o644)

	var session string
	c, g := graphClient(t, map[string]http.HandlerFunc{
		"PUT /me/drive/root:/Microsoft Teams Chat Files/notes.txt:/content": reply(`{"eTag":"\"{6C1C6B5E-AAAA-4BBB-8CCC-000000000001},1\"","name":"notes.txt","webUrl":"https://sp/notes.txt"}`),
		"GET /teams/T/channels/C/filesFolder":                               reply(`{"id":"F","parentReference":{"driveId":"D"}}`),
		"POST /drives/D/items/F:/big log.bin:/createUploadSession": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"uploadUrl":"`+session+`"}`)
		},
		"POST /me/drive/root:/Microsoft Teams Chat Files/big log.bin:/createUploadSession": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"uploadUrl":"`+session+`"}`)
		},
		"PUT /upload": func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "" {
				t.Error("upload sessions must go without the token")
			}
			io.WriteString(w, `{"eTag":"\"{ABC},2\"","name":"big log.bin","webUrl":"https://sp/big"}`)
		},
		"POST /chats/19:a/messages":                                             reply(`{"id":"m1"}`),
		"POST /teams/T/channels/C/messages/r/replies":                           reply(`{"id":"r2"}`),
		"GET /shares/" + "u!aHR0cHM6Ly9zcC9ub3Rlcy50eHQ" + "/driveItem":         reply(`{"name":"notes.txt"}`),
		"GET /shares/" + "u!aHR0cHM6Ly9zcC9ub3Rlcy50eHQ" + "/driveItem/content": reply("hello"),
		"GET /shares/u!aHR0cHM6Ly9zcC9ldmls/driveItem":                          reply(`{"name":"../evil"}`),
	})
	session = c.graph + "/upload"

	if err := c.Reply("chat/19:a/m0", "see", small); err != nil {
		t.Fatal(err)
	}
	msg := g.bodies["POST /chats/19:a/messages"]
	att := msg["attachments"].([]any)[0].(map[string]any)
	if att["id"] != "6c1c6b5e-aaaa-4bbb-8ccc-000000000001" || att["contentType"] != "reference" || att["contentUrl"] != "https://sp/notes.txt" || att["name"] != "notes.txt" {
		t.Errorf("attachment %+v", att)
	}
	if b := msg["body"].(map[string]any)["content"]; b != `see<attachment id="6c1c6b5e-aaaa-4bbb-8ccc-000000000001"></attachment>` {
		t.Errorf("body %v", b)
	}

	if err := c.Reply("T/C/r", "log", big); err != nil {
		t.Fatal(err)
	}
	if len(g.raw["PUT /upload bytes 0-2097151/5242880"]) != 2<<20 || len(g.raw["PUT /upload bytes 4194304-5242879/5242880"]) != 1<<20 {
		t.Errorf("chunks: %v", keys(g.raw))
	}
	if b := g.bodies["POST /teams/T/channels/C/messages/r/replies"]["body"].(map[string]any)["content"]; b != `log<attachment id="abc"></attachment>` {
		t.Errorf("body %v", b)
	}

	if err := c.Reply("chat/19:a/m0", "x", filepath.Join(dir, "missing")); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("a missing file: %v", err)
	}
	if err := c.Reply("T/Z/r", "x", small); err == nil {
		t.Error("no files folder")
	}
	if err := c.Reply("chat/19:zz/m0", "x"); err == nil {
		t.Error("send failing")
	}
	if err := c.Reply("chat/19:a/m0", "x", filepath.Join(dir, "big log.bin")); err != nil {
		t.Error(err)
	}
	g.routes["PUT /upload"] = func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }
	if err := c.Reply("T/C/r", "x", big); err == nil {
		t.Error("chunk failing")
	}
	delete(g.routes, "POST /drives/D/items/F:/big log.bin:/createUploadSession")
	if err := c.Reply("T/C/r", "x", big); err == nil {
		t.Error("session failing")
	}
	other := filepath.Join(dir, "other.txt")
	os.WriteFile(other, []byte("x"), 0o644)
	if err := c.Reply("chat/19:a/m0", "x", other); err == nil {
		t.Error("simple upload failing")
	}

	out := t.TempDir()
	p, err := c.Download("https://sp/notes.txt", out)
	if b, _ := os.ReadFile(p); err != nil || string(b) != "hello" || filepath.Base(p) != "notes.txt" {
		t.Errorf("download: %v %q", err, p)
	}
	if _, err := c.Download("https://sp/evil", out); err == nil {
		t.Error("names that escape the folder are refused")
	}
	if _, err := c.Download("https://sp/none", out); err == nil {
		t.Error("unknown share")
	}
	g.routes["GET /shares/u!aHR0cHM6Ly9zcC9ub3Rlcy50eHQ/driveItem/content"] = func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }
	if _, err := c.Download("https://sp/notes.txt", out); err == nil {
		t.Error("content failing")
	}

	got := files([]attachment{{ContentType: "reference", Name: "a.pdf", ContentURL: "u"}, {ContentType: "application/vnd.microsoft.card.adaptive"}})
	if len(got) != 1 || got[0].Name != "a.pdf" {
		t.Errorf("only file references are files: %+v", got)
	}
}

func keys[V any](m map[string]V) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestReactionsAnswer(t *testing.T) {
	fixedNow(t)
	body := `{"value":[
	  {"id":"r1","createdDateTime":"2026-11-03T08:00:00Z","from":{"user":{"id":"l","displayName":"L"}},"body":{"content":"review pls"},
	   "mentions":[{"mentioned":{"user":{"id":"me"}}}],"reactions":[{"reactionType":"✅","user":{"user":{"id":"me","displayName":null}}}]},
	  {"id":"r2","createdDateTime":"2026-11-03T08:00:00Z","from":{"user":{"id":"l","displayName":"L"}},"body":{"content":"root"},
	   "reactions":[{"reactionType":"like","user":{"user":{"id":"me"}}},{"reactionType":"like","user":{"user":{"id":"me"}}},{"reactionType":"like","user":{"application":{"id":"bot"}}}],
	   "replies":[{"id":"a","createdDateTime":"2026-11-03T08:10:00Z","from":{"user":{"id":"p","displayName":"P"}},"body":{"content":"you?"},"mentions":[{"mentioned":{"user":{"id":"me"}}}]}]},
	  {"id":"r3","createdDateTime":"2026-11-03T08:00:00Z","from":{"user":{"id":"l","displayName":"L"}},"body":{"content":"someone else reacted"},
	   "mentions":[{"mentioned":{"user":{"id":"me"}}}],"reactions":[{"reactionType":"✅","user":{"user":{"id":"p"}}}]}
	]}`
	ws, err := unansweredMentions([]byte(body), Channel{TeamID: "T", ChannelID: "C"}, "me", "gitlab.example.com")
	if err != nil || len(ws) != 1 || ws[0].Ref != "T/C/r3" {
		t.Errorf("my reaction on the mention or on the thread's post answers it, someone else's doesn't: %v %+v", err, ws)
	}
	posts, _ := Parse([]byte(body), "gitlab.example.com")
	if !posts[0].Reacted("me") || posts[2].Reacted("me") || posts[0].Reacted("") || len(posts[1].ReactedBy) != 1 {
		t.Errorf("reactors, once each, bots left out: %+v", posts)
	}
}

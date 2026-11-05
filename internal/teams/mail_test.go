package teams

import (
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/config"
)

const mailJSON = `{"value": [
  {"id":"m1","subject":"Can you look at this?","from":{"emailAddress":{"name":"Lukas Laczi","address":"lukas@luther.cz"}},
   "toRecipients":[{"emailAddress":{"address":"Martin.Prazak@luther.cz"}}],"receivedDateTime":"2026-11-03T09:00:00Z",
   "bodyPreview":"Hi Martin,\r\n the deploy  failed","webLink":"https://outlook/m1","inferenceClassification":"focused"},
  {"id":"m2","subject":"Weekly digest","from":{"emailAddress":{"name":"Newsletter","address":"news@vendor.com"}},
   "toRecipients":[{"emailAddress":{"address":"martin.prazak@luther.cz"}}],"receivedDateTime":"2026-11-03T08:00:00Z","inferenceClassification":"other"},
  {"id":"m3","subject":"Team update","from":{"emailAddress":{"name":"Boss","address":"boss@luther.cz"}},
   "toRecipients":[{"emailAddress":{"address":"all@luther.cz"}}],"receivedDateTime":"2026-11-03T07:00:00Z","inferenceClassification":"focused"},
  {"id":"m4","subject":"Approval needed: budget","from":{"emailAddress":{"address":"erp@luther.cz"}},
   "toRecipients":[{"emailAddress":{"address":"finance@luther.cz"}}],"receivedDateTime":"2026-11-03T06:00:00Z","inferenceClassification":"other"},
  {"id":"m5","from":{"emailAddress":{"name":"Eva","address":"eva@luther.cz"}},
   "toRecipients":[{"emailAddress":{"address":"m.prazak@luther.cz"}}],"receivedDateTime":"2026-11-03T05:00:00Z","inferenceClassification":"focused"},
  {"id":"m6","subject":"Undeliverable: budget","toRecipients":[{"emailAddress":{"address":""}}],"receivedDateTime":"2026-11-03T04:00:00Z","inferenceClassification":"focused"}
]}`

func refs(ws []Waiting) string {
	var out []string
	for _, w := range ws {
		out = append(out, w.Ref)
	}
	return strings.Join(out, ",")
}

func TestParseMail(t *testing.T) {
	off := false
	on := true
	mine := []string{"martin.prazak@luther.cz", "m.prazak@luther.cz", ""}
	for _, tc := range []struct {
		name string
		cfg  config.Mail
		want string
	}{
		{"direct and focused only", config.Mail{}, "mail/m1,mail/m5"},
		{"direct switched on", config.Mail{DirectRaw: &on}, "mail/m1,mail/m5"},
		{"direct switched off", config.Mail{DirectRaw: &off}, ""},
		{"a rule on the sender's name", config.Mail{DirectRaw: &off, Rules: []config.MailRule{{From: "lukas laczi"}}}, "mail/m1"},
		{"a sender rule skips mail without a sender", config.Mail{DirectRaw: &off, Rules: []config.MailRule{{From: "luther"}}}, "mail/m1,mail/m3,mail/m4,mail/m5"},
		{"a subject rule still takes it", config.Mail{DirectRaw: &off, Rules: []config.MailRule{{Subject: "BUDGET"}}}, "mail/m4,mail/m6"},
		{"any rule is enough", config.Mail{DirectRaw: &off, Rules: []config.MailRule{{From: "nobody"}, {Subject: "digest"}}}, "mail/m2"},
		{"a rule on the sender, sent to a list", config.Mail{Rules: []config.MailRule{{From: "BOSS@"}}}, "mail/m1,mail/m3,mail/m5"},
		{"a rule on the subject, filed as other", config.Mail{DirectRaw: &off, Rules: []config.MailRule{{Subject: "approval needed"}}}, "mail/m4"},
		{"every field of a rule must match", config.Mail{DirectRaw: &off, Rules: []config.MailRule{{From: "boss", Subject: "budget"}}}, ""},
		{"an empty rule matches nothing", config.Mail{DirectRaw: &off, Rules: []config.MailRule{{}}}, ""},
	} {
		ws, err := parseMail([]byte(mailJSON), mine, tc.cfg)
		if err != nil {
			t.Fatal(err)
		}
		if got := refs(ws); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
	ws, _ := parseMail([]byte(mailJSON), mine, config.Mail{})
	w := ws[0]
	if w.Kind != "mail" || w.Where != "Can you look at this?" || w.From != "Lukas Laczi" || w.Text != "Hi Martin, the deploy failed" || w.WebURL != "https://outlook/m1" {
		t.Errorf("waiting: %+v", w)
	}
	if ws[1].Where != "(no subject)" || ws[1].From != "Eva" {
		t.Errorf("no subject: %+v", ws[1])
	}
	ws, _ = parseMail([]byte(mailJSON), nil, config.Mail{Rules: []config.MailRule{{Subject: "budget"}}})
	if len(ws) != 2 || ws[0].From != "erp@luther.cz" || ws[1].From != "" || !ws[0].At.Equal(time.Date(2026, 11, 3, 6, 0, 0, 0, time.UTC)) {
		t.Errorf("the address stands in for a missing name, nothing for a missing sender: %+v", ws)
	}
	if ws, err := parseMail([]byte(`{"value":[]}`), mine, config.Mail{}); err != nil || ws != nil {
		t.Errorf("no mail: %v %+v", err, ws)
	}
	if _, err := parseMail([]byte("x"), mine, config.Mail{}); err == nil {
		t.Error("garbage")
	}
}

func withMail(t *testing.T, m *config.Mail) {
	siteConfig = func() config.Config {
		c := config.Get()
		c.Teams.Mail = m
		return c
	}
	t.Cleanup(func() { siteConfig = config.Get })
}

const allScopes = graphURL + "Chat.ReadWrite " + graphURL + "Mail.Read " + graphURL + "Chat.Create " + graphURL + "User.ReadBasic.All " + graphURL + "Files.ReadWrite.All"

func TestGranted(t *testing.T) {
	fixedNow(t)
	withMail(t, &config.Mail{})
	refreshed := 0
	c, g := graphClient(t, map[string]http.HandlerFunc{
		"POST /token": func(w http.ResponseWriter, r *http.Request) {
			refreshed++
			io.WriteString(w, `{"access_token":"at2","refresh_token":"rt2","expires_in":3600,"scope":"openid `+graphURL+`Chat.ReadWrite"}`)
		},
		"POST /chats/19:a/messages": reply(`{"id":"m1"}`),
	})
	c.tok = token{Access: "old", Refresh: "rt", Expires: time.Now().Add(time.Hour)} // saved before scopes were kept
	if !c.granted("Chat.ReadWrite") || c.granted("Mail.Read") || refreshed != 1 {
		t.Fatalf("an old token is refreshed once to learn its scopes: %d %+v", refreshed, c.tok)
	}
	c.granted("Chat.ReadWrite")
	if refreshed != 1 {
		t.Error("refreshed again")
	}
	for name, err := range map[string]error{
		"who":      func() error { _, err := c.Person("lukas"); return err }(),
		"chat":     func() error { _, err := c.Chat([]string{"lukas"}, "hi"); return err }(),
		"download": func() error { _, err := c.Download("https://sp/a", t.TempDir()); return err }(),
		"file":     c.Reply("chat/19:a/m0", "x", "notes.txt"),
	} {
		if !errors.Is(err, ErrNotGranted) {
			t.Errorf("%s without its permission: %v", name, err)
		}
	}
	if err := c.Reply("chat/19:a/m0", "plain"); err != nil {
		t.Errorf("replies still work: %v", err)
	}
	if slices.ContainsFunc(g.log, func(l string) bool { return strings.Contains(l, "/users") || strings.Contains(l, "mailFolders") }) {
		t.Errorf("nothing ungranted was asked: %v", g.log)
	}
	f := failingClient(t, map[string]int{"/graph/me/mailFolders/inbox/messages": 403}, "")
	f.tok.Scopes = graphURL + "Chat.ReadWrite"
	if s, err := f.Fetch(); err != nil || s.MailErr != "" {
		t.Errorf("mail without Mail.Read is skipped quietly: %v %q", err, s.MailErr)
	}
}

func TestFetchAndShowMail(t *testing.T) {
	fixedNow(t)
	c, g := graphClient(t, map[string]http.HandlerFunc{
		"GET /me/mailFolders/inbox/messages": func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			if f := q.Get("$filter"); f != "isRead eq false and receivedDateTime ge 2026-10-20T12:00:00Z" {
				t.Errorf("filter: %s", f)
			}
			if q.Get("$top") != "50" || q.Get("$select") != "id,subject,from,toRecipients,receivedDateTime,bodyPreview,webLink,inferenceClassification" {
				t.Errorf("query: %v", q)
			}
			w.Write([]byte(mailJSON))
		},
		"GET /me/messages/m1": reply(`{"conversationId":"c'1"}`),
		"GET /me/messages": func(w http.ResponseWriter, r *http.Request) {
			if f := r.URL.Query().Get("$filter"); f != "conversationId eq 'c''1'" || r.Header.Get("Prefer") != `outlook.body-content-type="text"` {
				t.Errorf("conversation query: %s %s", f, r.Header.Get("Prefer"))
			}
			w.Write([]byte(`{"value":[
			  {"from":{"emailAddress":{"name":"Martin Prazak"}},"receivedDateTime":"2026-11-03T10:00:00Z","uniqueBody":{"content":" looking \r\n"}},
			  {"from":{"emailAddress":{"name":"Lukas Laczi"}},"receivedDateTime":"2026-11-03T09:00:00Z","uniqueBody":{"content":"the deploy failed"}}]}`))
		},
	})
	c.me.UPN = "martin.prazak@luther.cz"
	ws, err := c.fetchMail(config.Mail{})
	if err != nil || refs(ws) != "mail/m1" {
		t.Fatalf("my address comes from userPrincipalName when mail is empty: %v %s", err, refs(ws))
	}
	lines, err := c.Show("mail/m1")
	if err != nil || len(lines) != 2 || lines[0].From != "Lukas Laczi" || lines[1].Text != "looking" {
		t.Fatalf("show: %v %+v (%v)", err, lines, g.log)
	}
	if _, err := c.Show("mail/nope"); err == nil {
		t.Error("an unknown mail fails")
	}
	g.routes["GET /me/messages"] = reply("not json")
	if _, err := c.Show("mail/m1"); err == nil {
		t.Error("a garbled conversation fails")
	}
	delete(g.routes, "GET /me/messages")
	if _, err := c.Show("mail/m1"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("the conversation failing: %v", err)
	}
	g.routes["GET /me/mailFolders/inbox/messages"] = reply("not json")
	if _, err := c.fetchMail(config.Mail{}); err == nil {
		t.Error("garbled mail fails")
	}
	c.me = me{}
	g.log = nil
	if _, err := c.fetchMail(config.Mail{}); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("who am I failing: %v", err)
	}
	if strings.Join(g.log, ",") != "GET /me" {
		t.Errorf("mail isn't read without knowing whose: %v", g.log)
	}
}

func TestFetchMergesMail(t *testing.T) {
	fixedNow(t)
	withMail(t, &config.Mail{Rules: []config.MailRule{{Subject: "budget"}}})
	c := newTestClient(t, &fake{t: t})
	c.tok = token{Access: "at", Expires: time.Now().Add(time.Hour), Scopes: allScopes}
	s, err := c.Fetch()
	if err != nil || s.MailErr != "" {
		t.Fatalf("%v %q", err, s.MailErr)
	}
	var mail []string
	for _, w := range s.Waiting {
		if w.Kind == "mail" {
			mail = append(mail, w.Ref)
		}
	}
	if strings.Join(mail, ",") != "mail/m6,mail/m4" {
		t.Errorf("mail, oldest first: %v", mail)
	}
	if !slices.IsSortedFunc(s.Waiting, func(a, b Waiting) int { return a.At.Compare(b.At) }) || len(s.Waiting) == len(mail) {
		t.Errorf("mail sorts in among the rest: %+v", s.Waiting)
	}
}

func TestGrantedScopeForms(t *testing.T) {
	for _, tc := range []struct {
		scopes string
		want   bool
	}{
		{graphURL + "Mail.Read", true},
		{"openid Mail.Read profile", true},
		{graphURL + "mail.read", true},
		{"MAIL.READ", true},
		{graphURL + "Mail.ReadWrite " + graphURL + "Mail.ReadBasic", false},
		{"https://outlook.office.com/Mail.Read", false},
		{graphURL + ".default", false},
	} {
		c, _ := graphClient(t, nil)
		c.tok.Scopes = tc.scopes
		if got := c.granted("Mail.Read"); got != tc.want {
			t.Errorf("%q: granted %v, want %v", tc.scopes, got, tc.want)
		}
	}
}

func TestGrantedWithoutScopes(t *testing.T) {
	refreshes := 0
	c, _ := graphClient(t, map[string]http.HandlerFunc{"POST /token": func(w http.ResponseWriter, r *http.Request) {
		refreshes++
		w.WriteHeader(500)
	}})
	c.tok = token{Access: "old", Refresh: "rt", Expires: time.Now().Add(time.Hour)}
	if c.granted("Chat.ReadWrite") || refreshes != 1 {
		t.Errorf("a failed refresh grants nothing: %d", refreshes)
	}
	if err := c.need("Chat.ReadWrite"); !errors.Is(err, ErrNotGranted) || err.Error() != "claudia hasn't been granted this permission yet: Chat.ReadWrite (an admin grants it on the claudia app, then run `claudia teams login`)" {
		t.Errorf("need: %v", err)
	}
	c, _ = graphClient(t, map[string]http.HandlerFunc{"POST /token": func(w http.ResponseWriter, r *http.Request) { t.Error("nothing to refresh with") }})
	c.tok = token{Access: "at", Expires: time.Now().Add(time.Hour)}
	if c.granted("Chat.ReadWrite") {
		t.Error("a token without scopes or refresh token grants nothing")
	}
	if c.tok.Access != "at" {
		t.Error("and is kept")
	}
	c.tok.Scopes = graphURL + "Chat.Create"
	if _, err := c.Chat([]string{"lukas"}, "hi"); !errors.Is(err, ErrNotGranted) || !strings.Contains(err.Error(), ": User.ReadBasic.All (") {
		t.Errorf("chat names the permission still missing: %v", err)
	}
}

func TestFetchKeepsTeamsWhenMailFails(t *testing.T) {
	fixedNow(t)
	withMail(t, &config.Mail{})
	c := failingClient(t, map[string]int{"/graph/me/mailFolders/inbox/messages": 403}, `{"error":{"code":"ErrorAccessDenied"}}`)
	s, err := c.Fetch()
	if err != nil || len(s.Waiting) == 0 {
		t.Fatalf("Teams still comes: %v %+v", err, s.Waiting)
	}
	if !strings.Contains(s.MailErr, "403") {
		t.Errorf("mail error kept: %q", s.MailErr)
	}
}

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
   "toRecipients":[{"emailAddress":{"address":"m.prazak@luther.cz"}}],"receivedDateTime":"2026-11-03T05:00:00Z","inferenceClassification":"focused"}
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
	mine := []string{"martin.prazak@luther.cz", "m.prazak@luther.cz"}
	for _, tc := range []struct {
		name string
		cfg  config.Mail
		want string
	}{
		{"direct and focused only", config.Mail{}, "mail/m1,mail/m5"},
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
			if f := r.URL.Query().Get("$filter"); f != "isRead eq false and receivedDateTime ge 2026-10-20T12:00:00Z" {
				t.Errorf("filter: %s", f)
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

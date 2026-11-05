package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/teams"
)

type fakeTeams struct {
	snap     teams.Snapshot
	fetchErr error
	did      []string
}

func (f *fakeTeams) Login(show func(string)) error {
	show("enter ABC")
	return f.fetchErr
}
func (f *fakeTeams) Channels() ([]teams.Channel, error) {
	return []teams.Channel{{Team: "Technology", Channel: "Code Review Requests"}}, f.fetchErr
}
func (f *fakeTeams) Fetch() (teams.Snapshot, error)     { return f.snap, f.fetchErr }
func (f *fakeTeams) FetchReview() ([]teams.Post, error) { return f.snap.Posts, f.fetchErr }
func (f *fakeTeams) Show(ref string) ([]teams.Line, error) {
	if ref == "bad" {
		return nil, errors.New("not a ref")
	}
	return []teams.Line{{From: "Lukas", At: time.Unix(0, 0), Text: "got a sec?", Files: []teams.File{{Name: "a.pdf", URL: "https://sp/a.pdf"}}}}, nil
}
func (f *fakeTeams) Reply(id, text string, files ...string) error {
	f.did = append(f.did, strings.TrimSpace("reply "+id+" "+text+" "+strings.Join(files, " ")))
	return nil
}
func (f *fakeTeams) Chat(who []string, text string, files ...string) (string, error) {
	f.did = append(f.did, strings.TrimSpace("chat "+strings.Join(who, "+")+" "+text+" "+strings.Join(files, " ")))
	return "chat/c/1", f.fetchErr
}
func (f *fakeTeams) Post(ch, text string, files ...string) (string, error) {
	f.did = append(f.did, "post "+ch+" "+text)
	return "T/C/1", f.fetchErr
}
func (f *fakeTeams) Person(who string) (teams.Person, error) {
	return teams.Person{Name: "Lukas Laczi", Email: "lukas@x.com"}, f.fetchErr
}
func (f *fakeTeams) Download(link, dir string) (string, error) {
	f.did = append(f.did, "download "+link+" "+dir)
	return dir + "/a.pdf", f.fetchErr
}
func (f *fakeTeams) Calendar(from, to time.Time) ([]teams.Event, error) {
	f.did = append(f.did, fmt.Sprintf("calendar %s", to.Sub(from)))
	return []teams.Event{{ID: "e1", Subject: "Standup", Start: time.Date(2026, 11, 3, 9, 0, 0, 0, time.Local), End: time.Date(2026, 11, 3, 9, 15, 0, 0, time.Local), Organizer: "Vaclav", Response: "accepted"}}, f.fetchErr
}
func (f *fakeTeams) Respond(ref, answer, comment string) error {
	f.did = append(f.did, strings.TrimSpace("respond "+ref+" "+answer+" "+comment))
	return nil
}
func (f *fakeTeams) React(id, emoji string) error {
	f.did = append(f.did, "react "+id+" "+emoji)
	return nil
}

const mrURL = "https://gitlab.example.com/a/b/-/merge_requests/1"

func withTeams(t *testing.T) *fakeTeams {
	f := &fakeTeams{snap: teams.Snapshot{Posts: []teams.Post{
		{ID: "old", At: time.Unix(1, 0), MRURLs: []string{mrURL}},
		{ID: "new", At: time.Unix(2, 0), MRURLs: []string{mrURL}},
		{ID: "other", At: time.Unix(3, 0)},
	}}}
	swap(t, &newTeams, func() (teamsClient, error) { return f, nil })
	return f
}

var teamsDefault = newTeams

func TestTeamsCmd(t *testing.T) {
	if r := claudia(t, "teams"); !strings.Contains(r.out, "login") {
		t.Errorf("help: %q", r.out)
	}
	f := withTeams(t)
	if r := claudia(t, "teams", "login"); !strings.Contains(r.out, "enter ABC") || !strings.Contains(r.out, "signed in") {
		t.Errorf("login: %q", r.out)
	}
	if r := claudia(t, "teams", "channels"); !strings.Contains(r.out, "Code Review Requests") {
		t.Errorf("channels: %q", r.out)
	}
	if r := claudia(t, "teams", "posts", "--mr", mrURL); !strings.Contains(r.out, `"new"`) || strings.Contains(r.out, `"other"`) {
		t.Errorf("posts --mr: %q", r.out)
	}
	if r := claudia(t, "teams", "posts", "--mr", "https://nope"); strings.TrimSpace(r.out) != "[]" {
		t.Errorf("no posts should print []: %q", r.out)
	}
	if r := claudia(t, "teams", "waiting"); strings.TrimSpace(r.out) != "[]" {
		t.Errorf("nothing waiting prints []: %q", r.out)
	}
	f.snap.Waiting = []teams.Waiting{{Ref: "chat/x/1", From: "Lukas"}}
	if r := claudia(t, "teams", "waiting"); !strings.Contains(r.out, `"chat/x/1"`) {
		t.Errorf("waiting: %q", r.out)
	}
	if r := claudia(t, "teams", "show", "chat/x/1"); !strings.Contains(r.out, "Lukas: got a sec?") || !strings.Contains(r.out, "📎 a.pdf  https://sp/a.pdf") {
		t.Errorf("show: %q", r.out)
	}
	claudia(t, "teams", "reply", mrURL, "looks", "good")
	claudia(t, "teams", "react", "other", "✅")
	if got := strings.Join(f.did, "|"); got != "reply new looks good|react other ✅" {
		t.Errorf("did %q", got)
	}
	f.did = nil
	swap(t, &clock, func() time.Time { return time.Date(2026, 11, 3, 8, 0, 0, 0, time.Local) })
	outs := map[string]string{}
	for _, args := range [][]string{
		{"reply", "chat/c/1", "--file", "a.pdf", "here", "--file", "b.png", "--image", "d.png"},
		{"chat", "lukas,pavel", "hi", "both", "--file", "x.txt"},
		{"post", "Technology/General", "news"},
		{"who", "Lukas", "Laczi"},
		{"download", "https://sp/a.pdf"},
		{"download", "https://sp/a.pdf", "/tmp/d"},
		{"calendar"},
		{"calendar", "7"},
		{"respond", "event/e1", "decline", "out", "sick"},
	} {
		r := claudia(t, append([]string{"teams"}, args...)...)
		if r.code != -1 {
			t.Errorf("%v failed: %+v", args, r)
		}
		outs[args[0]] += r.out
	}
	want := []string{
		"reply chat/c/1 here a.pdf b.png " + teams.Image("d.png"), "chat lukas+pavel hi both x.txt", "post Technology/General news",
		"download https://sp/a.pdf .", "download https://sp/a.pdf /tmp/d", "calendar 24h0m0s", "calendar 168h0m0s", "respond event/e1 decline out sick",
	}
	if got := strings.Join(f.did, "|"); got != strings.Join(want, "|") {
		t.Errorf("did\n%s\nwant\n%s", got, strings.Join(want, "|"))
	}
	if !strings.Contains(outs["chat"], "chat/c/1") || !strings.Contains(outs["post"], "T/C/1") || !strings.Contains(outs["who"], "lukas@x.com") ||
		!strings.Contains(outs["download"], "/tmp/d/a.pdf") || !strings.Contains(outs["calendar"], "Tue 3 Nov 09:00–09:15  Standup  (Vaclav, accepted)  event/e1") {
		t.Errorf("outputs %+v", outs)
	}
	for _, args := range [][]string{{"reply", "x"}, {"bogus"}, {"reply", "https://nope", "hi"}, {"show"}, {"show", "bad"},
		{"chat", "x"}, {"who"}, {"download"}, {"calendar", "0"}, {"calendar", "x"}, {"respond", "event/e1"}, {"reply", "x", "y", "--file"}, {"reply", "x", "y", "--image"}} {
		if r := claudia(t, append([]string{"teams"}, args...)...); r.code != 1 {
			t.Errorf("%v should fail, got %+v", args, r)
		}
	}
	f.fetchErr = errors.New("boom")
	for _, args := range [][]string{{"posts"}, {"waiting"}, {"login"}, {"channels"}, {"chat", "x", "y"}, {"post", "a/b", "y"}, {"who", "x"}, {"download", "u"}, {"calendar"}} {
		if r := claudia(t, append([]string{"teams"}, args...)...); r.code != 1 || !strings.Contains(r.err, "boom") {
			t.Errorf("%v: error: %+v", args, r)
		}
	}
	if r := claudia(t, "teams", "reply", mrURL, "x"); r.code != 1 {
		t.Errorf("reply by MR URL needs the review posts: %+v", r)
	}
	if _, err := teamsDefault(); !errors.Is(err, teams.ErrUnconfigured) {
		t.Errorf("the real client needs teams in config.json: %v", err)
	}
	swap(t, &newTeams, teamsDefault)
	if r := claudia(t, "teams", "posts"); r.code != 1 || !strings.Contains(r.err, "isn't set up") {
		t.Errorf("unconfigured: %+v", r)
	}
}

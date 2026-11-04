package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/repos"
	"github.com/MartyJRE/claudia/internal/state"
	"github.com/MartyJRE/claudia/internal/teams"
)

func TestTeamsPostsOnReviewRows(t *testing.T) {
	m := newModelShell(t)
	mr := m.gl.Review[0]
	other := m.gl.Review[1]

	m.switchView(vReview)
	if c := m.teamsChip(mr); c != "" {
		t.Errorf("no post, no chip: %q", c)
	}
	if pressShell(m, "T"); !strings.Contains(m.flash, "no Teams post") {
		t.Errorf("T without a post: %q", m.flash)
	}

	m.teams = teams.Snapshot{Posts: []teams.Post{
		{ID: "p1", From: "Lukas Laczi", At: time.Now().Add(-time.Hour), WebURL: "https://teams/p1", MRURLs: []string{mr.WebURL, other.WebURL},
			Replies: []teams.Reply{{From: "Lukas Laczi", Text: "ping"}}},
		{ID: "p0", From: "Lukas Laczi", At: time.Now().Add(-48 * time.Hour), MRURLs: []string{mr.WebURL}},
	}}
	if got := plainShell(m.teamsChip(mr)); got != "teams" {
		t.Errorf("chip = %q", got)
	}
	s := plainShell(m.View())
	if !strings.Contains(s, "teams") {
		t.Errorf("Review row should show the teams chip:\n%s", s)
	}
	if d := plainShell(strings.Join(m.teamsLines(mr), "\n")); !strings.Contains(d, "Lukas Laczi · 1h ago · 1 replies") || !strings.Contains(d, "Lukas Laczi: ping") {
		t.Errorf("detail = %q", d)
	}
	m.cur[vReview].pos = 0
	if cmd := pressShell(m, "T"); cmd == nil {
		t.Error("T should open the post")
	}

	note := m.teamsReplyNote([]string{mr.WebURL, other.WebURL})
	if strings.Count(note, "message p1") != 1 || strings.Contains(note, "p0") {
		t.Errorf("one line per post, newest post only:\n%s", note)
	}
	if !strings.Contains(note, mr.WebURL+", "+other.WebURL) || !strings.Contains(note, "claudia teams reply") {
		t.Errorf("note should list the post's MRs and the command:\n%s", note)
	}
	if m.teamsReplyNote([]string{"https://elsewhere"}) != "" {
		t.Error("no post, no note")
	}

	c := getConfig()
	c.Teams.Me = "Martin Prazak"
	fake(t, &getConfig, func() config.Config { return c })
	m.teams.Posts[0].Replies = append(m.teams.Posts[0].Replies, teams.Reply{From: "Martin Prazak", Text: "on it"})
	if got := plainShell(m.teamsChip(mr)); got != "teams ✓" {
		t.Errorf("after I replied: %q", got)
	}
}

func TestTeamsWaitingInInbox(t *testing.T) {
	m := newModelShell(t)
	m.repos = []repos.Repo{{Dir: "/code/ops"}}
	started := fakeStart(t, "ops:1", nil)
	m.teams.Waiting = []teams.Waiting{
		{Ref: "chat/19:a/m1", Kind: "chat", Where: "chat with Lukas Laczi", From: "Lukas Laczi", At: time.Now().Add(-time.Hour), Text: "got a sec?\nreally", WebURL: "https://teams/a", Unread: true},
		{Ref: "T/C/r1", Kind: "mention", Where: "Technology › General", From: "Pavel", At: time.Now().Add(-2 * time.Hour), Text: "thoughts?"},
		{Ref: "T/C/r9", Kind: "mention", Where: "Technology › Code Review", From: "Lukas", At: time.Now(), Text: "review pls", MRURLs: []string{m.gl.Review[0].WebURL}},
	}
	var got []inboxItem
	for _, it := range m.inbox() {
		if it.kind == kTeams {
			got = append(got, it)
		}
	}
	if len(got) != 2 {
		t.Fatalf("a mention about an MR I'm reviewing stays on its review row: %+v", got)
	}
	if got[0].id != "tm:T/C/r1" || got[1].text != "● got a sec?\nreally" || got[1].url != "https://teams/a" {
		t.Errorf("items, oldest first, unread marked: %+v", got)
	}
	m.refilter()
	if s := plainShell(m.View()); !strings.Contains(s, "teams") || !strings.Contains(s, "Technology › General") {
		t.Errorf("inbox should list Teams items:\n%s", s)
	}
	if plainShell(kindStyle(kTeams).Render("x")) != "x" {
		t.Error("style")
	}

	m.claudeForInbox(got[1])
	pressShell(m, "enter")
	if len(*started) != 1 {
		t.Fatalf("a reply session starts: %+v", *started)
	}
	p := promptOf((*started)[0])
	if (*started)[0].Name != "reply to Lukas" || !strings.HasPrefix(p, "Lukas Laczi wrote to me in Teams (chat with Lukas Laczi):\n\n> got a sec?\n> really") {
		t.Errorf("prompt: %q", p)
	}
	if !strings.Contains(p, "claudia teams show chat/19:a/m1") || !strings.Contains(p, "claudia teams reply chat/19:a/m1") || !strings.Contains(p, "Only after I OK") {
		t.Errorf("prompt should say how to read and reply, after an OK: %q", p)
	}

	m.replyInTeams(got[0].tw)
	pressShell(m, "enter")
	if p := promptOf((*started)[len(*started)-1]); len(*started) != 2 || !strings.HasPrefix(p, "Pavel mentioned me in Teams, in Technology › General:") {
		t.Errorf("mention prompt: %q", p)
	}
}

func TestTeamsSourceShell(t *testing.T) {
	m := newModelShell(t)
	at := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	state.Save(cacheFile[srcTeams], teams.Snapshot{Waiting: []teams.Waiting{{Ref: "chat/a/1"}}, FetchedAt: at})
	m.loadCaches()
	if len(m.teams.Waiting) != 1 || !m.fetchedAt[srcTeams].Equal(at) {
		t.Errorf("the cached Teams snapshot shows right away: %+v", m.teams)
	}

	m.tm = &teams.Client{}
	if !m.stale(srcTeams) {
		t.Error("set up and old: fetch")
	}
	snap := teams.Snapshot{Posts: []teams.Post{{ID: "p"}}, FetchedAt: at}
	var err error
	fake(t, &fetchTeams, func(*teams.Client) (teams.Snapshot, error) { return snap, err })
	f := m.fetch(srcTeams)().(fetched)
	m.applyFetched(f)
	if f.err != nil || len(m.teams.Posts) != 1 {
		t.Errorf("fetched: %+v", f)
	}
	err = teams.ErrNoChannel
	if f := m.fetch(srcTeams)().(fetched); f.err != nil {
		t.Errorf("no review channel isn't an error, chats still come: %v", f.err)
	}
	err = errors.New("Teams GET: 401")
	if f := m.fetch(srcTeams)().(fetched); f.err == nil {
		t.Error("other errors are shown")
	}
}

func TestTeamsCtlAndKeysShell(t *testing.T) {
	m := newModelShell(t)
	mr := m.gl.Review[0]
	m.teams.Posts = []teams.Post{
		{ID: "p3", At: time.Now(), MRURLs: []string{mr.WebURL}, WebURL: "https://teams/p3"},
		{ID: "p2", At: time.Now().Add(-time.Hour), MRURLs: []string{mr.WebURL}},
		{ID: "p1", At: time.Now().Add(-2 * time.Hour), MRURLs: []string{mr.WebURL}},
	}
	m.links.AddMRs("aaaaaaaa-wait", []string{mr.WebURL})
	m.reindex()
	h := m.CtlHere(m.sessions[0])
	if len(h.MRs) == 0 || strings.Join(h.MRs[0].TeamsPosts, ",") != "p3,p2,p1" {
		t.Errorf("ctl here lists the MR's Teams posts, newest first: %+v", h.MRs)
	}
	if n := strings.Count(plainShell(strings.Join(m.teamsLines(mr), "\n")), "teams "); n != 2 {
		t.Errorf("the detail shows the two newest posts, got %d", n)
	}

	m.switchView(vInbox)
	m.refilter()
	for i, it := range m.vis.inbox {
		if it.kind == kReview && it.mr.WebURL == mr.WebURL {
			m.cur[vInbox].pos = i
		}
	}
	if cmd := pressShell(m, "T"); cmd == nil {
		t.Error("T on a review in the inbox opens its Teams post")
	}
	for i, it := range m.vis.inbox {
		if it.kind == kClaude {
			m.cur[vInbox].pos = i
		}
	}
	if cmd := pressShell(m, "T"); cmd != nil {
		t.Error("T does nothing on a Claude session")
	}
}

func TestTeamsInvitesShell(t *testing.T) {
	m := newModelShell(t)
	m.teams.FetchedAt = time.Now()
	m.teams.Waiting = []teams.Waiting{{Ref: "event/e2", Kind: "invite", Where: "Wed 4 Nov 10:00", From: "Vaclav", Text: "Planning", WebURL: "https://outlook/e2"}}
	var inv inboxItem
	for _, it := range m.inbox() {
		if it.kind == kInvite {
			inv = it
		}
	}
	if inv.id != "tm:event/e2" || !inv.since.Equal(m.teams.FetchedAt) || plainShell(kindStyle(kInvite).Render("x")) != "x" {
		t.Fatalf("invite item: %+v", inv)
	}

	var got []string
	var err error
	fake(t, &respondTeams, func(_ *teams.Client, ref, answer, comment string) error {
		got = append(got, ref+" "+answer)
		return err
	})
	m.claudeForInbox(inv)
	if m.chooser == nil || len(m.chooser.all) != 3 {
		t.Fatalf("c on an invite asks for the answer")
	}
	cmd := pressShell(m, "down", "enter")
	if msg := cmd(); msg != (flashMsg{"Teams isn't set up", true}) {
		t.Errorf("not set up: %v", msg)
	}
	m.tm = &teams.Client{}
	m.answerInvite(inv.tw)
	if msg := pressShell(m, "enter")(); msg != refreshMsg(srcTeams) || len(got) != 1 || got[0] != "event/e2 accept" {
		t.Errorf("accepted: %v %v", msg, got)
	}
	err = errors.New("nope")
	m.answerInvite(inv.tw)
	if msg := pressShell(m, "enter")(); msg != (flashMsg{"nope", true}) {
		t.Errorf("error: %v", msg)
	}
}

func TestTeamsMailShell(t *testing.T) {
	m := newModelShell(t)
	m.repos = []repos.Repo{{Dir: "/code/ops"}}
	started := fakeStart(t, "ops:1", nil)
	at := time.Now().Add(-time.Hour)
	m.teams.Waiting = []teams.Waiting{{Ref: "mail/m1", Kind: "mail", Where: "Deploy failed", From: "Lukas Laczi", At: at, Text: "Hi Martin", WebURL: "https://outlook/m1"}}
	var mail []inboxItem
	for _, it := range m.inbox() {
		if it.kind == kMail {
			mail = append(mail, it)
		}
	}
	if len(mail) != 1 || mail[0].id != "tm:mail/m1" || !mail[0].since.Equal(at) || mail[0].url != "https://outlook/m1" || plainShell(kindStyle(kMail).Render("x")) != "x" {
		t.Fatalf("mail item: %+v", mail)
	}
	m.refilter()
	if s := plainShell(m.View()); !strings.Contains(s, "mail") || !strings.Contains(s, "Deploy failed") {
		t.Errorf("inbox should list mail:\n%s", s)
	}

	m.claudeForInbox(mail[0])
	pressShell(m, "enter")
	if len(*started) != 1 {
		t.Fatalf("a reply session starts: %+v", *started)
	}
	if p := promptOf((*started)[0]); !strings.HasPrefix(p, `Lukas Laczi emailed me: "Deploy failed"`) ||
		!strings.Contains(p, "claudia teams show mail/m1") || !strings.Contains(p, "paste into Outlook (https://outlook/m1)") {
		t.Errorf("prompt: %q", p)
	}

	m.applyFetched(fetched{src: srcTeams, data: teams.Snapshot{MailErr: "Teams GET: 403"}})
	if !strings.Contains(m.flash, "Outlook mail: Teams GET: 403") || m.errs[srcTeams] != nil {
		t.Errorf("a mail error flashes once, Teams itself is fine: %q %v", m.flash, m.errs[srcTeams])
	}
	m.flash = ""
	m.applyFetched(fetched{src: srcTeams, data: teams.Snapshot{MailErr: "Teams GET: 403"}})
	if m.flash != "" {
		t.Errorf("the same error doesn't flash again: %q", m.flash)
	}
}

func TestBriefMeetingsShell(t *testing.T) {
	m := newModelShell(t)
	at := func(h, min int) time.Time { return time.Date(2026, 11, 3, h, min, 0, 0, time.Local) }
	fake(t, &clockNow, func() time.Time { return at(12, 0) })
	m.teams.Events = []teams.Event{
		{Subject: "Morning", Start: at(9, 0), End: at(9, 30), Response: "accepted"},
		{Subject: "Now", Start: at(11, 30), End: at(12, 30), Response: "accepted"},
		{Subject: "Later", Start: at(15, 0), End: at(16, 0), Response: "tentativelyAccept"},
		{Subject: "Declined", Start: at(16, 0), End: at(17, 0), Response: "declined"},
		{Subject: "Tomorrow", Start: at(33, 0), End: at(34, 0), Response: "accepted"},
	}
	b, _ := json.Marshal(m.BriefData())
	s := string(b)
	if !strings.Contains(s, `"Meetings":[{"Subject":"Now","Start":"11:30","End":"12:30","Response":"accepted"},{"Subject":"Later"`) ||
		strings.Contains(s, "Morning") || strings.Contains(s, "Declined") || strings.Contains(s, "Tomorrow") {
		t.Errorf("the rest of today, declined left out: %s", s)
	}
}

func TestMentionsCoveredByMRsShell(t *testing.T) {
	m := newModelShell(t)
	m.gl.Me.Username = "martin"
	rev := m.gl.Review[0]
	tracked := "https://gitlab.example.com/g/x/-/merge_requests/1"
	m.ix.mrs[tracked] = gitlab.MR{WebURL: tracked, State: "merged"}
	unknown := "https://gitlab.example.com/g/payments/-/merge_requests/77"
	m.teams.Waiting = []teams.Waiting{
		{Ref: "T/C/review", Kind: "mention", MRURLs: []string{rev.WebURL}},
		{Ref: "T/C/merged", Kind: "mention", MRURLs: []string{tracked}},
		{Ref: "T/C/unknown", Kind: "mention", MRURLs: []string{tracked, unknown}},
		{Ref: "T/C/plain", Kind: "mention"},
	}
	refs := func() []string {
		var out []string
		for _, it := range m.inbox() {
			if it.kind == kTeams {
				out = append(out, it.tw.Ref)
			}
		}
		return out
	}
	if got := strings.Join(refs(), ","); got != "T/C/unknown,T/C/plain" {
		t.Errorf("on a review row or all MRs done: hidden; not looked up yet: shown. got %s", got)
	}

	var asked []string
	fake(t, &fetchMR, func(project string, iid int) (gitlab.MR, error) {
		asked = append(asked, fmt.Sprintf("%s!%d", project, iid))
		if iid == 78 {
			return gitlab.MR{}, errors.New("404")
		}
		return gitlab.MR{State: "opened", ApprovedBy: []string{"martin"}}, nil
	})
	m.teams.Waiting = append(m.teams.Waiting, teams.Waiting{Ref: "T/C/gone", Kind: "mention", MRURLs: []string{"https://gitlab.example.com/g/payments/-/merge_requests/78", "https://elsewhere/x"}})
	msg := m.lookupPostMRs()()
	m.Update(msg)
	if strings.Join(asked, ",") != "g/payments!77,g/payments!78" {
		t.Errorf("only untracked MRs are looked up, each once: %v", asked)
	}
	if got := strings.Join(refs(), ","); got != "T/C/plain,T/C/gone" {
		t.Errorf("an MR I approved clears its mention: %s", got)
	}
	var saved map[string]bool
	if !state.Load(postMRsFile, &saved) || !saved[unknown] {
		t.Errorf("looked-up MRs are remembered: %v", saved)
	}
	m.teams.Waiting = m.teams.Waiting[:3]
	if m.lookupPostMRs() != nil {
		t.Error("done MRs aren't asked about again")
	}
	m.postMRs = nil
	m.loadCaches()
	if done, known := m.mrDone(unknown); !done || !known {
		t.Error("and survive a restart")
	}

	// A review I've approved still in my list doesn't hold the mention on its row.
	m.gl.Review[0].ApprovedBy = []string{"martin"}
	m.reindex()
	m.teams.Waiting = []teams.Waiting{{Ref: "T/C/review", Kind: "mention", MRURLs: []string{rev.WebURL}}}
	if len(refs()) != 0 {
		t.Error("approved: done")
	}

	// My reaction ticks the review chip.
	m.teams.MeID = "me-id"
	m.teams.Posts = []teams.Post{{ID: "p", MRURLs: []string{rev.WebURL}, ReactedBy: []string{"me-id"}}}
	if plainShell(m.teamsChip(rev)) != "teams ✓" {
		t.Error("reacted: ✓")
	}

	// The lookup runs after a Teams fetch.
	m.teams.Waiting = []teams.Waiting{{Ref: "T/C/new", Kind: "mention", MRURLs: []string{"https://gitlab.example.com/g/payments/-/merge_requests/79"}}}
	snap := m.teams
	if _, cmd := m.Update(fetched{src: srcTeams, data: snap}); cmd == nil {
		t.Error("a Teams fetch looks up its new MRs")
	}
}

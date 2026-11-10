package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/atlassian"
)

func mentionsModelViews(t *testing.T) *Model {
	m := jiraModelViews(t)
	at := func(d time.Duration, x atlassian.Mention) atlassian.Mention {
		x.Created = time.Now().Add(-d)
		return x
	}
	m.jira.Mentions = []atlassian.Mention{
		at(time.Hour, jiraMentionViews("c1", "ABC-2", false)),
		at(30*time.Minute, jiraMentionViews("c2", "ABC-3", true)),
		at(10*time.Minute, jiraMentionViews("c3", "ABC-4", false)),
	}
	m.jira.Mentions[0].Text = strings.Repeat("word ", 60) + "end"
	m.conf.Mentions = []atlassian.Page{confMentionViews()}
	m.dismissed = dismissed{"jira:c3": time.Now()}
	m.view = vMentions
	m.reindex()
	return m
}

func mentionIDsViews(m *Model) string {
	var ids []string
	for _, x := range m.vis.mentions {
		ids = append(ids, x.id)
	}
	return strings.Join(ids, " ")
}

func TestMentionsList(t *testing.T) {
	m := mentionsModelViews(t)
	confID := "conf:42:" + m.conf.Mentions[0].Modified.Format(time.RFC3339)
	if got, want := mentionIDsViews(m), "jira:c1 "+confID; got != want {
		t.Fatalf("open mentions, newest first:\n got %s\nwant %s", got, want)
	}
	if n := m.unansweredMentions(); n != 1 {
		t.Errorf("only open Jira mentions count as unanswered, got %d", n)
	}
	if m.unansweredFor("ABC-2") != 1 || m.unansweredFor("ABC-3") != 0 || m.unansweredFor("ABC-4") != 0 {
		t.Error("unansweredFor")
	}
	s := screenViews(m)
	for _, want := range []string{"7 Mentions 1", "Bob Builder", "about ABC-2", "Martin please review & sign…", "OPS", "Jira and Confluence mentions"} {
		if !strings.Contains(s, want) {
			t.Errorf("screen lacks %q:\n%s", want, s)
		}
	}

	pressViews(m, "a")
	if got, want := mentionIDsViews(m), "jira:c1 "+confID+" jira:c3 jira:c2"; got != want {
		t.Errorf("show handled puts open ones first:\n got %s\nwant %s", got, want)
	}
	pressViews(m, "a")
	if m.showAll {
		t.Error("a toggles back")
	}

	m.filter.SetValue("runbook")
	m.refilter()
	if got := mentionIDsViews(m); got != confID {
		t.Errorf("filter: %s", got)
	}
}

func TestMentionsDetailStates(t *testing.T) {
	m := mentionsModelViews(t)
	m.showAll = true
	m.refilter()
	cases := []struct {
		pos  int
		want string
	}{
		{0, "waiting for your reply"},
		{1, "page mention"},
		{2, "dismissed"},
		{3, "you replied after this"},
	}
	for _, c := range cases {
		m.cur[vMentions].pos = c.pos
		if s := plainViews(m.detailMention()); !strings.Contains(s, c.want) {
			t.Errorf("row %d lacks %q:\n%s", c.pos, c.want, s)
		}
	}
	m.cur[vMentions].pos = 0
	s := plainViews(m.detailMention())
	if !strings.Contains(s, "ABC-2 about ABC-2  ·  To Do") || !strings.Contains(s, "Bob Builder, ") {
		t.Errorf("jira detail head:\n%s", s)
	}
	if !strings.Contains(s, "word word") || !strings.Contains(s, "end") || strings.Count(s, "word") != 60 {
		t.Errorf("long text wraps over several lines without losing words:\n%s", s)
	}
	m.cur[vMentions].pos = 1
	if s := plainViews(m.detailMention()); !strings.Contains(s, "Confluence, ") {
		t.Errorf("a page mention is by Confluence:\n%s", s)
	}
}

func TestMentionsDismiss(t *testing.T) {
	m := mentionsModelViews(t)
	pressViews(m, "x")
	if strings.Contains(mentionIDsViews(m), "jira:c1") {
		t.Error("x hides the mention")
	}
	if _, ok := loadDismissed()["jira:c1"]; !ok {
		t.Error("dismissal is saved")
	}
	if m.unansweredMentions() != 0 {
		t.Error("dismissed mentions aren't unanswered")
	}
	pressViews(m, "a")
	if got := mentionIDsViews(m); !strings.HasSuffix(got, "jira:c3 jira:c2 jira:c1") {
		t.Fatalf("a dismissed mention sorts with the handled ones: %s", got)
	}
	m.cur[vMentions].pos = 3
	pressViews(m, "x")
	if _, ok := loadDismissed()["jira:c1"]; ok {
		t.Error("x on a dismissed mention brings it back")
	}
}

func TestMentionsKeys(t *testing.T) {
	m := mentionsModelViews(t)
	pressViews(m, "J")
	if m.view != vJira {
		t.Fatal("J on a Jira mention goes to the ticket")
	}
	if is, _ := m.currentIssue(); is.Key != "ABC-2" {
		t.Errorf("on %s", is.Key)
	}

	m.switchView(vMentions)
	m.cur[vMentions].pos = 1
	pressViews(m, "J")
	if m.view != vMentions {
		t.Error("J on a page mention stays")
	}
	pressViews(m, "c")
	if m.chooser == nil || m.chooser.title != "reply on Runbook in" {
		t.Errorf("c on a page picks a repo for the reply: %+v", m.chooser)
	}
	pressViews(m, "esc")
	m.cur[vMentions].pos = 0
	pressViews(m, "c")
	if m.chooser == nil || m.chooser.title != "reply to Bob Builder in" {
		t.Errorf("c on a comment picks a repo for the reply: %+v", m.chooser)
	}
	pressViews(m, "esc")
	for _, k := range []string{"o", "y"} {
		if _, cmd := m.Update(keyViews(k)); cmd == nil {
			t.Errorf("%s returns a command", k)
		}
	}

	m.vis.mentions, m.vis.mentionRows = nil, nil
	if _, cmd := m.Update(keyViews("o")); cmd != nil {
		t.Error("nothing selected")
	}
}

func TestMentionsEmpty(t *testing.T) {
	m := newModelViews(t)
	m.view = vMentions
	m.refilter()
	if !strings.Contains(screenViews(m), "inbox zero") {
		t.Error("empty mentions")
	}
	m.atl, m.atlErr = nil, errors.New("set ATLASSIAN_EMAIL")
	if !strings.Contains(screenViews(m), "set ATLASSIAN_EMAIL") {
		t.Error("client error")
	}
}

func TestCleanExcerpt(t *testing.T) {
	cases := map[string]string{
		"@@@hl@@@Ann@@@endhl@@@ said":     "Ann said",
		"a &amp; b &quot;c&quot;&hellip;": `a & b "c"…`,
		"  line\n  two\t three ":          "line two three",
	}
	for in, want := range cases {
		if got := cleanExcerpt(in); got != want {
			t.Errorf("cleanExcerpt(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestConfluenceView(t *testing.T) {
	m := newModelViews(t)
	m.view = vConfluence
	m.refilter()
	if !strings.Contains(screenViews(m), "asking Confluence") {
		t.Error("before the first fetch")
	}
	m.conf = atlassian.ConfluenceSnapshot{
		Mine: []atlassian.Page{
			{Title: "Runbook", Space: "OPS", URL: atlBaseViews + "/wiki/x", Excerpt: "steps &amp; checks", Modified: time.Now().Add(-3 * time.Hour)},
			{Title: "Roadmap", Space: "PM", URL: atlBaseViews + "/wiki/y", Modified: time.Now().Add(-50 * time.Hour)},
		},
		FetchedAt: time.Now(),
	}
	m.refilter()
	s := screenViews(m)
	for _, want := range []string{"OPS", "Runbook", "3h", "PM", "Roadmap", "2d", "pages you edited recently", atlBaseViews + "/wiki/x", "steps & checks"} {
		if !strings.Contains(s, want) {
			t.Errorf("screen lacks %q:\n%s", want, s)
		}
	}
	m.filter.SetValue("pm")
	m.refilter()
	if len(m.vis.pages) != 1 || m.vis.pages[0].Title != "Roadmap" {
		t.Errorf("filter matches the space: %+v", m.vis.pages)
	}
	for _, k := range []string{"enter", "y"} {
		if _, cmd := m.Update(keyViews(k)); cmd == nil {
			t.Errorf("%s returns a command", k)
		}
	}
	if _, cmd := m.Update(keyViews("x")); cmd != nil {
		t.Error("unbound key")
	}

	m.filter.SetValue("nothing matches")
	m.refilter()
	if _, cmd := m.Update(keyViews("enter")); cmd != nil {
		t.Error("nothing selected")
	}
	if s := screenViews(m); !strings.Contains(s, "no pages") {
		t.Errorf("empty list:\n%s", s)
	}
	m.atl, m.atlErr = nil, errors.New("no client")
	if !strings.Contains(screenViews(m), "no client") {
		t.Error("client error")
	}
}

package ui

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/state"
)

func TestCtlInboxShell(t *testing.T) {
	m := newModelShell(t)
	m.links.AddMRs("bbbbbbbb-busy", []string{m.gl.Review[0].WebURL})
	m.reindex()
	for id := range m.dismissed {
		delete(m.dismissed, id)
	}
	items := m.CtlInbox("aaaaaaaa-wait")
	if len(items) != 4 {
		t.Fatalf("all inbox items, got %d", len(items))
	}
	byKind := map[string]CtlItem{}
	for _, it := range items {
		byKind[it.Kind] = it
	}
	for kind, want := range map[string]bool{"claude": true, "mention": true, "review": true, "threads": false} {
		if got := byKind[kind].ThisSession; got != want {
			t.Errorf("%s: this_session %v, want %v (same session, or same ticket ABC-1)", kind, got, want)
		}
	}
	busy := m.CtlInbox("bbbbbbbb-busy")
	for _, it := range busy {
		// The linked review MR is off this session's branch, so its ticket ABC-1 wins over the branch's ABC-2.
		want := it.Kind == "review" || it.Kind == "mention"
		if it.ThisSession != want {
			t.Errorf("busy session, %s: %v, want %v", it.Kind, it.ThisSession, want)
		}
	}
	for _, it := range m.CtlInbox("") {
		if it.ThisSession {
			t.Errorf("no session, nothing is this session's")
		}
	}
	if byKind["review"].URL != m.gl.Review[0].WebURL || byKind["review"].From != "alice" || byKind["review"].Since.IsZero() {
		t.Errorf("review item fields: %+v", byKind["review"])
	}
}

func TestCtlHereShell(t *testing.T) {
	m := newModelShell(t)
	m.links.AddMRs("aaaaaaaa-wait", []string{m.gl.Review[0].WebURL, "https://elsewhere/mr/1"})
	m.reindex()
	h := m.CtlHere(m.sessions[0])
	if h.Session != "aaaaaaaa-wait" || h.Name != "export csv" || h.Dir != "/code/payments" {
		t.Errorf("identity: %+v", h)
	}
	if h.Ticket == nil || *h.Ticket != (CtlTicket{Key: "ABC-1", Title: "Payments export", Status: "Dev in progress"}) {
		t.Errorf("ticket: %+v", h.Ticket)
	}
	if len(h.MRs) != 2 || !reflect.DeepEqual(h.MRs[0], CtlMR{URL: m.gl.Review[0].WebURL, Title: "feat(ABC-1): add payments export", State: "opened", Pipeline: "success"}) || !reflect.DeepEqual(h.MRs[1], CtlMR{URL: "https://elsewhere/mr/1"}) {
		t.Errorf("MRs: %+v", h.MRs)
	}
	if len(h.Items) != 3 {
		t.Errorf("only items for this session: %+v", h.Items)
	}

	closed := m.CtlHere(m.sessions[2])
	if closed.Ticket != nil || closed.MRs != nil || closed.Items != nil {
		t.Errorf("a session with nothing linked: %+v", closed)
	}
	b, _ := json.Marshal(closed)
	if string(b) != `{"session":"cccccccc-idle","name":"ledger chat","dir":"/code/ledger","inbox_items":null}` {
		t.Errorf("JSON: %s", b)
	}

	m.jira.Sprint[0].Category = "done"
	m.reindex()
	if h := m.CtlHere(m.sessions[0]); !h.Ticket.Done {
		t.Errorf("done ticket")
	}
}

func TestSnoozeShell(t *testing.T) {
	m := newModelShell(t)
	ids := []string{"rev:a", "thr:b"}
	if n := m.Snooze(ids); n != 2 {
		t.Errorf("snoozed %d", n)
	}
	if n := m.Snooze(append(ids, "cc:c")); n != 1 {
		t.Errorf("already snoozed ones don't count: %d", n)
	}
	var onDisk dismissed
	if !state.Load(dismissedFile, &onDisk) || len(onDisk) != 3 {
		t.Errorf("snoozes are saved: %v", onDisk)
	}

	tui := newModelShell(t)
	if tui.reloadDismissed() {
		t.Errorf("no file yet, nothing to reload")
	}
	ctl := &Model{dismissed: dismissed{}}
	ctl.Snooze([]string{m.inbox()[0].id})
	if !tui.reloadDismissed() || len(tui.dismissed) != 1 {
		t.Fatalf("the TUI picks up a snooze from ctl")
	}
	if tui.reloadDismissed() {
		t.Errorf("unchanged file isn't reloaded")
	}
	future := time.Now().Add(time.Hour)
	os.Chtimes(state.Path(dismissedFile), future, future)
	if !tui.reloadDismissed() {
		t.Errorf("a newer file is reloaded")
	}
}

func TestCurrentSessionShell(t *testing.T) {
	m := newModelShell(t)
	if _, ok := m.CurrentSession(); ok {
		t.Errorf("outside tmux there is no current session")
	}
	t.Setenv("TMUX_PANE", "%1")
	if r, ok := m.CurrentSession(); !ok || r.ID != "aaaaaaaa-wait" {
		t.Errorf("pane %%1: %v %v", r.ID, ok)
	}
	t.Setenv("TMUX_PANE", "%9")
	if _, ok := m.CurrentSession(); ok {
		t.Errorf("unknown pane")
	}
}

func TestBriefDataShell(t *testing.T) {
	m := newModelShell(t)
	m.gl.Mine[0].MergeStatus = "need_rebase"
	m.dismissed[m.inbox()[0].id] = time.Now()
	m.reindex()
	b, err := json.Marshal(m.BriefData())
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Inbox  []CtlItem
		Sprint []struct{ Key, Summary, Status string }
		MyMRs  []struct {
			Ref, Pipeline, Ticket string
			Reviewers             []string
			OpenThreads           int
			NeedsRebase           bool
		}
		Merged   []struct{ Ref, Pipeline string }
		Sessions []struct{ Name, Status, Ticket string }
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Inbox) != 3 {
		t.Errorf("snoozed items stay out of the brief: %d", len(got.Inbox))
	}
	if len(got.Sprint) != 2 || got.Sprint[0].Key != "ABC-1" {
		t.Errorf("done issues stay out: %+v", got.Sprint)
	}
	if len(got.MyMRs) != 1 || got.MyMRs[0].Ref != "billing!21" || got.MyMRs[0].Pipeline != "failed" || got.MyMRs[0].Ticket != "ABC-2" ||
		got.MyMRs[0].OpenThreads != 1 || !got.MyMRs[0].NeedsRebase || len(got.MyMRs[0].Reviewers) != 2 {
		t.Errorf("my MRs: %+v", got.MyMRs)
	}
	if len(got.Merged) != 1 || got.Merged[0].Pipeline != "failed" {
		t.Errorf("merged: %+v", got.Merged)
	}
	if len(got.Sessions) != 3 || got.Sessions[0].Status != "waiting" || got.Sessions[0].Ticket != "ABC-1" {
		t.Errorf("live sessions only: %+v", got.Sessions)
	}
}

func TestPrioShell(t *testing.T) {
	m := newModelShell(t)
	if len(m.CtlPrio()) != 0 {
		t.Error("no run log: no order")
	}
	if err := m.SetPrio([]string{"abc-1"}, ""); err == nil || !strings.Contains(err.Error(), "run log isn't open") {
		t.Errorf("no db: %v", err)
	}
	withRunsShell(t, m)
	other := mrShell("search", 11, "feat: search", "bob", "feat/s", time.Hour)
	m.gl.Review = append(m.gl.Review, other)
	for ref, want := range map[string]string{
		"x?":         "can't tell what",
		"!99":        "no MR !99",
		"!11":        "!11 is ambiguous: name the repo, e.g. payments!11",
		"billing!11": "no MR billing!11",
	} {
		if err := m.SetPrio([]string{ref}, ""); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", ref, err)
		}
	}
	link := "https://gitlab.example.com/g/x/-/merge_requests/7/"
	if err := m.SetPrio([]string{"abc-2", "search!11", "!21", link}, "Zdeněk, grooming"); err != nil {
		t.Fatal(err)
	}
	got := m.CtlPrio()
	if len(got) != 4 || got[0].Ref != "ABC-2" || got[0].Title != "Invoices" || got[0].Note != "Zdeněk, grooming" ||
		got[1].Ref != "search!11" || got[1].Subject != other.WebURL || got[1].Title != "feat: search" ||
		got[2].Ref != "billing!21" || got[3].Subject != strings.TrimRight(link, "/") || got[3].Pos != 4 {
		t.Errorf("order %+v", got)
	}
	if err := m.SetPrio(nil, ""); err != nil || len(m.CtlPrio()) != 0 {
		t.Errorf("cleared: %v", err)
	}
}

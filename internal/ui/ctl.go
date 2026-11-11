package ui

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/MartyJRE/claudia/internal/board"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/state"
	"github.com/MartyJRE/claudia/internal/tmux"
)

// The `claudia ctl` side: the same data the TUI shows, for a Claude session to
// read and act on from its shell.

type CtlItem struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Where       string    `json:"where"`
	From        string    `json:"from,omitempty"`
	Text        string    `json:"text"`
	URL         string    `json:"url,omitempty"`
	Ticket      string    `json:"ticket,omitempty"`
	Since       time.Time `json:"waiting_since"`
	Snoozed     bool      `json:"snoozed,omitempty"`
	ThisSession bool      `json:"this_session,omitempty"`
}

type CtlMR struct {
	URL      string `json:"url"`
	Title    string `json:"title,omitempty"`
	State    string `json:"state,omitempty"` // opened, merged, closed; empty if claudia doesn't track it
	Pipeline string `json:"pipeline,omitempty"`
	Approved bool   `json:"approved,omitempty"`
	// TeamsPosts are the Teams posts asking for its review, newest first.
	TeamsPosts []string `json:"teams_posts,omitempty"`
}

type CtlTicket struct {
	Key    string `json:"key"`
	Title  string `json:"title,omitempty"`
	Status string `json:"status,omitempty"`
	Done   bool   `json:"done,omitempty"`
}

type CtlHere struct {
	Session string     `json:"session"`
	Name    string     `json:"name"`
	Dir     string     `json:"dir"`
	Ticket  *CtlTicket `json:"ticket,omitempty"`
	MRs     []CtlMR    `json:"mrs,omitempty"`
	Items   []CtlItem  `json:"inbox_items"`
	Notes   []string   `json:"notes,omitempty"` // Obsidian notes about the ticket
}

// Headless builds the model from caches and live sessions without a terminal.
func Headless() *Model {
	m := New(Options{})
	m.sessions, m.workers = splitWorkers(m.loadSessions()().(sessionsMsg))
	if m.vault != nil {
		m.vault.Refresh()
		m.vaultNotes = m.vault.All()
	}
	m.reindex()
	return m
}

// CurrentSession finds the Claude session running in this tmux pane.
func (m *Model) CurrentSession() (row, bool) {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return row{}, false
	}
	for _, r := range m.sessions {
		if r.pane != nil && r.pane.ID == pane {
			return r, true
		}
	}
	return row{}, false
}

func (m *Model) linked(it inboxItem, sid string) bool {
	switch {
	case sid == "":
		return false
	case it.kind == kClaude:
		return it.sess.ID == sid
	case it.mr.WebURL != "":
		for _, u := range m.ix.SessionMRs[sid] {
			if u == it.mr.WebURL {
				return true
			}
		}
	}
	return it.ticket != "" && it.ticket == m.ix.SessionTicket[sid]
}

func (m *Model) CtlInbox(sid string) []CtlItem {
	var out []CtlItem
	for _, it := range m.inbox() {
		_, snoozed := m.dismissed[it.id]
		out = append(out, CtlItem{
			ID: it.id, Kind: kindLabel[it.kind], Where: it.where, From: it.who, Text: it.text,
			URL: it.url, Ticket: it.ticket, Since: it.since, Snoozed: snoozed, ThisSession: m.linked(it, sid),
		})
	}
	return out
}

func (m *Model) CtlHere(r row) CtlHere {
	h := CtlHere{Session: r.ID, Name: r.Name(), Dir: r.Dir()}
	if k := m.ix.SessionTicket[r.ID]; k != "" {
		t := &CtlTicket{Key: k}
		if is, ok := m.ix.Issues[k]; ok {
			t.Title, t.Status, t.Done = is.Summary, is.Status, is.Category == "done"
		}
		h.Ticket = t
	}
	for _, u := range m.ix.SessionMRs[r.ID] {
		mr := CtlMR{URL: u}
		if g, ok := m.ix.MRs[u]; ok {
			mr.Title, mr.State, mr.Pipeline, mr.Approved = g.Title, g.State, g.PipelineStatus(), g.Approved
		}
		for _, p := range m.teams.ForMR(u) {
			mr.TeamsPosts = append(mr.TeamsPosts, p.ID)
		}
		h.MRs = append(h.MRs, mr)
	}
	for _, n := range m.notesFor(m.ix.SessionTicket[r.ID]) {
		h.Notes = append(h.Notes, n.Path)
	}
	for _, it := range m.CtlInbox(r.ID) {
		if it.ThisSession {
			h.Items = append(h.Items, it)
		}
	}
	return h
}

// Snooze hides inbox items until they change, like x in the TUI.
func (m *Model) Snooze(ids []string) int {
	n := 0
	for _, id := range ids {
		if _, ok := m.dismissed[id]; !ok {
			m.dismissed[id] = time.Now()
			n++
		}
	}
	_ = state.Save(dismissedFile, m.dismissed)
	return n
}

// reloadDismissed picks up snoozes written by `claudia ctl`.
func (m *Model) reloadDismissed() bool {
	st, err := os.Stat(state.Path(dismissedFile))
	if err != nil || !st.ModTime().After(m.dismissedAt) {
		return false
	}
	m.dismissedAt = st.ModTime()
	m.dismissed = loadDismissed()
	return true
}

// Home switches the terminal back to claudia's session.
func Home() error { return switchClient("=" + tmux.Home) }

// BriefData is what the inbox brief is written from: compact on purpose, since
// it goes to Claude on every run.
func (m *Model) BriefData() any {
	type issue struct {
		Key, Summary, Status, Epic, Sprint string
	}
	type mr struct {
		Ref, Title, Pipeline, Ticket string
		Approved                     bool     `json:",omitempty"`
		Reviewers                    []string `json:",omitempty"`
		OpenThreads                  int      `json:",omitempty"`
		NeedsRebase                  bool     `json:",omitempty"`
	}
	type sess struct{ Name, Status, Ticket string }
	type meeting struct{ Subject, Start, End, Response string }
	var out struct {
		Inbox    []CtlItem
		Sprint   []issue
		MyMRs    []mr
		Merged   []mr
		Sessions []sess
		Meetings []meeting `json:",omitempty"` // the rest of today
	}
	for _, it := range m.CtlInbox("") {
		if !it.Snoozed {
			out.Inbox = append(out.Inbox, it)
		}
	}
	for _, is := range m.jira.Sprint {
		if is.Category == "done" {
			continue
		}
		i := issue{Key: is.Key, Summary: is.Summary, Status: is.Status, Epic: is.ParentSummary}
		if is.Sprint != nil {
			i.Sprint = is.Sprint.Name
		}
		out.Sprint = append(out.Sprint, i)
	}
	conv := func(g gitlab.MR, pipeline string) mr {
		r := mr{Ref: fmt.Sprintf("%s!%d", g.Repo(), g.IID), Title: g.Title, Pipeline: pipeline, Ticket: mrTicket(g),
			Approved: g.Approved, OpenThreads: g.OpenThreads(), NeedsRebase: g.MergeStatus == "need_rebase"}
		for _, u := range g.Reviewers {
			r.Reviewers = append(r.Reviewers, u.Username)
		}
		return r
	}
	for _, g := range m.gl.Mine {
		out.MyMRs = append(out.MyMRs, conv(g, g.PipelineStatus()))
	}
	for _, g := range m.gl.Merged {
		p := ""
		if g.MergePipeline != nil {
			p = g.MergePipeline.Status
		}
		out.Merged = append(out.Merged, conv(g, p))
	}
	for _, r := range m.sessions {
		if r.live() {
			out.Sessions = append(out.Sessions, sess{r.Name(), string(r.Live.Status), m.ix.SessionTicket[r.ID]})
		}
	}
	y, mo, d := clockNow().Date()
	for _, e := range m.teams.Events {
		if s := e.Start.Local(); e.End.After(clockNow()) && s.Year() == y && s.Month() == mo && s.Day() == d && e.Response != "declined" {
			out.Meetings = append(out.Meetings, meeting{e.Subject, s.Format("15:04"), e.End.Local().Format("15:04"), e.Response})
		}
	}
	return out
}

// CtlAgreed is one card in the agreed order.
type CtlAgreed struct {
	Pos     int       `json:"pos"`
	Ref     string    `json:"ref"`
	Subject string    `json:"subject"`
	Title   string    `json:"title,omitempty"`
	Note    string    `json:"note,omitempty"`
	Agreed  time.Time `json:"agreed"`
}

// CtlPrio is the agreed order, once agreements from a closed sprint are gone.
func (m *Model) CtlPrio() []CtlAgreed {
	m.loadAgreed()
	out := []CtlAgreed{}
	if m.db == nil {
		return out
	}
	for _, a := range m.db.Agreed() {
		c := CtlAgreed{Pos: a.Pos, Ref: a.Subject, Subject: a.Subject, Note: a.Note, Agreed: a.At}
		if is, ok := m.ix.Issues[a.Subject]; ok {
			c.Title = is.Summary
		}
		if mr, ok := m.mrByURL(a.Subject); ok {
			c.Ref, c.Title = mr.Repo()+fmt.Sprintf("!%d", mr.IID), mr.Title
		}
		out = append(out, c)
	}
	return out
}

// SetPrio replaces the agreed order with refs: ticket keys, MR links, or
// !IID / repo!IID for an MR claudia knows.
func (m *Model) SetPrio(refs []string, note string) error {
	if m.db == nil {
		return fmt.Errorf("the run log isn't open: %v", m.dbErr)
	}
	var subjects []string
	for _, ref := range refs {
		s, err := m.prioSubject(ref)
		if err != nil {
			return err
		}
		subjects = append(subjects, s)
	}
	return m.db.SetAgreed(subjects, note, board.ActiveSprint(m.jira.Sprint), clockNow())
}

var ticketRef = regexp.MustCompile(`^[A-Z][A-Z0-9]+-\d+$`)

func (m *Model) prioSubject(ref string) (string, error) {
	switch {
	case ticketRef.MatchString(strings.ToUpper(ref)):
		return strings.ToUpper(ref), nil
	case strings.Contains(ref, "/merge_requests/"):
		return strings.TrimRight(ref, "/"), nil
	case strings.Contains(ref, "!"):
		repo, iid, _ := strings.Cut(ref, "!")
		var found []gitlab.MR
		for _, mr := range slices.Concat(m.gl.Review, m.gl.Mine) {
			if fmt.Sprint(mr.IID) == iid && (repo == "" || mr.Repo() == repo || mr.Project() == repo) &&
				!slices.ContainsFunc(found, func(o gitlab.MR) bool { return o.WebURL == mr.WebURL }) {
				found = append(found, mr)
			}
		}
		switch len(found) {
		case 1:
			return found[0].WebURL, nil
		case 0:
			return "", fmt.Errorf("no MR %s among your reviews and MRs", ref)
		}
		return "", fmt.Errorf("%s is ambiguous: name the repo, e.g. %s!%s", ref, found[0].Repo(), iid)
	}
	return "", fmt.Errorf("can't tell what %q is: give a ticket key, an MR link or !IID", ref)
}

func (m *Model) mrByURL(url string) (gitlab.MR, bool) {
	for _, mr := range slices.Concat(m.gl.Review, m.gl.Mine) {
		if mr.WebURL == url {
			return mr, true
		}
	}
	return gitlab.MR{}, false
}

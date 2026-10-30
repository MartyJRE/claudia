package ui

import (
	"slices"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/links"
)

// index joins sessions, MRs and issues so every view can show the other two.
type index struct {
	sessionTicket map[string]string
	sessionMRs    map[string][]string
	mrSessions    map[string][]string
	ticketMRs     map[string][]gitlab.MR
	ticketSess    map[string][]string
	issues        map[string]atlassian.Issue
	mrs           map[string]gitlab.MR
}

func buildIndex(rows []row, gl gitlab.Snapshot, js atlassian.JiraSnapshot, l *links.Links) index {
	ix := index{
		sessionTicket: map[string]string{},
		sessionMRs:    map[string][]string{},
		mrSessions:    map[string][]string{},
		ticketMRs:     map[string][]gitlab.MR{},
		ticketSess:    map[string][]string{},
		issues:        map[string]atlassian.Issue{},
		mrs:           map[string]gitlab.MR{},
	}
	for _, i := range append(js.Team, js.Sprint...) {
		ix.issues[i.Key] = i
	}
	for _, list := range [][]gitlab.MR{gl.Review, gl.Mine, gl.Merged} {
		for _, m := range list {
			ix.mrs[m.WebURL] = m
			if t := mrTicket(m); t != "" && !slices.ContainsFunc(ix.ticketMRs[t], func(o gitlab.MR) bool { return o.WebURL == m.WebURL }) {
				ix.ticketMRs[t] = append(ix.ticketMRs[t], m)
			}
		}
	}
	for _, r := range rows {
		var urls []string
		if r.Transcript != nil {
			for _, p := range r.PRs {
				urls = append(urls, p.URL)
			}
		}
		for _, u := range l.MRs(r.ID) {
			if !slices.Contains(urls, u) {
				urls = append(urls, u)
			}
		}
		ix.sessionMRs[r.ID] = urls
		for _, u := range urls {
			ix.mrSessions[u] = append(ix.mrSessions[u], r.ID)
		}
		t := ix.resolveTicket(r.Session, urls, l)
		if t != "" {
			ix.sessionTicket[r.ID] = t
			ix.ticketSess[t] = append(ix.ticketSess[t], r.ID)
		}
	}
	return ix
}

// resolveTicket: by hand → from the session's MR when the session isn't on that
// MR's branch (a review run in a clone sitting on another story) → branch → title
// → last ticket branch.
func (ix index) resolveTicket(s claude.Session, urls []string, l *links.Links) string {
	if k, ok := l.Ticket(s.ID); ok {
		if k == "-" {
			return ""
		}
		return k
	}
	if len(urls) > 0 && s.Transcript != nil {
		onBranch := false
		var mrKey string
		for _, u := range urls {
			m, ok := ix.mrs[u]
			if !ok {
				continue
			}
			if m.SourceBranch == s.Branch {
				onBranch = true
			}
			if mrKey == "" {
				mrKey = mrTicket(m)
			}
		}
		if !onBranch && mrKey != "" {
			return mrKey
		}
	}
	return s.Ticket()
}

func mrTicket(m gitlab.MR) string {
	if t := claude.TicketFrom(m.SourceBranch); t != "" {
		return t
	}
	return claude.TicketFrom(m.Title)
}

// related returns open MRs that belong to the same change: same ticket, or the
// same source branch in another repo.
func related(m gitlab.MR, gl gitlab.Snapshot) []gitlab.MR {
	t := mrTicket(m)
	out := []gitlab.MR{m}
	for _, list := range [][]gitlab.MR{gl.Review, gl.Mine} {
		for _, o := range list {
			if o.WebURL == m.WebURL || slices.ContainsFunc(out, func(x gitlab.MR) bool { return x.WebURL == o.WebURL }) {
				continue
			}
			if (t != "" && mrTicket(o) == t) || (o.SourceBranch == m.SourceBranch && o.ProjectID != m.ProjectID) {
				out = append(out, o)
			}
		}
	}
	return out
}

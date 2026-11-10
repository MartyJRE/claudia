package board

import (
	"slices"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/links"
)

// Index joins sessions, MRs and issues so every view can show the other two.
type Index struct {
	SessionTicket map[string]string
	SessionMRs    map[string][]string
	MRSessions    map[string][]string
	TicketMRs     map[string][]gitlab.MR
	TicketSess    map[string][]string
	Issues        map[string]atlassian.Issue
	MRs           map[string]gitlab.MR
}

func BuildIndex(sessions []claude.Session, gl gitlab.Snapshot, js atlassian.JiraSnapshot, l *links.Links) Index {
	ix := Index{
		SessionTicket: map[string]string{},
		SessionMRs:    map[string][]string{},
		MRSessions:    map[string][]string{},
		TicketMRs:     map[string][]gitlab.MR{},
		TicketSess:    map[string][]string{},
		Issues:        map[string]atlassian.Issue{},
		MRs:           map[string]gitlab.MR{},
	}
	for _, i := range append(js.Team, js.Sprint...) {
		ix.Issues[i.Key] = i
	}
	for _, list := range [][]gitlab.MR{gl.Review, gl.Mine, gl.Merged} {
		for _, m := range list {
			ix.MRs[m.WebURL] = m
			if t := MRTicket(m); t != "" && !slices.ContainsFunc(ix.TicketMRs[t], func(o gitlab.MR) bool { return o.WebURL == m.WebURL }) {
				ix.TicketMRs[t] = append(ix.TicketMRs[t], m)
			}
		}
	}
	for _, s := range sessions {
		var urls []string
		if s.Transcript != nil {
			for _, p := range s.PRs {
				urls = append(urls, p.URL)
			}
		}
		for _, u := range l.MRs(s.ID) {
			if !slices.Contains(urls, u) {
				urls = append(urls, u)
			}
		}
		ix.SessionMRs[s.ID] = urls
		for _, u := range urls {
			ix.MRSessions[u] = append(ix.MRSessions[u], s.ID)
		}
		t := ix.ResolveTicket(s, urls, l)
		if t != "" {
			ix.SessionTicket[s.ID] = t
			ix.TicketSess[t] = append(ix.TicketSess[t], s.ID)
		}
	}
	return ix
}

// ResolveTicket: by hand → from the session's MR when the session isn't on that
// MR's branch (a review run in a clone sitting on another story) → branch → title
// → last ticket branch.
func (ix Index) ResolveTicket(s claude.Session, urls []string, l *links.Links) string {
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
			m, ok := ix.MRs[u]
			if !ok {
				continue
			}
			if m.SourceBranch == s.Branch {
				onBranch = true
			}
			if mrKey == "" {
				mrKey = MRTicket(m)
			}
		}
		if !onBranch && mrKey != "" {
			return mrKey
		}
	}
	return s.Ticket()
}

func MRTicket(m gitlab.MR) string {
	if t := claude.TicketFrom(m.SourceBranch); t != "" {
		return t
	}
	return claude.TicketFrom(m.Title)
}

// Related returns open MRs that belong to the same change: same ticket, or the
// same source branch in another repo.
func Related(m gitlab.MR, gl gitlab.Snapshot) []gitlab.MR {
	t := MRTicket(m)
	out := []gitlab.MR{m}
	for _, list := range [][]gitlab.MR{gl.Review, gl.Mine} {
		for _, o := range list {
			if o.WebURL == m.WebURL || slices.ContainsFunc(out, func(x gitlab.MR) bool { return x.WebURL == o.WebURL }) {
				continue
			}
			if (t != "" && MRTicket(o) == t) || (o.SourceBranch == m.SourceBranch && o.ProjectID != m.ProjectID) {
				out = append(out, o)
			}
		}
	}
	return out
}

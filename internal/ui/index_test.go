package ui

import (
	"testing"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/links"
)

func TestResolveTicket(t *testing.T) {
	mr := gitlab.MR{WebURL: "u1", SourceBranch: "feat/ABC-7/x"}
	gl := gitlab.Snapshot{Review: []gitlab.MR{mr}}
	l := &links.Links{SessionMRs: map[string][]string{"review": {"u1"}}, SessionTickets: map[string]string{"manual": "ABC-9", "none": "-"}}
	rows := []row{
		// A review run in a clone sitting on another story's branch takes the MR's ticket.
		{Session: claude.Session{ID: "review", Transcript: &claude.Transcript{Branch: "feat/ABC-3/other"}}},
		{Session: claude.Session{ID: "branch", Transcript: &claude.Transcript{Branch: "devel", TicketBranch: "feat/ABC-5/y"}}},
		{Session: claude.Session{ID: "manual", Transcript: &claude.Transcript{Branch: "feat/ABC-5/y"}}},
		{Session: claude.Session{ID: "none", Transcript: &claude.Transcript{Branch: "feat/ABC-5/y"}}},
	}
	ix := buildIndex(rows, gl, atlassian.JiraSnapshot{}, l)
	for id, want := range map[string]string{"review": "ABC-7", "branch": "ABC-5", "manual": "ABC-9", "none": ""} {
		if got := ix.SessionTicket[id]; got != want {
			t.Errorf("%s: got %q want %q", id, got, want)
		}
	}
}

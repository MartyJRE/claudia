package ui

import (
	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/board"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/links"
)

type index = board.Index

func buildIndex(rows []row, gl gitlab.Snapshot, js atlassian.JiraSnapshot, l *links.Links) index {
	ss := make([]claude.Session, len(rows))
	for i, r := range rows {
		ss[i] = r.Session
	}
	return board.BuildIndex(ss, gl, js, l)
}

var (
	mrTicket = board.MRTicket
	related  = board.Related
)

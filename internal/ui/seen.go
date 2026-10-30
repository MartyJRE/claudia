package ui

import (
	"fmt"

	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/state"
)

const seenFile = "mr-seen.json"

type seenMR struct {
	SHA      string `json:"sha"`
	Notes    int    `json:"notes"`
	Pipeline string `json:"pipeline"`
	Merge    string `json:"merge"`
}

func snapshotOf(m gitlab.MR) seenMR {
	return seenMR{m.SHA, m.UserNotesCount, m.PipelineStatus(), m.MergeStatus}
}

type seenStore map[string]seenMR

func loadSeen() (seenStore, bool) {
	s := seenStore{}
	ok := state.Load(seenFile, &s)
	return s, ok
}

func (s seenStore) mark(ms ...gitlab.MR) {
	for _, m := range ms {
		s[m.WebURL] = snapshotOf(m)
	}
	_ = state.Save(seenFile, s)
}

type chip struct {
	text string
	kind int // 0 news, 1 bad
}

// changes lists what moved on an MR since you last opened it.
func (s seenStore) changes(m gitlab.MR) []chip {
	old, ok := s[m.WebURL]
	if !ok {
		return []chip{{"new", 0}}
	}
	var out []chip
	if old.SHA != "" && m.SHA != old.SHA {
		out = append(out, chip{"new commits", 0})
	}
	if d := m.UserNotesCount - old.Notes; d > 0 {
		out = append(out, chip{fmt.Sprintf("+%d comments", d), 0})
	}
	if p := m.PipelineStatus(); p != old.Pipeline && p != "" {
		k := 0
		if p == "failed" {
			k = 1
		}
		out = append(out, chip{"pipeline " + p, k})
	}
	if m.MergeStatus != old.Merge {
		switch m.MergeStatus {
		case "need_rebase":
			out = append(out, chip{"needs rebase", 1})
		case "conflict", "broken_status":
			out = append(out, chip{"conflicts", 1})
		}
	}
	return out
}

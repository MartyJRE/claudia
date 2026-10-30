package ui

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MartyJRE/claudia/internal/gitlab"
)

type mrRow struct {
	gitlab.MR
	merged bool
}

var mrCols = []col{
	{"MR", 26, 0},
	{"TITLE", 0, 0},
	{"AUTHOR", 16, 110},
	{"TICKET", 10, 0},
	{"PIPELINE", 10, 0},
	{"APPROVALS", 10, 120},
	{"CHANGES", 24, 95},
	{"AGE", 5, 0},
}

func (m *Model) pendingReviews() int {
	n := 0
	for _, mr := range m.gl.Review {
		if !mr.Draft && !mr.ApprovedByMe(m.gl.Me.Username) {
			n++
		}
	}
	return n
}

func (m *Model) failedPipelines() int {
	n := 0
	for _, mr := range m.gl.Mine {
		if mr.PipelineStatus() == "failed" {
			n++
		}
	}
	for _, mr := range m.gl.Merged {
		if mr.MergePipeline != nil && mr.MergePipeline.Status == "failed" {
			n++
		}
	}
	return n
}

func (m *Model) filterMRs(match func(...string) bool) []mrRow {
	var out []mrRow
	add := func(mr gitlab.MR, merged bool) {
		if match(mr.Title, mr.Repo(), mr.Author.Username, mr.SourceBranch, mrTicket(mr), fmt.Sprintf("!%d", mr.IID)) {
			out = append(out, mrRow{mr, merged})
		}
	}
	switch m.view {
	case vReview:
		for _, mr := range m.gl.Review {
			if m.showAll || !mr.ApprovedByMe(m.gl.Me.Username) {
				add(mr, false)
			}
		}
		// Unseen activity first, then drafts last.
		slices.SortStableFunc(out, func(a, b mrRow) int {
			if a.Draft != b.Draft {
				if a.Draft {
					return 1
				}
				return -1
			}
			return b.UpdatedAt.Compare(a.UpdatedAt)
		})
	case vMine:
		for _, mr := range m.gl.Mine {
			add(mr, false)
		}
		for _, mr := range m.gl.Merged {
			add(mr, true)
		}
	}
	return out
}

func (m *Model) listMRs(h int) string {
	rows := m.vis.mrs
	empty := "nothing waiting for your review"
	if m.view == vMine {
		empty = "no open or recently merged MRs"
	}
	if m.gl.FetchedAt.IsZero() {
		empty = spinner[m.frame%len(spinner)] + " asking GitLab…"
		if m.errs[srcGitlab] != nil {
			empty = m.errs[srcGitlab].Error()
		}
	}
	cols := slices.Clone(mrCols)
	if m.view == vMine {
		cols[2].title, cols[2].w = "REVIEWERS", 22
	}
	return m.table(cols, len(rows), &m.cur[m.view], h, func(i int, sel bool) []string {
		r := rows[i]
		title := r.Title
		st := sText
		if sel {
			st = sBold
		}
		if r.Draft {
			title = "Draft: " + strings.TrimPrefix(title, "Draft: ")
			st = sDim
		}
		second := sDim.Render(r.Author.Username)
		if m.view == vMine {
			second = reviewers(r.MR)
		}
		return []string{
			sKey.Render(fmt.Sprintf("!%d", r.IID)) + " " + sText.Render(r.Repo()),
			st.Render(title),
			second,
			sTicket.Render(mrTicket(r.MR)),
			pipelineText(r),
			m.approvalText(r.MR),
			m.chipsText(r),
			sDim.Render(age(r.UpdatedAt)),
		}
	}, empty)
}

func pipelineText(r mrRow) string {
	p := r.Pipeline
	if r.merged {
		p = r.MergePipeline
		if p == nil {
			return sDim.Render("merged")
		}
	}
	if p == nil {
		return sDim.Render("—")
	}
	return pipelineStatus(p.Status)
}

func pipelineStatus(s string) string {
	switch s {
	case "success":
		return sOK.Render("✓ passed")
	case "failed":
		return sErr.Render("✗ failed")
	case "running", "pending", "created", "preparing", "waiting_for_resource":
		return lipgloss.NewStyle().Foreground(blue).Render("● " + s)
	case "canceled", "skipped":
		return sDim.Render("○ " + s)
	case "manual":
		return lipgloss.NewStyle().Foreground(yellow).Render("▶ manual")
	}
	return sDim.Render(s)
}

func (m *Model) approvalText(mr gitlab.MR) string {
	if mr.ApprovedByMe(m.gl.Me.Username) {
		return sOK.Render("✓ you")
	}
	if mr.Approved {
		return sOK.Render(fmt.Sprintf("✓ %d", len(mr.ApprovedBy)))
	}
	if len(mr.ApprovedBy) > 0 {
		return lipgloss.NewStyle().Foreground(blue).Render(fmt.Sprintf("%d, %d left", len(mr.ApprovedBy), mr.ApprovalsLeft))
	}
	if mr.ApprovalsLeft > 0 {
		return sDim.Render(fmt.Sprintf("%d needed", mr.ApprovalsLeft))
	}
	return ""
}

func mrState(mr gitlab.MR) string {
	switch {
	case mr.State == "merged":
		return lipgloss.NewStyle().Foreground(purple).Render("merged")
	case mr.State == "closed":
		return sDim.Render("closed")
	case mr.Draft:
		return sDim.Render("draft")
	}
	return pipelineStatus(mr.PipelineStatus())
}

func (m *Model) chipsText(r mrRow) string {
	if r.merged {
		return ""
	}
	var parts []string
	for _, c := range m.seen.changes(r.MR) {
		st := lipgloss.NewStyle().Foreground(blue)
		if c.kind == 1 {
			st = sErr
		}
		parts = append(parts, st.Render(c.text))
	}
	switch r.MergeStatus {
	case "need_rebase":
		if !slices.ContainsFunc(parts, func(s string) bool { return strings.Contains(s, "rebase") }) {
			parts = append(parts, sErr.Render("rebase"))
		}
	case "conflict":
		parts = append(parts, sErr.Render("conflict"))
	}
	if m.view == vMine {
		if n := r.OpenThreads(); n > 0 {
			parts = append(parts, lipgloss.NewStyle().Foreground(yellow).Render(fmt.Sprintf("%d open threads", n)))
		} else if !r.DiscussionsResolved && r.Threads == nil {
			parts = append(parts, lipgloss.NewStyle().Foreground(yellow).Render("open threads"))
		}
	}
	return strings.Join(parts, sDim.Render(" · "))
}

func (m *Model) currentMR() (mrRow, bool) {
	c := m.cur[m.view].pos
	if c < 0 || c >= len(m.vis.mrs) {
		return mrRow{}, false
	}
	return m.vis.mrs[c], true
}

func (m *Model) detailMR() string {
	r, ok := m.currentMR()
	if !ok {
		return m.panel(nil)
	}
	head := sKey.Render(fmt.Sprintf("!%d", r.IID)) + " " + sBold.Render(r.Title)
	lines := []string{head}
	meta := []string{sDim.Render(r.Project()), sBranch.Render(" " + r.SourceBranch + " → " + r.TargetBranch), sDim.Render("by " + r.Author.Username)}
	lines = append(lines, strings.Join(meta, sDim.Render("  ·  ")))

	if t := mrTicket(r.MR); t != "" {
		l := sTicket.Render(t)
		if is, ok := m.ix.issues[t]; ok {
			l += " " + sText.Render(is.Summary) + sDim.Render(" · ") + statusText(is)
		}
		lines = append(lines, l)
	}
	if rel := related(r.MR, m.gl); len(rel) > 1 {
		var refs []string
		for _, o := range rel[1:] {
			refs = append(refs, m.mrRef(o.WebURL))
		}
		lines = append(lines, sDim.Render("same change: ")+strings.Join(refs, "   "))
	}
	if ids := m.ix.mrSessions[r.WebURL]; len(ids) > 0 {
		lines = append(lines, sDim.Render("sessions: ")+m.sessionRefs(ids))
	}
	if m.view == vMine {
		if len(r.Reviewers) > 0 {
			lines = append(lines, sDim.Render("reviewers: ")+reviewers(r.MR))
		} else {
			lines = append(lines, lipgloss.NewStyle().Foreground(yellow).Render("no reviewer assigned"))
		}
		for _, t := range r.Threads {
			if t.Resolved {
				continue
			}
			lines = append(lines, threadLine(t))
		}
	} else if len(r.ApprovedBy) > 0 {
		lines = append(lines, sDim.Render("approved by "+strings.Join(r.ApprovedBy, ", ")))
	}
	return m.panel(lines)
}

// reviewers marks who already approved.
func reviewers(mr gitlab.MR) string {
	var parts []string
	for _, u := range mr.Reviewers {
		if slices.Contains(mr.ApprovedBy, u.Username) {
			parts = append(parts, sOK.Render("✓ "+u.Username))
		} else {
			parts = append(parts, sText.Render(u.Username))
		}
	}
	if len(parts) == 0 {
		return sDim.Render("—")
	}
	return strings.Join(parts, sDim.Render(", "))
}

func threadLine(t gitlab.Thread) string {
	where := ""
	if t.Path != "" {
		where = sBranch.Render(fmt.Sprintf("%s:%d ", lastPath(t.Path), t.Line))
	}
	dot := lipgloss.NewStyle().Foreground(yellow).Render("● ")
	if t.Resolved {
		dot = sDim.Render("✓ ")
	}
	reply := ""
	if t.Replies > 0 {
		reply = sDim.Render(fmt.Sprintf("  (%d replies, last %s)", t.Replies, t.LastBy))
	}
	return dot + sKey.Render(t.Author) + " " + where + sText.Render(oneLine(t.Body)) + reply
}

func lastPath(p string) string {
	parts := strings.Split(p, "/")
	if len(parts) > 2 {
		return "…/" + strings.Join(parts[len(parts)-2:], "/")
	}
	return p
}

func (m *Model) chooseThreads(mr gitlab.MR) {
	if len(mr.Threads) == 0 {
		m.setFlash("no review threads", false)
		return
	}
	var items []choice
	for _, t := range mr.Threads {
		items = append(items, choice{label: threadLine(t), search: t.Author + " " + t.Path + " " + t.Body, value: t.URL})
	}
	m.chooser = newChooser(fmt.Sprintf("threads on !%d", mr.IID), items, func(m *Model, ch choice) (tea.Model, tea.Cmd) {
		return m, openURL(ch.value.(string))
	})
}

func (m *Model) rebase(mr gitlab.MR) (tea.Model, tea.Cmd) {
	if !m.confirm("B"+mr.WebURL, fmt.Sprintf("rebase !%d %s onto %s", mr.IID, mr.Repo(), mr.TargetBranch)) {
		return m, nil
	}
	m.setFlash(fmt.Sprintf("rebasing !%d…", mr.IID), false)
	return m, func() tea.Msg {
		if err := gitlab.Rebase(mr); err != nil {
			return flashMsg{err.Error(), true}
		}
		return refreshMsg(srcGitlab)
	}
}

func (m *Model) sessionRefs(ids []string) string {
	var parts []string
	for _, id := range ids {
		for _, r := range m.sessions {
			if r.ID == id {
				dot := sDim.Render("○ ")
				if r.live() {
					dot = sOK.Render("● ")
				}
				parts = append(parts, dot+sText.Render(r.Name()))
			}
		}
	}
	return strings.Join(parts, "   ")
}

func (m *Model) keyMRs(k string) (tea.Model, tea.Cmd) {
	r, ok := m.currentMR()
	switch k {
	case "a":
		m.showAll = !m.showAll
		m.refilter()
		return m, nil
	case "o":
		if ok {
			m.seen.mark(r.MR)
			return m, openURL(r.WebURL)
		}
	case "p":
		if ok {
			p := r.Pipeline
			if r.merged {
				p = r.MergePipeline
			}
			if p != nil {
				return m, openURL(p.WebURL)
			}
			m.setFlash("no pipeline", true)
		}
	case "s":
		if ok {
			m.seen.mark(r.MR)
		}
	case "y":
		if ok {
			return m, copyText(r.WebURL, "MR link")
		}
	case "c":
		if ok {
			m.chooseSessions(m.ix.mrSessions[r.WebURL], "sessions for !"+fmt.Sprint(r.IID))
		}
	case "v":
		if ok {
			m.chooseThreads(r.MR)
		}
	case "f":
		if ok && m.view == vMine && !r.merged {
			if r.OpenThreads() == 0 {
				m.setFlash("no open review threads", false)
				return m, nil
			}
			return m.addressThreads(r.MR)
		}
	case "B":
		if ok && !r.merged {
			return m.rebase(r.MR)
		}
	case "J":
		if ok {
			return m.gotoTicket(mrTicket(r.MR))
		}
	case "enter":
		if !ok {
			return m, nil
		}
		if m.view == vMine {
			if ids := m.ix.mrSessions[r.WebURL]; len(ids) > 0 {
				m.chooseSessions(ids, "sessions for !"+fmt.Sprint(r.IID))
				return m, nil
			}
			m.seen.mark(r.MR)
			return m, openURL(r.WebURL)
		}
		return m.reviewWithClaude(r.MR)
	case "A", "U":
		if !ok || m.view != vReview {
			return m, nil
		}
		verb, fn := "approve", gitlab.Approve
		if k == "U" {
			verb, fn = "unapprove", gitlab.Unapprove
		}
		if !m.confirm(k+r.WebURL, fmt.Sprintf("%s !%d %s", verb, r.IID, r.Repo())) {
			return m, nil
		}
		mr := r.MR
		return m, func() tea.Msg {
			if err := fn(mr); err != nil {
				return flashMsg{err.Error(), true}
			}
			return refreshMsg(srcGitlab)
		}
	}
	return m, nil
}

func (m *Model) chooseSessions(ids []string, title string) {
	var items []choice
	for _, id := range ids {
		for _, r := range m.sessions {
			if r.ID != id {
				continue
			}
			st := "closed"
			if r.live() {
				st = string(r.Live.Status)
			}
			items = append(items, choice{label: sText.Render(r.Name()), hint: st + " · " + age(r.LastActive()), search: r.Name(), value: r})
		}
	}
	if len(items) == 0 {
		m.setFlash("no Claude sessions linked yet", true)
		return
	}
	m.chooser = newChooser(title, items, func(m *Model, ch choice) (tea.Model, tea.Cmd) {
		return m.jumpTo(ch.value.(row))
	})
}

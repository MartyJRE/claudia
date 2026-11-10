package ui

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MartyJRE/claudia/internal/brief"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/teams"
)

type inboxKind int

const (
	kReview inboxKind = iota
	kThreads
	kMention
	kPage
	kClaude
	kTeams
	kInvite
	kMail
)

var kindLabel = map[inboxKind]string{
	kReview:  "review",
	kThreads: "threads",
	kMention: "mention",
	kPage:    "page",
	kClaude:  "claude",
	kTeams:   "teams",
	kInvite:  "invite",
	kMail:    "mail",
}

// inboxItem is anything someone (or a session) is waiting on you for.
type inboxItem struct {
	id     string // snooze key; it embeds a version so a change brings the item back
	kind   inboxKind
	where  string
	who    string
	text   string
	since  time.Time
	url    string
	ticket string
	mr     gitlab.MR
	sess   row
	ment   mention
	tw     teams.Waiting
}

func (m *Model) inbox() []inboxItem {
	var out []inboxItem
	for _, mr := range m.gl.Review {
		if mr.Draft || mr.ApprovedByMe(m.gl.Me.Username) || mr.ReviewedByMe() {
			continue
		}
		out = append(out, inboxItem{
			id: "rev:" + mr.WebURL + ":" + mr.SHA, kind: kReview,
			where: fmt.Sprintf("%s !%d", mr.Repo(), mr.IID), who: mr.Author.Username, text: mr.Title,
			since: mr.CreatedAt, url: mr.WebURL, ticket: mrTicket(mr), mr: mr,
		})
	}
	for _, mr := range m.gl.Mine {
		var open []gitlab.Thread
		for _, t := range mr.Threads {
			if !t.Resolved {
				open = append(open, t)
			}
		}
		if len(open) == 0 {
			continue
		}
		var who []string
		since := open[0].At
		for _, t := range open {
			if !slices.Contains(who, t.Author) {
				who = append(who, t.Author)
			}
			if t.At.Before(since) {
				since = t.At
			}
		}
		out = append(out, inboxItem{
			id: fmt.Sprintf("thr:%s:%d:%s", mr.WebURL, len(open), mr.SHA), kind: kThreads,
			where: fmt.Sprintf("%s !%d", mr.Repo(), mr.IID), who: strings.Join(who, ", "),
			text:  fmt.Sprintf("%d open: %s", len(open), oneLine(open[0].Body)),
			since: since, url: mr.WebURL, ticket: mrTicket(mr), mr: mr,
		})
	}
	for _, x := range m.mentions() {
		if !m.open(x) {
			continue
		}
		k := kMention
		if !x.jira {
			k = kPage
		}
		it := inboxItem{id: x.id, kind: k, where: x.where, who: x.author, text: x.text, since: x.at, url: x.url, ment: x}
		if x.jira {
			it.ticket = x.where
		} else {
			it.text = x.title
		}
		out = append(out, it)
	}
	for _, w := range m.teams.Waiting {
		if m.coveredByMRs(w) {
			continue
		}
		text, k, since := w.Text, kTeams, w.At
		if w.Unread {
			text = "● " + text
		}
		if w.Kind == "invite" {
			// An invite waits from when it arrived, which Graph doesn't say; list it now.
			k, since = kInvite, m.teams.FetchedAt
		}
		if w.Kind == "mail" {
			k = kMail
		}
		out = append(out, inboxItem{id: "tm:" + w.Ref, kind: k, where: w.Where, who: w.From, text: text, since: since, url: w.WebURL, tw: w})
	}
	for _, r := range m.sessions {
		if !r.needsYou() {
			continue
		}
		text := r.Name()
		if ask := strings.TrimSpace(strings.TrimPrefix(r.summary, needsYouPrefix)); ask != r.summary && ask != "" && r.Live.Status != claude.StatusWaiting {
			text += " · " + ask
		}
		out = append(out, inboxItem{
			id: fmt.Sprintf("cc:%s:%d", r.ID, r.Live.UpdatedAt), kind: kClaude,
			where: project(r.Dir()), who: "claude", text: text, since: r.Live.Since(),
			ticket: m.ix.SessionTicket[r.ID], sess: r,
		})
	}
	slices.SortStableFunc(out, func(a, b inboxItem) int { return a.since.Compare(b.since) })
	return out
}

func (m *Model) filterInbox(match func(...string) bool) []inboxItem {
	var out []inboxItem
	for _, it := range m.inbox() {
		if _, snoozed := m.dismissed[it.id]; snoozed && !m.showAll {
			continue
		}
		if match(kindLabel[it.kind], it.where, it.who, it.text, it.ticket) {
			out = append(out, it)
		}
	}
	return out
}

func (m *Model) inboxCount() int {
	n := 0
	for _, it := range m.inbox() {
		if _, snoozed := m.dismissed[it.id]; !snoozed {
			n++
		}
	}
	return n
}

var inboxCols = []col{
	{"WAITING", 8, 0},
	{"", 9, 0},
	{"WHERE", 24, 0},
	{"FROM", 20, 100},
	{"", 0, 0},
}

func kindStyle(k inboxKind) lipgloss.Style {
	switch k {
	case kReview:
		return lipgloss.NewStyle().Foreground(orange)
	case kThreads:
		return lipgloss.NewStyle().Foreground(yellow)
	case kMention:
		return lipgloss.NewStyle().Foreground(purple)
	case kPage:
		return lipgloss.NewStyle().Foreground(blue)
	case kTeams:
		return lipgloss.NewStyle().Foreground(cyan)
	case kInvite:
		return lipgloss.NewStyle().Foreground(red)
	case kMail:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#f5a3c7"))
	}
	return lipgloss.NewStyle().Foreground(green)
}

// waiting colours how long someone has been blocked on you.
func waiting(t time.Time) string {
	d := time.Since(t)
	st := sDim
	switch {
	case d > 3*24*time.Hour:
		st = sErr
	case d > 24*time.Hour:
		st = lipgloss.NewStyle().Foreground(yellow)
	case d > 4*time.Hour:
		st = sText
	}
	return st.Render(age(t))
}

func (m *Model) listInbox(h int) string {
	top := m.viewBrief()
	h -= lipgloss.Height(top) - 1 // its trailing empty line is the table's first
	return top + m.table(inboxCols, m.vis.widths.inbox, len(m.vis.inboxRows), &m.cur[vInbox], h, m.inboxCells, "nobody is waiting on you")
}

func (m *Model) inboxCells(i int, sel bool) []string {
	r := m.vis.inboxRows[i]
	if r.header() {
		return m.listHeader(vInbox, r, 0)
	}
	it := m.vis.inbox[r.item]
	st := sText
	if sel {
		st = sBold
	}
	if _, snoozed := m.dismissed[it.id]; snoozed {
		st = sDim
	}
	where := sText.Render(it.where)
	if it.kind == kMention {
		where = sTicket.Render(it.where)
	}
	return []string{listIndent(r) + waiting(it.since), kindStyle(it.kind).Render(kindLabel[it.kind]), where, sDim.Render(it.who), st.Render(it.text)}
}

func (m *Model) currentInbox() (inboxItem, bool) {
	i, ok := m.listItem(vInbox)
	if !ok {
		return inboxItem{}, false
	}
	return m.vis.inbox[i], true
}

func (m *Model) detailInbox() string {
	if g, ok := m.currentGroup(vInbox); ok {
		return m.detailGroup(vInbox, g)
	}
	it, ok := m.currentInbox()
	if !ok {
		return m.panel(nil)
	}
	head := kindStyle(it.kind).Render(kindLabel[it.kind])
	if it.where != it.ticket {
		head += "  " + sBold.Render(it.where)
	}
	if it.ticket != "" {
		head += "  " + sTicket.Render(it.ticket)
		if is, ok := m.ix.Issues[it.ticket]; ok {
			head += sDim.Render(" "+is.Summary+" · ") + statusText(is)
		}
	}
	lines := []string{head, sDim.Render("waiting since " + it.since.Local().Format("Mon 2 Jan 15:04") + " · " + it.who)}
	switch it.kind {
	case kReview:
		lines = append(lines, sText.Render(it.mr.Title), pipelineText(mrRow{it.mr, false})+"  "+m.approvalText(it.mr)+"  "+m.chipsText(mrRow{it.mr, false}))
		lines = append(lines, m.teamsLines(it.mr)...)
	case kThreads:
		for _, t := range it.mr.Threads {
			if !t.Resolved {
				lines = append(lines, threadLine(t))
			}
		}
	case kClaude:
		if it.sess.Transcript != nil && it.sess.LastPrompt != "" {
			lines = append(lines, sDim.Render("› ")+sText.Render(oneLine(it.sess.LastPrompt)))
		}
	default:
		lines = append(lines, sText.Render(it.text))
	}
	return m.panel(m.withNotes(lines, it.ticket))
}

func (m *Model) keyInbox(k string) (tea.Model, tea.Cmd) {
	act := m.action(k)
	if m.keyList(act) {
		return m, nil
	}
	switch act {
	case "inbox.all":
		m.showAll = !m.showAll
		m.refilter()
		return m, nil
	case "inbox.brief":
		if err := brief.Request(); err != nil {
			m.setFlash(err.Error(), true)
		} else {
			m.briefPending = time.Now()
		}
		return m, nil
	}
	it, ok := m.currentInbox()
	if !ok {
		return m, nil
	}
	switch act {
	case "inbox.open":
		switch it.kind {
		case kClaude:
			return m.jumpTo(it.sess)
		case kReview, kThreads:
			m.seen.mark(it.mr)
		}
		return m, openURL(it.url)
	case "inbox.review":
		if it.kind == kReview {
			return m.reviewWithClaude(it.mr)
		}
	case "inbox.threads":
		if it.kind == kThreads {
			m.chooseThreads(it.mr)
		}
	case "inbox.rebase":
		if it.kind == kReview || it.kind == kThreads {
			return m.rebase(it.mr)
		}
	case "inbox.approve":
		if it.kind == kReview {
			if !m.confirm("A"+it.mr.WebURL, fmt.Sprintf("approve !%d %s", it.mr.IID, it.mr.Repo())) {
				return m, nil
			}
			mr := it.mr
			return m, func() tea.Msg {
				if err := approveMR(mr); err != nil {
					return flashMsg{err.Error(), true}
				}
				return refreshMsg(srcGitlab)
			}
		}
	case "inbox.snooze":
		m.dismissed.toggle(it.id)
		m.refilter()
	case "inbox.ticket":
		return m.gotoTicket(it.ticket)
	case "inbox.teams":
		if it.kind == kReview || it.kind == kThreads {
			return m.openTeamsPost(it.mr)
		}
	case "inbox.claude", "inbox.claude_bg":
		return m.claudeForInbox(it)
	case "inbox.plan":
		if it.kind == kClaude {
			m.openPlan(it.sess)
		}
	}
	return m, nil
}

// viewBrief is the natural-language status above the inbox list.
func (m *Model) viewBrief() string {
	head := lipgloss.NewStyle().Foreground(purple).Bold(true).Render(" ✻ brief")
	b := m.brief
	switch {
	case !m.briefPending.IsZero():
		head += sDim.Render("  " + m.spin() + " writing a fresh one…")
	case !b.At.IsZero():
		when := "written " + age(b.At) + " ago"
		if age(b.At) == "now" {
			when = "written just now"
		}
		head += sDim.Render("  " + when + "  ·  ")
		head += sKey.Render(m.key("inbox.brief")) + sDim.Render(" refresh")
	default:
		head += sDim.Render("  ") + sKey.Render(m.key("inbox.brief")) + sDim.Render(" asks Claude for a summary of what's waiting")
	}
	lines := []string{head}
	if b.Text != "" {
		body := lipgloss.NewStyle().Foreground(fg).Width(max(20, m.width-4)).Render(b.Text)
		for _, l := range strings.Split(body, "\n") {
			lines = append(lines, "   "+l)
			if len(lines) > 6 {
				break
			}
		}
	}
	if b.Err != "" {
		lines = append(lines, "   "+sErr.Render("last refresh failed: "+oneLine(b.Err)))
	}
	return strings.Join(lines, "\n") + "\n\n"
}

func (m *Model) reloadBrief() {
	st, err := os.Stat(brief.Path())
	if err != nil || !st.ModTime().After(m.briefAt) {
		return
	}
	m.briefAt = st.ModTime()
	m.brief, _ = brief.Load()
	if !m.briefPending.IsZero() && st.ModTime().After(m.briefPending) {
		m.briefPending = time.Time{}
	}
}

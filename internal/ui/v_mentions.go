package ui

import (
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MartyJRE/claudia/internal/atlassian"
)

// mention is a Jira comment or Confluence page that @-mentions you.
type mention struct {
	id       string
	where    string // ticket key or Confluence space
	title    string
	author   string
	text     string
	url      string
	at       time.Time
	answered bool
	jira     bool
}

func (m *Model) mentions() []mention {
	var out []mention
	for _, j := range m.jira.Mentions {
		out = append(out, mention{
			id: "jira:" + j.CommentID, where: j.Key, title: j.Summary, author: j.Author,
			text: j.Text, url: j.URL, at: j.Created, answered: j.Answered, jira: true,
		})
	}
	for _, p := range m.conf.Mentions {
		out = append(out, mention{
			id: "conf:" + p.ID + ":" + p.Modified.Format(time.RFC3339), where: p.Space, title: p.Title,
			text: cleanExcerpt(p.Excerpt), url: p.URL, at: p.Modified, answered: m.repliedOnPage(p),
		})
	}
	return out
}

// repliedOnPage: I commented on the mention's page after it, as with Jira.
func (m *Model) repliedOnPage(p atlassian.Page) bool {
	for _, c := range m.conf.Comments {
		if c.OnPage != "" && c.OnPage == p.OnPage && c.Modified.After(p.Modified) {
			return true
		}
	}
	return false
}

// open: a Jira mention nobody has answered yet and that wasn't dismissed.
func (m *Model) open(x mention) bool {
	_, gone := m.dismissed[x.id]
	return !gone && !x.answered
}

func (m *Model) unansweredMentions() int {
	n := 0
	for _, x := range m.mentions() {
		if x.jira && m.open(x) {
			n++
		}
	}
	return n
}

func (m *Model) unansweredFor(key string) int {
	n := 0
	for _, x := range m.mentions() {
		if x.jira && x.where == key && m.open(x) {
			n++
		}
	}
	return n
}

func (m *Model) filterMentions(match func(...string) bool) []mention {
	var out []mention
	for _, x := range m.mentions() {
		if !m.showAll && !m.open(x) {
			continue
		}
		if match(x.where, x.title, x.author, x.text) {
			out = append(out, x)
		}
	}
	slices.SortStableFunc(out, func(a, b mention) int {
		if m.open(a) != m.open(b) {
			if m.open(a) {
				return -1
			}
			return 1
		}
		return b.at.Compare(a.at)
	})
	return out
}

var mentionCols = []col{
	{"", 2, 0},
	{"WHERE", 10, 0},
	{"FROM", 18, 0},
	{"ABOUT", 34, 110},
	{"SAYS", 0, 0},
	{"AGE", 5, 0},
}

func (m *Model) listMentions(h int) string {
	rows := m.vis.mentionRows
	empty := "inbox zero — a shows handled ones"
	if m.atl == nil && m.atlErr != nil {
		empty = m.atlErr.Error()
	}
	return m.table(mentionCols, m.vis.widths.mentions, len(rows), &m.cur[vMentions], h, m.mentionCells, empty)
}

func (m *Model) mentionCells(i int, sel bool) []string {
	r := m.vis.mentionRows[i]
	if r.header() {
		return m.listHeader(vMentions, r, colStart(layout(mentionCols, m.width, m.vis.widths.mentions), 1))
	}
	x := m.vis.mentions[r.item]
	dot := lipgloss.NewStyle().Foreground(purple).Render("●")
	st := sText
	if !m.open(x) {
		dot, st = sDim.Render("○"), sDim
	} else if !x.jira {
		dot = lipgloss.NewStyle().Foreground(blue).Render("◇")
	}
	if sel {
		st = st.Bold(true)
	}
	where := sTicket.Render(x.where)
	if !x.jira {
		where = sDim.Render(x.where)
	}
	return []string{dot, listIndent(r) + where, st.Render(x.author), sDim.Render(x.title), st.Render(x.text), sDim.Render(age(x.at))}
}

func (m *Model) currentMention() (mention, bool) {
	i, ok := m.listItem(vMentions)
	if !ok {
		return mention{}, false
	}
	return m.vis.mentions[i], true
}

func (m *Model) detailMention() string {
	if g, ok := m.currentGroup(vMentions); ok {
		return m.detailGroup(vMentions, g)
	}
	x, ok := m.currentMention()
	if !ok {
		return m.panel(nil)
	}
	head := sTicket.Render(x.where) + " " + sBold.Render(x.title)
	if is, ok := m.ix.Issues[x.where]; ok {
		head += sDim.Render("  ·  ") + statusText(is)
	}
	lines := []string{head}
	by := x.author
	if by == "" {
		by = "Confluence"
	}
	state := lipgloss.NewStyle().Foreground(purple).Render("waiting for your reply")
	switch {
	case x.answered:
		state = sOK.Render("you replied after this")
	case !m.open(x):
		state = sDim.Render("dismissed")
	case !x.jira:
		state = sDim.Render("page mention")
	}
	lines = append(lines, sDim.Render(by+", "+x.at.Local().Format("Mon 2 Jan 15:04")+"  ·  ")+state)
	// Wrap the text over the remaining panel lines.
	w := max(20, m.width-8)
	words := strings.Fields(x.text)
	var cur string
	for _, wd := range words {
		if len(cur)+len(wd)+1 > w {
			lines = append(lines, sText.Render(cur))
			cur = wd
			continue
		}
		cur = strings.TrimSpace(cur + " " + wd)
	}
	if cur != "" {
		lines = append(lines, sText.Render(cur))
	}
	return m.panel(lines)
}

func (m *Model) keyMentions(k string) (tea.Model, tea.Cmd) {
	act := m.action(k)
	if m.keyList(act) {
		return m, nil
	}
	if act == "mentions.all" {
		m.showAll = !m.showAll
		m.refilter()
		return m, nil
	}
	x, ok := m.currentMention()
	if !ok {
		return m, nil
	}
	switch act {
	case "mentions.open":
		return m, openURL(x.url)
	case "mentions.dismiss":
		m.dismissed.toggle(x.id)
		m.refilter()
	case "mentions.ticket":
		if x.jira {
			return m.gotoTicket(x.where)
		}
	case "mentions.copy":
		return m, copyText(x.url, "link")
	case "mentions.reply", "mentions.reply_bg":
		return m.replyToMention(x)
	}
	return m, nil
}

func cleanExcerpt(s string) string {
	r := strings.NewReplacer("@@@hl@@@", "", "@@@endhl@@@", "", "&hellip;", "…", "&quot;", "\"", "&amp;", "&")
	return oneLine(r.Replace(s))
}

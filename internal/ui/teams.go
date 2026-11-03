package ui

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/launch"
	"github.com/MartyJRE/claudia/internal/teams"
)

func (m *Model) teamsMe() string { return getConfig().Teams.Me }

// teamsChip marks a review request that was also posted in Teams; ✓ once you've answered there.
func (m *Model) teamsChip(mr gitlab.MR) string {
	posts := m.teams.ForMR(mr.WebURL)
	if len(posts) == 0 {
		return ""
	}
	if posts[0].RepliedBy(m.teamsMe()) {
		return sDim.Render("teams ✓")
	}
	return lipgloss.NewStyle().Foreground(purple).Render("teams")
}

func (m *Model) teamsLines(mr gitlab.MR) []string {
	var out []string
	for i, p := range m.teams.ForMR(mr.WebURL) {
		if i == 2 {
			break
		}
		l := lipgloss.NewStyle().Foreground(purple).Render("teams ") + sDim.Render(p.From+" · "+age(p.At)+" ago")
		if n := len(p.Replies); n > 0 {
			l += sDim.Render(fmt.Sprintf(" · %d replies", n))
		}
		out = append(out, l)
		for _, r := range p.Replies[max(0, len(p.Replies)-3):] {
			out = append(out, sDim.Render("  "+r.From+": ")+sText.Render(oneLine(r.Text)))
		}
	}
	return out
}

func (m *Model) openTeamsPost(mr gitlab.MR) (tea.Model, tea.Cmd) {
	posts := m.teams.ForMR(mr.WebURL)
	if len(posts) == 0 || posts[0].WebURL == "" {
		m.setFlash("no Teams post for this MR", false)
		return m, nil
	}
	return m, openURL(posts[0].WebURL)
}

// teamsReplyNote tells a review session which Teams threads asked for it, one per post
// even when a post covers several of the MRs.
func (m *Model) teamsReplyNote(urls []string) string {
	var lines []string
	var seen []string
	for _, u := range urls {
		for _, p := range m.teams.ForMR(u) {
			if slices.Contains(seen, p.ID) {
				continue
			}
			seen = append(seen, p.ID)
			var covers []string
			for _, x := range p.MRURLs {
				if slices.Contains(urls, x) {
					covers = append(covers, x)
				}
			}
			lines = append(lines, fmt.Sprintf("- message %s from %s about %s", p.ID, p.From, strings.Join(covers, ", ")))
			break // only the newest post per MR
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "\n\nThe review was also requested in Teams:\n" + strings.Join(lines, "\n") +
		"\nWhen the review is done, propose a one-line reply for each of those threads (one line covering all its MRs), or just a reaction. " +
		"Only after I OK it, post it with `claudia teams reply <message id> '<text>'` or `claudia teams react <message id> ✅`."
}

// onReviewRow: a mention in a post about an MR waiting for my review already
// shows on that MR's row.
func (m *Model) onReviewRow(w teams.Waiting) bool {
	for _, u := range w.MRURLs {
		if slices.ContainsFunc(m.gl.Review, func(mr gitlab.MR) bool { return mr.WebURL == u }) {
			return true
		}
	}
	return false
}

// replyInTeams drafts an answer to a chat or @mention; nothing is sent without an OK.
func (m *Model) replyInTeams(w teams.Waiting) (tea.Model, tea.Cmd) {
	what := "wrote to me in Teams (" + w.Where + ")"
	if w.Kind == "mention" {
		what = "mentioned me in Teams, in " + w.Where
	}
	prompt := fmt.Sprintf("%s %s:\n\n> %s\n\n"+
		"Read the conversation with `claudia teams show %s` (`claudia teams download <url>` fetches a file it lists), check the code where it helps, and draft my reply: briefly, in the language they wrote in. "+
		"Only after I OK the exact text, send it with `claudia teams reply %s '<text>'` (add `--file <path>` to attach a file), or react with `claudia teams react %s 👍`.",
		w.From, what, strings.ReplaceAll(w.Text, "\n", "\n> "), w.Ref, w.Ref, w.Ref)
	m.pickRepo("reply to "+firstName(w.From)+" in", "", nil, func(m *Model, dir string, p claude.Profile) (tea.Model, tea.Cmd) {
		sp, _ := launch.New(dir, p, "reply to "+firstName(w.From), prompt)
		sp.Kind = "reply"
		return m.start(sp)
	})
	return m, nil
}

// answerInvite offers the three answers to a meeting invite.
func (m *Model) answerInvite(w teams.Waiting) {
	tm := m.tm
	var items []choice
	for _, a := range []string{"accept", "tentative", "decline"} {
		items = append(items, choice{label: a, value: a})
	}
	m.chooser = newChooser(w.Text, items, func(m *Model, ch choice) (tea.Model, tea.Cmd) {
		answer := ch.value.(string)
		return m, func() tea.Msg {
			if tm == nil {
				return flashMsg{"Teams isn't set up", true}
			}
			if err := respondTeams(tm, w.Ref, answer, ""); err != nil {
				return flashMsg{err.Error(), true}
			}
			return refreshMsg(srcTeams)
		}
	})
}

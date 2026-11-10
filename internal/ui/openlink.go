package ui

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/launch"
	"github.com/MartyJRE/claudia/internal/state"
)

// "Take a look at this": a pasted link becomes the right Claude session,
// whether or not claudia already knows the MR or ticket.

const openRequestFile = "open-request.json"

type promptBox struct {
	title  string
	hint   string
	verb   string // what enter does; "open" when empty
	in     textinput.Model
	submit func(*Model, string) (tea.Model, tea.Cmd)
}

func (m *Model) askLink() tea.Cmd {
	in := newInput("link › ", "GitLab MR, Jira ticket or any URL")
	in.Width = max(40, m.width-12)
	if clip, err := cmdOutput(exec.Command("pbpaste")); err == nil {
		if s := strings.TrimSpace(string(clip)); strings.HasPrefix(s, "http") && !strings.ContainsAny(s, " \n") {
			in.SetValue(s)
			in.CursorEnd()
		}
	}
	m.prompt = &promptBox{title: "open a link in Claude", hint: linkHint, in: in, submit: func(m *Model, s string) (tea.Model, tea.Cmd) {
		return m.openLink(s)
	}}
	return m.prompt.in.Focus()
}

func newInput(prompt, placeholder string) textinput.Model {
	in := textinput.New()
	in.Prompt = prompt
	in.PromptStyle = sKey
	in.TextStyle = sText
	in.Placeholder = placeholder
	return in
}

func (m *Model) updatePrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.prompt = nil
		return m, nil
	case "enter":
		p := m.prompt
		m.prompt = nil
		if v := strings.TrimSpace(p.in.Value()); v != "" {
			return p.submit(m, v)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.prompt.in, cmd = m.prompt.in.Update(msg)
	return m, cmd
}

const linkHint = "an MR gets reviewed, a Jira ticket gets what its state calls for, anything else: pick a repo and Claude takes a look"

func (m *Model) viewPrompt(h int) string {
	body := " " + sBold.Render(m.prompt.title) + "\n\n " + m.prompt.in.View()
	if m.prompt.hint != "" {
		body += "\n\n" + sDim.Render(" "+m.prompt.hint)
	}
	return lipgloss.NewStyle().Height(h).MaxHeight(h).Render(body)
}

type mrLoadedMsg struct {
	mr  gitlab.MR
	err error
}

var jiraKeyRe = regexp.MustCompile(`\b([A-Z][A-Z0-9]+-\d+)\b`)

// openLink routes a link: an MR is loaded and reviewed in its clone, a Jira
// ticket goes through claudeForTicket, anything else lands in a picked repo.
func (m *Model) openLink(link string) (tea.Model, tea.Cmd) {
	if project, iid, ok := gitlab.ParseMRURL(link); ok {
		for _, mr := range append(append([]gitlab.MR{}, m.gl.Review...), m.gl.Mine...) {
			if mr.Project() == project && mr.IID == iid {
				return m.reviewWithClaude(mr)
			}
		}
		m.setFlash(fmt.Sprintf("loading %s!%d…", project, iid), false)
		return m, func() tea.Msg {
			mr, err := fetchMR(project, iid)
			return mrLoadedMsg{mr, err}
		}
	}
	// Confluence lives under /wiki/; page titles there often contain KEY-123-looking words.
	if base := getConfig().AtlassianURL; base != "" && strings.HasPrefix(link, base) && !strings.Contains(link, "/wiki/") {
		if k := jiraKeyRe.FindString(link); k != "" {
			summary := ""
			if is, ok := m.ix.Issues[k]; ok {
				summary = is.Summary
			}
			return m.claudeForTicket(k, summary)
		}
	}
	m.pickRepo("take a look in", "", nil, func(m *Model, dir string, p claude.Profile) (tea.Model, tea.Cmd) {
		sp, _ := launch.New(dir, p, "look: "+shortLink(link), "Take a look at "+link)
		sp.Kind = "look"
		return m.start(sp)
	})
	return m, nil
}

func shortLink(s string) string {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
	if r := []rune(s); len(r) > 40 {
		return string(r[:39]) + "…"
	}
	return s
}

// takeOpenRequest picks up `claudia open <link>` from another shell.
func (m *Model) takeOpenRequest() (string, bool) {
	var req struct {
		Link string `json:"link"`
	}
	path := state.Path(openRequestFile)
	if !state.Load(openRequestFile, &req) {
		return "", false
	}
	os.Remove(path)
	return req.Link, req.Link != ""
}

// RequestOpen hands a link to the running claudia.
func RequestOpen(link string) error {
	return state.Save(openRequestFile, map[string]string{"link": link})
}

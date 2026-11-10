package ui

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MartyJRE/claudia/internal/launch"
	"github.com/MartyJRE/claudia/internal/repos"
)

// A repo that doesn't exist yet: personal ones go to GitHub, work ones to the
// GitLab group of the clones next to them, and Claude starts in it.

type newRepoRow string

type repoCreatedMsg struct {
	spec repos.New
	err  error
}

// offerNewRepo adds "+ new repo" under the repo chooser; a query nothing
// matches becomes the new repo's name.
func (m *Model) offerNewRepo() {
	c := m.chooser
	pick := c.pick
	c.extra = func(q string) []choice {
		label := "+ new repo…"
		if q != "" {
			label = fmt.Sprintf("+ new repo %q…", q)
		}
		return []choice{{label: sKey.Render(label), value: newRepoRow(q)}}
	}
	c.pick = func(m *Model, ch choice) (tea.Model, tea.Cmd) {
		if q, ok := ch.value.(newRepoRow); ok {
			m.pickRepoHome(string(q))
			return m, nil
		}
		return pick(m, ch)
	}
	c.filter()
}

func (m *Model) pickRepoHome(name string) {
	cfg := getConfig()
	root := repos.Root()
	items := []choice{{label: sBold.Render(cfg.PersonalDir), hint: "GitHub", value: repos.New{Dir: filepath.Join(root, cfg.PersonalDir)}}}
	groups := repos.Groups(m.repos, filepath.Join(root, cfg.WorkDir))
	for _, dir := range slices.Sorted(maps.Keys(groups)) {
		items = append(items, pathChoice(dir, root, "GitLab "+groups[dir], repos.New{Dir: dir, Group: groups[dir], GitLabHost: cfg.GitLabHost}))
	}
	m.chooser = newChooser("new repo in", items, func(m *Model, ch choice) (tea.Model, tea.Cmd) {
		return m, m.nameRepo(ch.value.(repos.New), name)
	})
}

func (m *Model) nameRepo(spec repos.New, name string) tea.Cmd {
	in := newInput("name › ", "folder and repo name")
	in.SetValue(name)
	in.CursorEnd()
	in.Width = max(40, m.width-12)
	where := "GitHub"
	if spec.Group != "" {
		where = "GitLab " + spec.Group
	}
	m.prompt = &promptBox{
		title: "new repo in " + strings.TrimPrefix(spec.Dir, repos.Root()+"/"),
		hint:  "a git repo in that folder, an empty remote on " + where + " as its origin, and Claude in it",
		verb:  "next",
		in:    in,
		submit: func(m *Model, name string) (tea.Model, tea.Cmd) {
			dir := filepath.Join(spec.Dir, name)
			switch _, err := os.Stat(dir); {
			case !repos.ValidName(name):
				m.setFlash("letters, digits, . _ and - only", true)
			case err == nil:
				m.setFlash(dir+" already exists", true)
			default:
				spec.Dir = dir
				m.pickVisibility(spec)
				return m, nil
			}
			return m, m.nameRepo(spec, name)
		},
	}
	return m.prompt.in.Focus()
}

// pickVisibility lists the host's usual choice first, so enter takes it.
func (m *Model) pickVisibility(spec repos.New) {
	vis := []choice{
		{label: "private", hint: "only members", value: "private"},
		{label: "public", hint: "anyone", value: "public"},
	}
	if spec.Group != "" {
		vis = append([]choice{{label: "internal", hint: "anyone signed in to " + spec.GitLabHost, value: "internal"}}, vis...)
	}
	m.chooser = newChooser(filepath.Base(spec.Dir)+" is", vis, func(m *Model, ch choice) (tea.Model, tea.Cmd) {
		spec.Visibility = ch.value.(string)
		spec.Branch = "main"
		if spec.Group != "" {
			spec.Branch = "master"
		}
		return m, m.nameBranch(spec)
	})
}

func (m *Model) nameBranch(spec repos.New) tea.Cmd {
	in := newInput("branch › ", "default branch")
	in.SetValue(spec.Branch)
	in.CursorEnd()
	in.Width = max(40, m.width-12)
	m.prompt = &promptBox{
		title: "default branch of " + filepath.Base(spec.Dir),
		verb:  "create",
		in:    in,
		submit: func(m *Model, branch string) (tea.Model, tea.Cmd) {
			spec.Branch = branch
			if !repos.ValidBranch(branch) {
				m.setFlash("not a branch name git takes", true)
				return m, m.nameBranch(spec)
			}
			m.setFlash("creating "+filepath.Base(spec.Dir)+"…", false)
			return m, func() tea.Msg { return repoCreatedMsg{spec, createRepo(spec)} }
		},
	}
	return m.prompt.in.Focus()
}

func (m *Model) repoCreated(msg repoCreatedMsg) (tea.Model, tea.Cmd) {
	dir := msg.spec.Dir
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil && !slices.ContainsFunc(m.repos, func(r repos.Repo) bool { return r.Dir == dir }) {
		project := ""
		if msg.spec.Group != "" {
			project = msg.spec.Group + "/" + filepath.Base(dir)
		}
		m.repos = append(m.repos, repos.Repo{Dir: dir, Branch: msg.spec.Branch, Project: project, Touched: clockNow()})
	}
	if msg.err != nil {
		m.setFlash(msg.err.Error(), true)
		return m, nil
	}
	sp, _ := launch.New(dir, m.profileFor(dir, 0), filepath.Base(dir), "")
	return m.start(sp)
}

package ui

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/launch"
	"github.com/MartyJRE/claudia/internal/repos"
)

// What `c` does depends on what's waiting: a review request gets reviewed, a
// mention gets a drafted reply, review threads get addressed, and a ticket gets
// whatever its state calls for.

const noWrites = "Don't change any files and don't write anything to Jira."

func briefRef(key, path string, err error) string {
	if err != nil {
		return fmt.Sprintf("Read the Jira issue %s with the Atlassian MCP, including its comments", key)
	}
	return fmt.Sprintf("Read the Jira issue %s exported to %s (it includes the comments; open the images it references)", key, path)
}

// claudeForTicket picks the session a ticket needs right now.
func (m *Model) claudeForTicket(key, summary string) (tea.Model, tea.Cmd) {
	for _, mr := range m.gl.Review {
		if mrTicket(mr) == key && !mr.Draft && !mr.ApprovedByMe(m.gl.Me.Username) {
			return m.reviewWithClaude(mr)
		}
	}
	for _, mr := range m.gl.Mine {
		if mrTicket(mr) == key && mr.OpenThreads() > 0 {
			return m.addressThreads(mr)
		}
	}
	is, known := m.ix.issues[key]
	status := strings.ToLower(is.Status)
	switch {
	case known && (is.Category == "new" || strings.Contains(status, "analysis")):
		return m.ticketSession(key, summary, "analyse "+key+" in", func(path string, err error) string {
			brief := path
			if err != nil {
				brief = "Jira (use the Atlassian MCP)"
			}
			return strings.NewReplacer("{key}", key, "{brief}", brief).Replace(config.Get().AnalysisPrompt)
		})
	case known && strings.Contains(status, "test"):
		return m.ticketSession(key, summary, "test plan for "+key+" in", func(path string, err error) string {
			return briefRef(key, path, err) + " and look at the changes that went in for it" + m.mrList(key) +
				". It's ready for testing: write a test plan for devel from the acceptance criteria, with concrete requests or UI steps, expected results and edge cases. " + noWrites
		})
	case known && is.Category == "indeterminate" && is.Mine:
		return m.ticketSession(key, summary, "continue "+key+" in", func(path string, err error) string {
			return briefRef(key, path, err) + ". I'm working on it. Check where the work stands: the branches for " + key +
				" in this repo and its MRs" + m.mrList(key) + ", against the acceptance criteria. Summarise what's done, what's left, and propose the next steps. Don't change files yet."
		})
	}
	return m.briefSession(key, summary)
}

// briefSession is the plain "what is this ticket about" session.
func (m *Model) briefSession(key, summary string) (tea.Model, tea.Cmd) {
	return m.ticketSession(key, summary, key+" in", func(path string, err error) string {
		return briefRef(key, path, err) + ". Then look into this repo and summarise: what needs to be done, where in the code, and what is unclear or needs a decision. " + noWrites
	})
}

func (m *Model) mrList(key string) string {
	var urls []string
	for _, mr := range m.ix.ticketMRs[key] {
		urls = append(urls, mr.WebURL)
	}
	if len(urls) == 0 {
		return ""
	}
	return " (" + strings.Join(urls, ", ") + ")"
}

// replyToMention drafts an answer to a Jira or Confluence mention.
func (m *Model) replyToMention(x mention) (tea.Model, tea.Cmd) {
	quote := "> " + strings.ReplaceAll(x.text, "\n", "\n> ")
	if !x.jira {
		prompt := fmt.Sprintf("I was mentioned on the Confluence page %q (%s). Read it with the Atlassian MCP, find where I'm mentioned and what is asked of me, check the code where it helps, and draft my reply. Don't post anything to Confluence; show me the draft.", x.title, x.url)
		m.pickRepo("reply on "+x.title+" in", "", nil, func(m *Model, dir string, p claude.Profile) (tea.Model, tea.Cmd) {
			sp, _ := launch.New(dir, p, "reply: "+x.title, prompt)
			return m.start(sp)
		})
		return m, nil
	}
	return m.ticketSession(x.where, x.title, "reply to "+x.author+" in", func(path string, err error) string {
		return fmt.Sprintf("%s mentioned me on %s (%s):\n\n%s\n\n%s, and look at the code where it helps to answer. "+
			"Then draft my reply: answer each of their points, briefly, in the language they wrote in. Don't post anything to Jira; show me the draft.",
			x.author, x.where, x.url, quote, briefRef(x.where, path, err))
	})
}

// ticketSession picks a repo (those on the ticket's branch or holding its MRs
// first) and starts claude paired with the ticket. prompt receives the exported
// brief; the export runs off the UI thread.
func (m *Model) ticketSession(key, summary, title string, prompt func(path string, err error) string) (tea.Model, tea.Cmd) {
	lower := strings.ToLower(key)
	onBranch := slices.ContainsFunc(m.repos, func(r repos.Repo) bool { return strings.Contains(strings.ToLower(r.Branch), lower) })
	mrRepo := map[string]string{}
	var names []string
	for _, mr := range m.ix.ticketMRs[key] {
		if _, ok := mrRepo[mr.Project()]; !ok {
			mrRepo[mr.Project()] = fmt.Sprintf("!%d", mr.IID)
			names = append(names, mr.Repo())
		}
	}
	query := ""
	if !onBranch && len(names) == 1 {
		query = names[0]
	}
	m.pickRepo(title, query, func(r repos.Repo) (bool, string) {
		if strings.Contains(strings.ToLower(r.Branch), lower) {
			return true, "on " + r.Branch
		}
		if iid, ok := mrRepo[r.Project]; ok {
			return true, iid + " · " + r.Branch
		}
		return false, ""
	}, func(m *Model, dir string, p claude.Profile) (tea.Model, tea.Cmd) {
		name := key + " " + summary
		atl := m.atl
		return m, func() tea.Msg {
			path, err := "", error(nil)
			if atl != nil {
				path, err = atl.ExportBrief(key, briefDir())
			} else {
				err = m.atlErr
			}
			sp, id := launch.New(dir, p, name, prompt(path, err))
			_ = m.links.SetTicket(id, key)
			return startMsg{sp}
		}
	})
	return m, nil
}

// reviewWithClaude runs the review-mr skill on the MR together with the related
// MRs of the same change.
func (m *Model) reviewWithClaude(mr gitlab.MR) (tea.Model, tea.Cmd) {
	rel := related(mr, m.gl)
	var urls []string
	for _, o := range rel {
		urls = append(urls, o.WebURL)
	}
	m.seen.mark(rel...)
	name := fmt.Sprintf("review %s!%d", mr.Repo(), mr.IID)
	if t := mrTicket(mr); t != "" {
		name = "review " + t
	}
	prompt := strings.ReplaceAll(config.Get().ReviewPrompt, "{urls}", strings.Join(urls, " "))
	return m.inClone(mr, "review", name, prompt, urls)
}

// addressThreads works through the open review remarks on one of my MRs.
func (m *Model) addressThreads(mr gitlab.MR) (tea.Model, tea.Cmd) {
	m.seen.mark(mr)
	prompt := fmt.Sprintf("Address the unresolved review threads on my MR %s (branch %s). If this clone isn't on %s, check it out first, and stop and ask if the working tree has changes. "+
		"Read the threads with `glab api projects/%d/merge_requests/%d/discussions`. For each one, decide whether the remark is right: if it is, fix it on this branch; if not, draft a reply explaining why. "+
		"Then summarise the changes and the replies you propose. Don't push, post replies or resolve threads until I confirm.",
		mr.WebURL, mr.SourceBranch, mr.SourceBranch, mr.ProjectID, mr.IID)
	return m.inClone(mr, "fix", fmt.Sprintf("threads %s!%d", mr.Repo(), mr.IID), prompt, []string{mr.WebURL})
}

// inClone starts claude in a local clone of the MR's project, asking which one
// when there are several and falling back to the repo picker when there is none.
func (m *Model) inClone(mr gitlab.MR, verb, name, prompt string, urls []string) (tea.Model, tea.Cmd) {
	launchIn := func(m *Model, dir string) (tea.Model, tea.Cmd) {
		sp, id := launch.New(dir, launch.WorkProfile(m.store.Profiles), name, prompt)
		if err := m.links.AddMRs(id, urls); err != nil {
			m.setFlash(err.Error(), true)
		}
		if t := mrTicket(mr); t != "" {
			_ = m.links.SetTicket(id, t)
		}
		return m.start(sp)
	}
	clones := repos.Clones(m.repos, mr.Project(), mr.SourceBranch)
	switch len(clones) {
	case 0:
		m.setFlash("no local clone of "+mr.Project()+" — pick where to run it", false)
		m.pickRepo(verb+" "+mr.Repo()+" in", mr.Repo(), nil, func(m *Model, dir string, _ claude.Profile) (tea.Model, tea.Cmd) {
			return launchIn(m, dir)
		})
		return m, nil
	case 1:
		return launchIn(m, clones[0].Dir)
	}
	var items []choice
	for _, c := range clones {
		items = append(items, pathChoice(c.Dir, repos.Root(), c.Branch, c.Dir))
	}
	m.chooser = newChooser(verb+" in clone", items, func(m *Model, ch choice) (tea.Model, tea.Cmd) {
		return launchIn(m, ch.value.(string))
	})
	return m, nil
}

// claudeForInbox: the session each kind of waiting item needs.
func (m *Model) claudeForInbox(it inboxItem) (tea.Model, tea.Cmd) {
	switch it.kind {
	case kReview:
		return m.reviewWithClaude(it.mr)
	case kThreads:
		return m.addressThreads(it.mr)
	case kMention, kPage:
		return m.replyToMention(it.ment)
	case kClaude:
		return m.jumpTo(it.sess)
	}
	return m, nil
}

package ui

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MartyJRE/claudia/internal/autopilot"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/launch"
	"github.com/MartyJRE/claudia/internal/repos"
	"github.com/MartyJRE/claudia/internal/steps"
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

// intent is one kind of session claudia can start on a ticket.
type intent struct {
	id, label string
	start     func(m *Model) (tea.Model, tea.Cmd)
}

// claudeForTicket offers the sessions that fit the ticket, with the cursor on
// the one its state calls for, so enter does what fits.
func (m *Model) claudeForTicket(key, summary string) (tea.Model, tea.Cmd) {
	is, known := m.ix.Issues[key]
	status := strings.ToLower(is.Status)
	var review, threads *gitlab.MR
	for _, mr := range m.gl.Review {
		if mrTicket(mr) == key && review == nil {
			review = &mr
		}
	}
	for _, mr := range m.gl.Mine {
		if mrTicket(mr) != key {
			continue
		}
		if mr.OpenThreads() > 0 && threads == nil {
			threads = &mr
		}
		if review == nil {
			review = &mr
		}
	}

	intents := []intent{
		{"analysis", "Analysis", func(m *Model) (tea.Model, tea.Cmd) { return m.stepSession("plan", key, summary) }},
		{"implementation", "Implementation", func(m *Model) (tea.Model, tea.Cmd) { return m.stepSession("implement", key, summary) }},
		{"general", "General", func(m *Model) (tea.Model, tea.Cmd) { return m.briefSession(key, summary) }},
		{"continue", "Continue", func(m *Model) (tea.Model, tea.Cmd) {
			return m.ticketSession(key, summary, "continue", "continue", "continue "+key+" in", func(path string, err error) string {
				return briefRef(key, path, err) + ". I'm working on it. Check where the work stands: the branches for " + key +
					" in this repo and its MRs" + m.mrList(key) + ", against the acceptance criteria. Summarise what's done, what's left, and propose the next steps. Don't change files yet."
			})
		}},
		{"test", "Test plan", func(m *Model) (tea.Model, tea.Cmd) {
			return m.ticketSession(key, summary, "test", "test plan", "test plan for "+key+" in", func(path string, err error) string {
				return briefRef(key, path, err) + " and look at the changes that went in for it" + m.mrList(key) +
					". It's ready for testing: write a test plan for devel from the acceptance criteria, with concrete requests or UI steps, expected results and edge cases. " + noWrites
			})
		}},
	}
	if review != nil {
		mr := *review
		intents = append(intents, intent{"review", fmt.Sprintf("Review !%d %s", mr.IID, mr.Repo()), func(m *Model) (tea.Model, tea.Cmd) { return m.reviewWithClaude(mr) }})
	}
	if threads != nil {
		mr := *threads
		intents = append(intents, intent{"fix", fmt.Sprintf("Fix threads on !%d %s", mr.IID, mr.Repo()), func(m *Model) (tea.Model, tea.Cmd) { return m.addressThreads(mr) }})
	}

	fits := "general"
	pending := false
	for _, mr := range m.gl.Review {
		if mrTicket(mr) == key && !mr.Draft && !mr.ApprovedByMe(m.gl.Me.Username) {
			pending = true
		}
	}
	switch {
	case pending:
		fits = "review"
	case threads != nil:
		fits = "fix"
	case known && (is.Category == "new" || strings.Contains(status, "analysis")):
		fits = "analysis"
	case known && strings.Contains(status, "test"):
		fits = "test"
	case known && is.Category == "indeterminate" && is.Mine:
		fits = "continue"
	}

	var items []choice
	for _, it := range intents {
		hint := ""
		if it.id == fits {
			hint = "fits the ticket's state"
		}
		items = append(items, choice{label: it.label, hint: hint, search: it.label, value: it})
	}
	m.chooser = newChooser("session for "+key, items, func(m *Model, ch choice) (tea.Model, tea.Cmd) {
		return ch.value.(intent).start(m)
	})
	m.chooser.cur.pos = max(0, slices.IndexFunc(intents, func(it intent) bool { return it.id == fits }))
	return m, nil
}

var fillBrief = steps.FillBrief

// briefSession is the plain "what is this ticket about" session.
func (m *Model) briefSession(key, summary string) (tea.Model, tea.Cmd) {
	return m.ticketSession(key, summary, "general", "about", key+" in", func(path string, err error) string {
		return briefRef(key, path, err) + ". Then look into this repo and summarise: what needs to be done, where in the code, and what is unclear or needs a decision. " + noWrites
	})
}

func (m *Model) mrList(key string) string {
	var urls []string
	for _, mr := range m.ix.TicketMRs[key] {
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
			sp.Kind = "reply"
			return m.start(sp)
		})
		return m, nil
	}
	return m.ticketSession(x.where, x.title, "reply", "reply to "+firstName(x.author), "reply to "+x.author+" in", func(path string, err error) string {
		return fmt.Sprintf("%s mentioned me on %s (%s):\n\n%s\n\n%s, and look at the code where it helps to answer. "+
			"Then draft my reply: answer each of their points, briefly, in the language they wrote in. Don't post anything to Jira; show me the draft.",
			x.author, x.where, x.url, quote, briefRef(x.where, path, err))
	})
}

// ticketSession picks a repo (those on the ticket's branch or holding its MRs
// first) and starts claude paired with the ticket. prompt receives the exported
// brief; the export runs off the UI thread.
func (m *Model) ticketSession(key, summary, intent, verb, title string, prompt func(path string, err error) string) (tea.Model, tea.Cmd) {
	lower := strings.ToLower(key)
	onBranch := slices.ContainsFunc(m.repos, func(r repos.Repo) bool { return strings.Contains(strings.ToLower(r.Branch), lower) })
	mrRepo := map[string]string{}
	var names []string
	for _, mr := range m.ix.TicketMRs[key] {
		if _, ok := mrRepo[mr.Project()]; !ok {
			mrRepo[mr.Project()] = fmt.Sprintf("!%d", mr.IID)
			names = append(names, mr.Repo())
		}
	}
	start := m.ticketLaunch(key, summary, intent, verb, prompt)
	query := ""
	if !onBranch && len(names) == 1 {
		query = names[0]
	}
	// A ticket is work, whatever repo it lands in.
	m.pickWork = true
	m.pickRepo(title, query, func(r repos.Repo) (bool, string) {
		if strings.Contains(strings.ToLower(r.Branch), lower) {
			return true, "on " + r.Branch
		}
		if iid, ok := mrRepo[r.Project]; ok {
			return true, iid + " · " + r.Branch
		}
		return false, ""
	}, start)
	return m, nil
}

// ticketLaunch starts the ticket's session in the dir it's given, as the run
// s is starting if there is one.
func (m *Model) ticketLaunch(key, summary, intent, verb string, prompt func(path string, err error) string) func(*Model, string, claude.Profile) (tea.Model, tea.Cmd) {
	pending := m.takeStep()
	return func(m *Model, dir string, p claude.Profile) (tea.Model, tea.Cmd) {
		name := m.sessionTitle(intent, verb, key, summary)
		atl, bg, db := m.atl, m.background, m.db
		if pending != nil && db != nil {
			_ = db.Set(autopilot.RepoKey(key), dir)
		}
		return m, func() tea.Msg {
			path, err := "", error(nil)
			if atl != nil {
				path, err = exportJiraBrief(atl, key, briefDir())
			} else {
				err = m.atlErr
			}
			sp, id := launch.New(dir, p, name, prompt(path, err))
			sp.Kind = intent
			sp, runErr := startRun(db, sp, id, pending)
			_ = m.links.SetTicket(id, key)
			return startMsg{sp, bg, runErr}
		}
	}
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
	name := m.sessionTitle("review", "review", mrTicket(mr), mr.Title)
	prompt := steps.ReviewPrompt(getConfig(), urls) + m.teamsReplyNote(urls)
	return m.inClone(mr, "review", name, prompt, urls)
}

// addressThreads works through the open review remarks on one of my MRs.
func (m *Model) addressThreads(mr gitlab.MR) (tea.Model, tea.Cmd) {
	m.seen.mark(mr)
	prompt := steps.ThreadsPrompt(mr)
	return m.inClone(mr, "fix", m.sessionTitle("fix", "fix threads", mrTicket(mr), mr.Title), prompt, []string{mr.WebURL})
}

// inClone starts claude in a local clone of the MR's project, asking which one
// when there are several and falling back to the repo picker when there is none.
func (m *Model) inClone(mr gitlab.MR, verb, name, prompt string, urls []string) (tea.Model, tea.Cmd) {
	pending := m.takeStep()
	launchIn := func(m *Model, dir string) (tea.Model, tea.Cmd) {
		sp, id := launch.New(dir, launch.ProfileFor(dir, m.store.Profiles), name, prompt)
		sp.Kind = verb
		sp, err := startRun(m.db, sp, id, pending)
		if err != nil {
			m.setFlash("run log: "+err.Error(), true)
		}
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
	case kTeams:
		return m.replyInTeams(it.tw)
	case kInvite:
		m.answerInvite(it.tw)
	case kMail:
		return m.replyToMail(it.tw)
	}
	return m, nil
}

// sessionTitle names a session after what it's about rather than its ticket key.
func (m *Model) sessionTitle(intent, verb, key, fallback string) string {
	return steps.Title(getConfig(), m.ix.Issues, intent, verb, key, fallback)
}

func firstName(s string) string {
	s, _, _ = strings.Cut(s, " ")
	s, _, _ = strings.Cut(s, ".")
	return s
}

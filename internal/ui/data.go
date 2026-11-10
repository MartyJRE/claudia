package ui

import (
	"errors"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/launch"
	"github.com/MartyJRE/claudia/internal/repos"
	"github.com/MartyJRE/claudia/internal/runs"
	"github.com/MartyJRE/claudia/internal/state"
	"github.com/MartyJRE/claudia/internal/summary"
	"github.com/MartyJRE/claudia/internal/teams"
	"github.com/MartyJRE/claudia/internal/tmux"
)

type source int

const (
	srcGitlab source = iota
	srcJira
	srcConfluence
	srcRepos
	srcTeams
	nSources
)

var sourceEvery = [nSources]time.Duration{
	srcGitlab:     time.Minute,
	srcJira:       2 * time.Minute,
	srcConfluence: 5 * time.Minute,
	srcRepos:      time.Minute,
	srcTeams:      3 * time.Minute,
}

var cacheFile = [nSources]string{
	srcGitlab:     "cache-gitlab.json",
	srcJira:       "cache-jira.json",
	srcConfluence: "cache-confluence.json",
	srcTeams:      "cache-teams.json",
}

type fetched struct {
	src  source
	data any
	err  error
}

type sessionsMsg []row

// stuckRows are sessions claudia started whose claude runs but never
// registered, most likely held by a dialog in their window: shown as waiting
// on you so they aren't lost, wherever they were started from.
func stuckRows(rows []row, procs map[string]claude.Proc, kinds map[string]string, loc *tmux.Locator) []row {
	known := map[string]bool{}
	for _, r := range rows {
		known[r.ID] = true
	}
	var out []row
	for id, p := range procs {
		if known[id] || kinds[id] == "" || p.Age < runs.Grace {
			continue
		}
		r := row{Session: claude.Session{ID: id, Live: &claude.Live{PID: p.PID, SessionID: id, Name: p.Name, Status: claude.StatusWaiting, Starting: true}}, kind: kinds[id]}
		if pane, ok := locatePane(loc, p.PID); ok {
			r.pane = &pane
		}
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b row) int { return strings.Compare(a.ID, b.ID) })
	return out
}

func (m *Model) loadSessions() tea.Cmd {
	store, db := m.store, m.db
	return func() tea.Msg {
		loc := newLocator()
		sums := summary.Load()
		kinds := launch.Kinds()
		var rows []row
		for _, s := range listSessions(store) {
			r := row{Session: s, summary: sums[s.ID].Text, summaryFor: sums[s.ID].For, kind: kinds[s.ID]}
			if s.Live != nil {
				if p, ok := locatePane(loc, s.Live.PID); ok {
					r.pane = &p
				} else {
					r.host = locateHost(loc, s.Live.PID)
				}
			}
			rows = append(rows, r)
		}
		procs := processSessions()
		rows = append(rows, stuckRows(rows, procs, kinds, loc)...)
		if db != nil {
			seen := make([]runs.Seen, len(rows))
			for i, r := range rows {
				seen[i] = runs.SeenOf(r.Session, r.summary, r.summaryFor)
			}
			_, _ = db.Reconcile(runs.WithStarting(seen, procs), clockNow())
		}
		return sessionsMsg(rows)
	}
}

func (m *Model) fetch(src source) tea.Cmd {
	atl, atlErr, tm := m.atl, m.atlErr, m.tm
	return func() tea.Msg {
		f := fetched{src: src}
		switch src {
		case srcGitlab:
			f.data, f.err = fetchGitlab()
		case srcJira:
			if atl == nil {
				return fetched{src, nil, atlErr}
			}
			f.data, f.err = fetchJira(atl)
		case srcConfluence:
			if atl == nil {
				return fetched{src, nil, atlErr}
			}
			f.data, f.err = fetchConfluence(atl)
		case srcRepos:
			f.data = findRepos(repos.Root())
		case srcTeams:
			f.data, f.err = fetchTeams(tm)
			if errors.Is(f.err, teams.ErrNoChannel) {
				f.err = nil // DMs and mentions still work without the review channel
			}
		}
		if f.err == nil && cacheFile[src] != "" {
			_ = state.Save(cacheFile[src], f.data)
		}
		return f
	}
}

// loadCaches shows the last known data immediately, so the popup is useful
// before the first fetch returns.
func (m *Model) loadCaches() {
	if state.Load(cacheFile[srcGitlab], &m.gl) {
		m.fetchedAt[srcGitlab] = m.gl.FetchedAt
	}
	if state.Load(cacheFile[srcJira], &m.jira) {
		m.fetchedAt[srcJira] = m.jira.FetchedAt
	}
	if state.Load(cacheFile[srcConfluence], &m.conf) {
		m.fetchedAt[srcConfluence] = m.conf.FetchedAt
	}
	if state.Load(cacheFile[srcTeams], &m.teams) {
		m.fetchedAt[srcTeams] = m.teams.FetchedAt
	}
	state.Load(postMRsFile, &m.postMRs)
}

func (m *Model) stale(src source) bool {
	if src == srcTeams && m.tm == nil {
		return false
	}
	return !m.busy[src] && time.Since(m.fetchedAt[src]) >= sourceEvery[src]
}

func (m *Model) applyFetched(f fetched) {
	m.busy[f.src] = false
	m.errs[f.src] = f.err
	if f.err != nil {
		m.fetchedAt[f.src] = time.Now() // back off until the next interval
		return
	}
	m.fetchedAt[f.src] = time.Now()
	switch d := f.data.(type) {
	case gitlab.Snapshot:
		m.gl = d
		if !m.seenExisted {
			m.seen.mark(append(append([]gitlab.MR{}, d.Review...), d.Mine...)...)
			m.seenExisted = true
		}
	case atlassian.JiraSnapshot:
		m.jira = d
	case atlassian.ConfluenceSnapshot:
		m.conf = d
	case []repos.Repo:
		m.repos = d
	case teams.Snapshot:
		if d.MailErr != "" && d.MailErr != m.teams.MailErr {
			m.setFlash("Outlook mail: "+d.MailErr, true)
		}
		m.teams = d
	}
}

const dismissedFile = "dismissed.json"

type dismissed map[string]time.Time

func loadDismissed() dismissed {
	d := dismissed{}
	state.Load(dismissedFile, &d)
	return d
}

func (d dismissed) toggle(id string) {
	if _, ok := d[id]; ok {
		delete(d, id)
	} else {
		d[id] = time.Now()
	}
	_ = state.Save(dismissedFile, d)
}

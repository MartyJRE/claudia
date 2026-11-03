package ui

import (
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/repos"
	"github.com/MartyJRE/claudia/internal/state"
	"github.com/MartyJRE/claudia/internal/teams"
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

func (m *Model) loadSessions() tea.Cmd {
	store := m.store
	return func() tea.Msg {
		loc := newLocator()
		var rows []row
		for _, s := range listSessions(store) {
			r := row{Session: s}
			if s.Live != nil {
				if p, ok := locatePane(loc, s.Live.PID); ok {
					r.pane = &p
				} else {
					r.host = locateHost(loc, s.Live.PID)
				}
			}
			rows = append(rows, r)
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

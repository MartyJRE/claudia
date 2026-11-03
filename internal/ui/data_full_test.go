package ui

import (
	"errors"
	"testing"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/repos"
	"github.com/MartyJRE/claudia/internal/state"
)

func TestFetchGitlabCachesFull(t *testing.T) {
	m := newModelShell(t)
	fake(t, &fetchGitlab, func() (gitlab.Snapshot, error) { return gitlab.Snapshot{Me: gitlab.User{Username: "me"}}, nil })
	f := m.fetch(srcGitlab)().(fetched)
	if snap, ok := f.data.(gitlab.Snapshot); f.src != srcGitlab || f.err != nil || !ok || snap.Me.Username != "me" {
		t.Errorf("fetched: %+v", f)
	}
	var cached gitlab.Snapshot
	if !state.Load(cacheFile[srcGitlab], &cached) || cached.Me.Username != "me" {
		t.Errorf("a good fetch is cached: %+v", cached)
	}

	fake(t, &fetchGitlab, func() (gitlab.Snapshot, error) {
		return gitlab.Snapshot{Me: gitlab.User{Username: "other"}}, errors.New("401")
	})
	if f := m.fetch(srcGitlab)().(fetched); f.err == nil || f.err.Error() != "401" {
		t.Errorf("errors come back: %+v", f)
	}
	if !state.Load(cacheFile[srcGitlab], &cached) || cached.Me.Username != "me" {
		t.Errorf("a failed fetch keeps the old cache: %+v", cached)
	}
}

func TestFetchAtlassianAndReposFull(t *testing.T) {
	m := newModelShell(t)
	m.atl = &atlassian.Client{Base: "https://x"}
	var jiraBy, confBy *atlassian.Client
	fake(t, &fetchJira, func(c *atlassian.Client) (atlassian.JiraSnapshot, error) {
		jiraBy = c
		return atlassian.JiraSnapshot{Sprint: []atlassian.Issue{{Key: "ABC-9"}}}, nil
	})
	fake(t, &fetchConfluence, func(c *atlassian.Client) (atlassian.ConfluenceSnapshot, error) {
		confBy = c
		return atlassian.ConfluenceSnapshot{}, errors.New("503")
	})
	if f := m.fetch(srcJira)().(fetched); jiraBy != m.atl || f.err != nil || len(f.data.(atlassian.JiraSnapshot).Sprint) != 1 {
		t.Errorf("jira through the model's client: %+v", f)
	}
	var cached atlassian.JiraSnapshot
	if !state.Load(cacheFile[srcJira], &cached) || cached.Sprint[0].Key != "ABC-9" {
		t.Errorf("jira cached: %+v", cached)
	}
	if f := m.fetch(srcConfluence)().(fetched); confBy != m.atl || f.err == nil {
		t.Errorf("confluence error: %+v", f)
	}

	var root string
	fake(t, &findRepos, func(r string) []repos.Repo {
		root = r
		return []repos.Repo{{Dir: r + "/a"}}
	})
	f := m.fetch(srcRepos)().(fetched)
	if rs, ok := f.data.([]repos.Repo); !ok || len(rs) != 1 || root != repos.Root() {
		t.Errorf("repos are found under the code root %q: %+v", root, f)
	}
}

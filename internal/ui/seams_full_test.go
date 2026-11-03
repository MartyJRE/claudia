package ui

import (
	"errors"
	"os/exec"
	"strings"
	"syscall"
	"testing"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/launch"
	"github.com/MartyJRE/claudia/internal/repos"
	"github.com/MartyJRE/claudia/internal/tmux"
	"github.com/MartyJRE/claudia/internal/update"
)

var errNoProcess = errors.New("no processes in tests")

// stubOS keeps every test off the machine: commands fail, tmux has no panes,
// and anything with real side effects panics until a test fakes it.
func stubOS() {
	cmdOutput = func(*exec.Cmd) ([]byte, error) { return nil, errNoProcess }
	cmdCombinedOutput = func(*exec.Cmd) ([]byte, error) { return nil, errNoProcess }
	cmdRun = func(*exec.Cmd) error { return errNoProcess }
	newLocator = func() *tmux.Locator { return &tmux.Locator{} }

	startSession = func(launch.Spec) (string, error) { panic("launch.Start not faked") }
	switchClient = func(string) error { panic("tmux.Switch not faked") }
	joinSplit = func([]tmux.Pane) (string, error) { panic("tmux.JoinSplit not faked") }
	unsplitWindow = func(string) error { panic("tmux.Unsplit not faked") }
	killProcess = func(int, syscall.Signal) error { panic("kill not faked") }

	fetchGitlab = func() (gitlab.Snapshot, error) { panic("gitlab.Fetch not faked") }
	fetchMR = func(string, int) (gitlab.MR, error) { panic("gitlab.FetchMR not faked") }
	approveMR = func(gitlab.MR) error { panic("gitlab.Approve not faked") }
	unapproveMR = func(gitlab.MR) error { panic("gitlab.Unapprove not faked") }
	rebaseMR = func(gitlab.MR) error { panic("gitlab.Rebase not faked") }
	findRepos = func(string) []repos.Repo { panic("repos.Find not faked") }

	fetchJira = func(*atlassian.Client) (atlassian.JiraSnapshot, error) { panic("FetchJira not faked") }
	fetchConfluence = func(*atlassian.Client) (atlassian.ConfluenceSnapshot, error) { panic("FetchConfluence not faked") }
	exportJiraBrief = func(*atlassian.Client, string, string) (string, error) { panic("ExportBrief not faked") }
	jiraTransitions = func(*atlassian.Client, string) ([]atlassian.Transition, error) { panic("Transitions not faked") }
	transitionIssue = func(*atlassian.Client, string, string) error { panic("Transition not faked") }

	fetchUpdate = func() (update.Plan, error) { panic("update.Fetch not faked") }
	installUpdate = func(func(string)) error { panic("update.Install not faked") }
	stopWatcher = func() { panic("watch.Stop not faked") }
}

// fake swaps a seam for the length of the test.
func fake[T any](t *testing.T, seam *T, v T) {
	t.Helper()
	old := *seam
	*seam = v
	t.Cleanup(func() { *seam = old })
}

// cmdLine is a command as the shell would show it.
func cmdLine(c *exec.Cmd) string { return strings.Join(c.Args, " ") }

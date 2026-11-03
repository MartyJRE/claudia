package ui

import (
	"os"
	"os/exec"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/launch"
	"github.com/MartyJRE/claudia/internal/repos"
	"github.com/MartyJRE/claudia/internal/tmux"
	"github.com/MartyJRE/claudia/internal/update"
	"github.com/MartyJRE/claudia/internal/watch"
)

// Everything that leaves the process goes through these, so tests can stand in for it.
var (
	getConfig = config.Get

	startSession  = launch.Start
	switchClient  = tmux.Switch
	joinSplit     = tmux.JoinSplit
	unsplitWindow = tmux.Unsplit
	newLocator    = tmux.NewLocator
	locatePane    = (*tmux.Locator).Pane
	locateHost    = (*tmux.Locator).Host
	listSessions  = (*claude.Store).Sessions
	scanUsage     = (*claude.UsageScanner).Scan
	killProcess   = syscall.Kill

	cmdOutput         = (*exec.Cmd).Output
	cmdCombinedOutput = (*exec.Cmd).CombinedOutput
	cmdRun            = (*exec.Cmd).Run
	execProcess       = tea.ExecProcess
	executable        = os.Executable
	tickAfter         = tea.Tick

	fetchGitlab = gitlab.Fetch
	fetchMR     = gitlab.FetchMR
	approveMR   = gitlab.Approve
	unapproveMR = gitlab.Unapprove
	rebaseMR    = gitlab.Rebase
	findRepos   = repos.Find

	fetchJira       = (*atlassian.Client).FetchJira
	fetchConfluence = (*atlassian.Client).FetchConfluence
	exportJiraBrief = (*atlassian.Client).ExportBrief
	jiraTransitions = (*atlassian.Client).Transitions
	transitionIssue = (*atlassian.Client).Transition

	updateAvailable = update.Available
	fetchUpdate     = update.Fetch
	installUpdate   = update.Install
	stopWatcher     = watch.Stop
)

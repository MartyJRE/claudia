// Package steps is how a Board step becomes a session: its prompt, its name,
// the repo it runs in. The TUI's s and the autopilot both start steps through it.
package steps

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/launch"
	"github.com/MartyJRE/claudia/internal/repos"
)

// Session is how a ticket step starts its session: the intent, the verb in
// its title and the configured prompt.
type Session struct {
	Intent, Verb string
	Prompt       func(config.Config) string
}

var Ticket = map[string]Session{
	"readiness": {"readiness", "check", func(c config.Config) string { return c.ReadinessPrompt }},
	"plan":      {"analysis", "analyse", func(c config.Config) string { return c.AnalysisPrompt }},
	"implement": {"implementation", "implement", func(c config.Config) string { return c.ImplementationPrompt }},
	"mr":        {"mr", "open MR", func(c config.Config) string { return c.MRPrompt }},
	"qa":        {"qa", "QA notes", func(c config.Config) string { return c.QAPrompt }},
}

// FillBrief puts the ticket and where its brief was exported into a prompt.
func FillBrief(prompt, key, path string, err error) string {
	brief := path
	if err != nil {
		brief = "Jira (use the Atlassian MCP)"
	}
	return strings.NewReplacer("{key}", key, "{brief}", brief).Replace(prompt)
}

// ReviewPrompt is the configured review prompt over the MRs of one change.
func ReviewPrompt(c config.Config, urls []string) string {
	return strings.ReplaceAll(c.ReviewPrompt, "{urls}", strings.Join(urls, " "))
}

// ThreadsPrompt works through the open review remarks on one of my MRs.
func ThreadsPrompt(mr gitlab.MR) string {
	return fmt.Sprintf("Address the unresolved review threads on my MR %s (branch %s). If this clone isn't on %s, check it out first, and stop and ask if the working tree has changes. "+
		"Read the threads with `glab api projects/%d/merge_requests/%d/discussions`. For each one, decide whether the remark is right: if it is, fix it on this branch; if not, draft a reply explaining why. "+
		"Then summarise the changes and the replies you propose. Don't push, post replies or resolve threads until I confirm.",
		mr.WebURL, mr.SourceBranch, mr.SourceBranch, mr.ProjectID, mr.IID)
}

var commitPrefix = regexp.MustCompile(`^(?i)(draft:\s*)?([a-z]+(\([^)]*\))?!?:\s*)?`)

// Title names a session after what it's about rather than its ticket key:
// "review: Expose gender on suggestions". The ticket's summary wins; else the
// MR title without its "feat(KEY):" prefix. session_names overrides the shape
// per intent.
func Title(c config.Config, issues map[string]atlassian.Issue, intent, verb, key, fallback string) string {
	about := commitPrefix.ReplaceAllString(strings.TrimSpace(fallback), "")
	if is, ok := issues[key]; ok && is.Summary != "" {
		about = is.Summary
	}
	if about == "" {
		about = key
	}
	if r := []rune(about); len(r) > 50 {
		about = strings.TrimSpace(string(r[:49])) + "…"
	}
	if tpl := c.SessionNames[intent]; tpl != "" {
		name := strings.NewReplacer("{key}", key, "{summary}", about, "{verb}", verb).Replace(tpl)
		if name = strings.Trim(name, " -:_"); name != "" {
			return name
		}
	}
	return verb + ": " + about
}

// TicketRepo is the repo a ticket's step runs in without asking: the one on a
// branch named after the ticket, else the one picked for it before, else the
// single clone of the project its MRs are in. Several of either is a question.
func TicketRepo(all []repos.Repo, key string, mrs []gitlab.MR, picked string) (repos.Repo, bool) {
	lower := strings.ToLower(key)
	var onBranch []repos.Repo
	for _, r := range all {
		if strings.Contains(strings.ToLower(r.Branch), lower) {
			onBranch = append(onBranch, r)
		}
	}
	switch len(onBranch) {
	case 1:
		return onBranch[0], true
	case 0:
	default:
		return repos.Repo{}, false
	}
	for _, r := range all {
		if picked != "" && r.Dir == picked {
			return r, true
		}
	}
	projects := map[string]bool{}
	for _, mr := range mrs {
		projects[mr.Project()] = true
	}
	if len(projects) != 1 {
		return repos.Repo{}, false
	}
	if cl := repos.Clones(all, mrs[0].Project(), mrs[0].SourceBranch); len(cl) == 1 {
		return cl[0], true
	}
	return repos.Repo{}, false
}

// ReviewClone is the clone a review or feedback run uses without asking: the
// one on the MR's branch, or the only one.
func ReviewClone(all []repos.Repo, mr gitlab.MR) (repos.Repo, bool) {
	cl := repos.Clones(all, mr.Project(), mr.SourceBranch)
	if len(cl) == 1 || len(cl) > 1 && cl[0].Branch == mr.SourceBranch {
		return cl[0], true
	}
	return repos.Repo{}, false
}

// Anywhere is where a step that needs no particular repo runs: the work dir
// under the code root, so Claude can still look around the code.
func Anywhere(c config.Config) string {
	if dir := filepath.Join(c.CodeRoot, c.WorkDir); isDir(dir) {
		return dir
	}
	return c.CodeRoot
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

var gitStatus = func(dir string) ([]byte, error) {
	return exec.Command("git", "-C", dir, "status", "--porcelain").Output()
}

// Dirty: the working tree has changes, or git couldn't say.
func Dirty(dir string) bool {
	out, err := gitStatus(dir)
	return err != nil || len(strings.TrimSpace(string(out))) > 0
}

// Color is the /color least used by the sessions running side by side.
func Color(used map[string]int) string {
	best := launch.Colors[0]
	for _, c := range launch.Colors {
		if used[c] < used[best] {
			best = c
		}
	}
	return best
}

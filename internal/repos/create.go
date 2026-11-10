package repos

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// New is a repo to create: a GitHub one when Group is empty, else a GitLab
// one in that group.
type New struct {
	Dir        string
	Group      string
	GitLabHost string
	Visibility string // private, internal or public
	Branch     string
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]*$`)

// ValidName says whether name works as both a folder and a GitHub/GitLab repo name.
func ValidName(name string) bool { return nameRe.MatchString(name) && !strings.HasSuffix(name, ".git") }

var branchRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._/-]*$`)

// ValidBranch rejects what git would, short of asking it.
func ValidBranch(b string) bool {
	return branchRe.MatchString(b) && !strings.Contains(b, "..") && !strings.Contains(b, "//") &&
		!strings.HasSuffix(b, "/") && !strings.HasSuffix(b, ".") && !strings.HasSuffix(b, ".lock")
}

// Create makes Dir a git repo whose origin is a new, empty remote. The local
// repo stays when the remote can't be made.
func Create(n New) error {
	if err := os.MkdirAll(n.Dir, 0o755); err != nil {
		return err
	}
	if err := runIn(n.Dir, nil, "git", "init", "-b", n.Branch); err != nil {
		return err
	}
	name := filepath.Base(n.Dir)
	if n.Group == "" {
		return runIn(n.Dir, nil, "gh", "repo", "create", name, "--"+n.Visibility, "--source", ".", "--remote", "origin")
	}
	host := []string{"GITLAB_HOST=" + n.GitLabHost}
	// Without a path glab names the project after the cwd and makes it origin; with one it sets up another folder.
	if err := runIn(n.Dir, host, "glab", "repo", "create", "--group", n.Group, "--"+n.Visibility, "--defaultBranch", n.Branch); err != nil {
		return err
	}
	if runIn(n.Dir, nil, "git", "remote", "get-url", "origin") == nil {
		return nil
	}
	return runIn(n.Dir, nil, "git", "remote", "add", "origin", "git@"+n.GitLabHost+":"+n.Group+"/"+name+".git")
}

func runIn(dir string, env []string, name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Dir = dir
	if env != nil {
		c.Env = append(os.Environ(), env...)
	}
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("%s %s: %s", name, args[0], strings.TrimSpace(string(out)+" "+err.Error()))
	}
	return nil
}

// Groups maps the folders under dir that hold clones to the GitLab group those
// clones live in, e.g. Work/acme/devops → acme/devops, so a new repo created
// there lands next to them on GitLab too.
func Groups(all []Repo, dir string) map[string]string {
	out := map[string]string{}
	for _, r := range all {
		if !strings.HasPrefix(r.Dir, dir+"/") {
			continue
		}
		group, ok := strings.CutSuffix(r.Project, "/"+filepath.Base(r.Dir))
		if parent := filepath.Dir(r.Dir); ok && group != "" && out[parent] == "" {
			out[parent] = group
		}
	}
	return out
}

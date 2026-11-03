// Package update notices when a newer claudia is on GitHub and installs it.
// Only builds from `go install …@latest` are checked: a build from a local
// checkout carries vcs settings and is its developer's business.
package update

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/MartyJRE/claudia/internal/state"
)

const (
	module = "github.com/MartyJRE/claudia"
	file   = "update.json"
)

var repoAPI = "https://api.github.com/repos/MartyJRE/claudia"

var (
	buildInfo   = debug.ReadBuildInfo
	execCommand = exec.Command
)

// Current is the commit this binary was built from, if it was installed from
// the module proxy; ok is false for local builds.
func Current() (sha string, ok bool) {
	bi, found := buildInfo()
	if !found {
		return "", false
	}
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" {
			return "", false
		}
	}
	return commitOf(bi.Main.Version)
}

// commitOf reads the commit out of a pseudo-version:
// v0.0.0-20261030145546-1133a71d4dc3 → 1133a71d4dc3.
func commitOf(v string) (string, bool) {
	i := strings.LastIndexByte(v, '-')
	if i < 0 || len(v)-i-1 != 12 || strings.Contains(v, "+") {
		return "", false
	}
	return v[i+1:], true
}

type status struct {
	Latest  string    `json:"latest"`
	Checked time.Time `json:"checked"`
}

// Check asks GitHub for the newest commit on main and remembers it. The attempt
// is recorded up front, so a failing or slow check isn't retried until it's due
// again (the watcher asks every few seconds, and GitHub rate-limits).
func Check() error {
	var s status
	state.Load(file, &s)
	s.Checked = time.Now()
	if err := state.Save(file, s); err != nil {
		return err
	}
	req, err := http.NewRequest("GET", repoAPI+"/commits/main", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github.sha")
	c := &http.Client{Timeout: 10 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 100))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("github: %s", resp.Status)
	}
	s.Latest = strings.TrimSpace(string(b))
	return state.Save(file, s)
}

// Due reports whether the last check is older than every.
func Due(every time.Duration) bool {
	var s status
	return !state.Load(file, &s) || time.Since(s.Checked) > every
}

// Available reports whether GitHub has a commit this binary doesn't.
func Available() bool {
	cur, ok := Current()
	if !ok {
		return false
	}
	var s status
	if !state.Load(file, &s) || s.Latest == "" {
		return false
	}
	return !strings.HasPrefix(s.Latest, cur)
}

type Commit struct {
	SHA     string
	Subject string
}

// Plan says what an update brings: the latest commit and the commits between
// this build and it. For a local build (no known commit) it lists the latest few.
type Plan struct {
	Current string
	Latest  string
	Commits []Commit
}

func Fetch() (Plan, error) {
	var p Plan
	cur, known := Current()
	p.Current = cur
	url := repoAPI + "/commits?per_page=5"
	if known {
		url = repoAPI + "/compare/" + cur + "...main"
	}
	c := &http.Client{Timeout: 10 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return p, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return p, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	type gc struct {
		SHA    string `json:"sha"`
		Commit struct {
			Message string `json:"message"`
		} `json:"commit"`
	}
	var list []gc
	if known {
		var cmp struct {
			Commits []gc `json:"commits"`
		}
		err = json.NewDecoder(resp.Body).Decode(&cmp)
		list = cmp.Commits
	} else {
		err = json.NewDecoder(resp.Body).Decode(&list)
	}
	if err != nil {
		return p, err
	}
	for _, g := range list {
		subject, _, _ := strings.Cut(g.Commit.Message, "\n")
		p.Commits = append(p.Commits, Commit{SHA: g.SHA[:min(7, len(g.SHA))], Subject: subject})
	}
	if known {
		if n := len(p.Commits); n > 0 {
			p.Latest = p.Commits[n-1].SHA
		} else {
			p.Latest = cur[:7]
		}
		// The compare API lists oldest first; newest first reads better.
		slices.Reverse(p.Commits)
	} else if len(p.Commits) > 0 {
		p.Latest = p.Commits[0].SHA
	}
	return p, nil
}

// Install fetches and builds the latest claudia straight from GitHub, so a
// just-pushed commit doesn't wait for the module proxy. Each line of go's
// output goes to line as it happens.
func Install(line func(string)) error {
	cmd := execCommand("go", "install", "-v", module+"@latest")
	cmd.Env = append(os.Environ(), "GOPROXY=direct")
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			line(sc.Text())
		}
		close(done)
	}()
	err := cmd.Wait()
	pw.Close()
	<-done
	return err
}

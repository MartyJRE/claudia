// Package update notices when a newer claudia release is on GitHub and installs it.
package update

import (
	"bufio"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime/debug"
	"slices"
	"strconv"
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

// release is the tag goreleaser stamps in with -ldflags -X.
var release string

// Current is the version this binary was built as: a release tag (v0.4.2), or
// for an install of an untagged commit, that commit. ok is false for local builds.
func Current() (v string, ok bool) {
	if semver(release) != nil {
		return release, true
	}
	bi, found := buildInfo()
	if !found {
		return "", false
	}
	// Go stamps the tag into a build of a clean tagged checkout as well as into
	// `go install …@vX`; any other build from a checkout is its developer's business.
	if semver(bi.Main.Version) != nil {
		return bi.Main.Version, true
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

var semverRe = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

// semver splits a plain release version, vMAJOR.MINOR.PATCH; anything else is nil.
func semver(v string) []int {
	m := semverRe.FindStringSubmatch(v)
	if m == nil {
		return nil
	}
	n := make([]int, 3)
	for i := range n {
		n[i], _ = strconv.Atoi(m[i+1])
	}
	return n
}

// Newer reports whether release version a comes after b.
func Newer(a, b string) bool {
	x, y := semver(a), semver(b)
	return x != nil && y != nil && slices.Compare(x, y) > 0
}

type status struct {
	Latest  string    `json:"latest"`
	Commit  string    `json:"commit"`
	Checked time.Time `json:"checked"`
}

// Check asks GitHub for the latest release and the commit it was tagged on,
// and remembers both. The attempt is recorded up front, so a failing or slow
// check isn't retried until it's due again (the watcher asks every few seconds,
// and GitHub rate-limits).
func Check() error {
	var s status
	state.Load(file, &s)
	s.Checked = time.Now()
	if err := state.Save(file, s); err != nil {
		return err
	}
	var rel struct {
		Tag string `json:"tag_name"`
	}
	if err := getJSON("/releases/latest", &rel); err != nil {
		return err
	}
	sha, err := get("/commits/"+rel.Tag, "application/vnd.github.sha", 100)
	if err != nil {
		return err
	}
	s.Latest, s.Commit = rel.Tag, strings.TrimSpace(string(sha))
	return state.Save(file, s)
}

func get(path, accept string, limit int64) ([]byte, error) {
	req, err := http.NewRequest("GET", repoAPI+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	c := &http.Client{Timeout: 10 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github: %s", resp.Status)
	}
	return b, nil
}

func getJSON(path string, v any) error {
	b, err := get(path, "application/vnd.github+json", 8<<20)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// Due reports whether the last check is older than every.
func Due(every time.Duration) bool {
	var s status
	return !state.Load(file, &s) || time.Since(s.Checked) > every
}

// Available reports whether GitHub has a release this binary doesn't: a newer
// tag or, for an install of an untagged commit, any other commit.
func Available() bool {
	cur, ok := Current()
	if !ok {
		return false
	}
	var s status
	if !state.Load(file, &s) {
		return false
	}
	if semver(cur) != nil {
		return Newer(s.Latest, cur)
	}
	return s.Commit != "" && !strings.HasPrefix(s.Commit, cur)
}

type Release struct {
	Tag       string    `json:"tag_name"`
	Name      string    `json:"name"`
	Body      string    `json:"body"`
	Published time.Time `json:"published_at"`
}

// PerPage is how many releases a page of Releases holds; a shorter page is the last.
const PerPage = 100

// Releases lists the published releases, newest first, from page 1 on.
func Releases(page int) ([]Release, error) {
	var list []Release
	err := getJSON(fmt.Sprintf("/releases?per_page=%d&page=%d", PerPage, page), &list)
	return list, err
}

type Commit struct {
	SHA     string
	Subject string
}

// Plan says what an update brings: the releases newer than this build, newest
// first. An install of an untagged commit can't be placed among the releases,
// so it gets the commits between it and the latest release (or main, before
// the first one). A local build only sees the latest release.
type Plan struct {
	Current  string
	Latest   string
	Releases []Release
	Commits  []Commit
}

func Fetch() (Plan, error) {
	var p Plan
	cur, known := Current()
	p.Current = cur
	list, err := Releases(1)
	if err != nil {
		return p, err
	}
	if len(list) > 0 {
		p.Latest = list[0].Tag
	}
	switch {
	case !known:
		p.Releases = list[:min(1, len(list))]
	case semver(cur) != nil:
		for _, r := range list {
			if Newer(r.Tag, cur) {
				p.Releases = append(p.Releases, r)
			}
		}
		p.Latest = cmp.Or(p.Latest, cur)
	default:
		var c struct {
			Commits []struct {
				SHA    string `json:"sha"`
				Commit struct {
					Message string `json:"message"`
				} `json:"commit"`
			} `json:"commits"`
		}
		if err := getJSON("/compare/"+cur+"..."+cmp.Or(p.Latest, "main"), &c); err != nil {
			return p, err
		}
		for _, g := range c.Commits {
			subject, _, _ := strings.Cut(g.Commit.Message, "\n")
			p.Commits = append(p.Commits, Commit{SHA: g.SHA[:min(7, len(g.SHA))], Subject: subject})
		}
		// The compare API lists oldest first; newest first reads better.
		slices.Reverse(p.Commits)
		if p.Latest == "" {
			p.Latest = cur[:min(7, len(cur))]
			if len(p.Commits) > 0 {
				p.Latest = p.Commits[0].SHA
			}
		}
	}
	return p, nil
}

// Install fetches and builds the latest claudia straight from GitHub, so a
// just-pushed tag doesn't wait for the module proxy. Each line of go's
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

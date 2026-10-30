// Package update notices when a newer claudia is on GitHub and installs it.
// Only builds from `go install …@latest` are checked: a build from a local
// checkout carries vcs settings and is its developer's business.
package update

import (
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"
	"time"

	"github.com/MartyJRE/claudia/internal/state"
)

const (
	module    = "github.com/MartyJRE/claudia"
	latestURL = "https://api.github.com/repos/MartyJRE/claudia/commits/main"
	file      = "update.json"
)

// Current is the commit this binary was built from, if it was installed from
// the module proxy; ok is false for local builds.
func Current() (sha string, ok bool) {
	bi, found := debug.ReadBuildInfo()
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

// Check asks GitHub for the newest commit on main and remembers it.
func Check() error {
	req, err := http.NewRequest("GET", latestURL, nil)
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
	if err != nil || resp.StatusCode != 200 {
		return err
	}
	return state.Save(file, status{Latest: strings.TrimSpace(string(b)), Checked: time.Now()})
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

// Install fetches and installs the latest claudia, straight from GitHub so a
// just-pushed commit doesn't wait for the module proxy.
func Install(out io.Writer) error {
	cmd := exec.Command("go", "install", module+"@latest")
	cmd.Env = append(os.Environ(), "GOPROXY=direct")
	cmd.Stdout, cmd.Stderr = out, out
	return cmd.Run()
}

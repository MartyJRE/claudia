package update

import (
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"testing"

	"github.com/MartyJRE/claudia/internal/state"
)

func built(t *testing.T, version string, settings ...debug.BuildSetting) {
	old := buildInfo
	buildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Version: version}, Settings: settings}, true
	}
	t.Cleanup(func() { buildInfo = old })
}

func TestCurrent(t *testing.T) {
	built(t, "v0.0.0-20261030145546-1133a71d4dc3")
	if sha, ok := Current(); !ok || sha != "1133a71d4dc3" {
		t.Errorf("proxy build: %q %v", sha, ok)
	}
	built(t, "v0.0.0-20261030145546-1133a71d4dc3", debug.BuildSetting{Key: "vcs.revision", Value: "1133a71d4dc3ffff"})
	if _, ok := Current(); ok {
		t.Error("a local checkout build has a commit to compare")
	}
	buildInfo = func() (*debug.BuildInfo, bool) { return nil, false }
	if _, ok := Current(); ok {
		t.Error("no build info")
	}
}

func TestAvailable(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	built(t, "v0.0.0-20261030145546-1133a71d4dc3")
	if Available() {
		t.Error("available without a check")
	}
	state.Save(file, status{Latest: "1133a71d4dc3ffff"})
	if Available() {
		t.Error("available when GitHub has this commit")
	}
	state.Save(file, status{Latest: "2222222222220000"})
	if !Available() {
		t.Error("newer commit not available")
	}
}

func TestCheckErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	os.WriteFile(filepath.Join(dir, "claudia"), nil, 0o600)
	if err := Check(); err == nil {
		t.Error("state can't be saved")
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	old := repoAPI
	t.Cleanup(func() { repoAPI = old })
	repoAPI = "://bad"
	if err := Check(); err == nil || !strings.Contains(err.Error(), "missing protocol scheme") {
		t.Errorf("bad url: %v", err)
	}
}

func TestCheckCutOffBody(t *testing.T) {
	// Promises more than it sends, then hangs up.
	fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		conn, buf, _ := w.(http.Hijacker).Hijack()
		buf.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 50\r\n\r\n1133a71")
		buf.Flush()
		conn.Close()
	})
	if err := Check(); err == nil || !strings.Contains(err.Error(), "unexpected EOF") {
		t.Errorf("got %v", err)
	}
	var s status
	if !state.Load(file, &s) || s.Latest != "" || s.Checked.IsZero() {
		t.Errorf("saved %+v", s)
	}
}

func TestFetchSinceThisBuild(t *testing.T) {
	built(t, "v0.0.0-20261030145546-1133a71d4dc3")
	fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/MartyJRE/claudia/compare/1133a71d4dc3...main" {
			http.Error(w, "unexpected "+r.URL.String(), 400)
			return
		}
		io.WriteString(w, `{"commits":[{"sha":"aaaaaaa1111","commit":{"message":"old"}},{"sha":"bbbbbbb2222","commit":{"message":"new\n\nbody"}}]}`)
	})
	p, err := Fetch()
	if err != nil {
		t.Fatal(err)
	}
	if p.Current != "1133a71d4dc3" || p.Latest != "bbbbbbb" || len(p.Commits) != 2 || p.Commits[0] != (Commit{"bbbbbbb", "new"}) {
		t.Errorf("got %+v", p)
	}

	fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, `{"commits":[]}`) })
	if p, err := Fetch(); err != nil || p.Latest != "1133a71" || p.Commits != nil {
		t.Errorf("up to date: %+v %v", p, err)
	}

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	repoAPI = "http://" + ln.Addr().String()
	ln.Close()
	if _, err := Fetch(); err == nil {
		t.Error("unreachable GitHub")
	}
}

// fakeGo stands in for `go install`: it records the command and runs script.
func fakeGo(t *testing.T, script string) *[]string {
	var ran []string
	old := execCommand
	execCommand = func(name string, args ...string) *exec.Cmd {
		ran = append([]string{name}, args...)
		return exec.Command("/bin/sh", "-c", script)
	}
	t.Cleanup(func() { execCommand = old })
	return &ran
}

func TestInstall(t *testing.T) {
	ran := fakeGo(t, `echo "get github.com/MartyJRE/claudia"; echo "proxy=$GOPROXY" >&2; echo built`)
	var lines []string
	if err := Install(func(l string) { lines = append(lines, l) }); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(*ran, []string{"go", "install", "-v", "github.com/MartyJRE/claudia@latest"}) {
		t.Errorf("ran %q", *ran)
	}
	// stdout and stderr interleave, so only the set of lines is fixed.
	slices.Sort(lines)
	if !slices.Equal(lines, []string{"built", "get github.com/MartyJRE/claudia", "proxy=direct"}) {
		t.Errorf("lines %q", lines)
	}
}

func TestInstallFails(t *testing.T) {
	fakeGo(t, `echo "go: module not found"; exit 1`)
	var lines []string
	if err := Install(func(l string) { lines = append(lines, l) }); err == nil || !strings.Contains(err.Error(), "exit status 1") {
		t.Errorf("got %v", err)
	}
	if !slices.Equal(lines, []string{"go: module not found"}) {
		t.Errorf("lines %q", lines)
	}

	execCommand = func(string, ...string) *exec.Cmd { return exec.Command(filepath.Join(t.TempDir(), "no-go")) }
	if err := Install(func(string) { t.Error("output from a command that didn't start") }); err == nil {
		t.Error("started a missing binary")
	}
}

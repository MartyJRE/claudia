package launch

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/tmux"
)

var (
	work     = claude.Profile{Name: "work", Dir: "/p/work"}
	personal = claude.Profile{Name: "personal", Dir: "/p/personal"}
	other    = claude.Profile{Name: "other", Dir: "/p/other"}
)

func TestProfile(t *testing.T) {
	if got := Profile("personal", []claude.Profile{work, personal}); got != personal {
		t.Errorf("by name: %+v", got)
	}
	if got := Profile("nope", []claude.Profile{work, personal}); got != work {
		t.Errorf("fallback: %+v", got)
	}
	t.Setenv("HOME", "/home/me")
	if got := Profile("x", nil); got != (claude.Profile{Name: "default", Dir: "/home/me/.claude"}) {
		t.Errorf("none: %+v", got)
	}
}

func TestProfileFor(t *testing.T) {
	for _, c := range []struct {
		dir      string
		profiles []claude.Profile
		want     claude.Profile
	}{
		{"/code/Work/pay", []claude.Profile{other, personal, work}, work},
		{"/code/Work/pay", []claude.Profile{other, personal}, other},
		{"/code/Workshop/x", []claude.Profile{work, personal}, personal},
		{"/code/Personal/x", []claude.Profile{work, other}, other},
		{"/code/Personal/x", []claude.Profile{work}, work},
	} {
		if got := ProfileFor(c.dir, c.profiles); got != c.want {
			t.Errorf("%s %v: got %+v want %+v", c.dir, c.profiles, got, c.want)
		}
	}
	if got := WorkProfile([]claude.Profile{personal, work}); got != work {
		t.Errorf("work profile %+v", got)
	}
}

func TestWindowName(t *testing.T) {
	if got := windowName("", "fix it"); got != tmux.Icon+" fix it" {
		t.Errorf("got %q", got)
	}
	if got := windowName("review", "pay !12"); got != "\U000F0208 pay !12" {
		t.Errorf("review icon: %q", got)
	}
	if got := windowName("unknown", "x"); got != tmux.Icon+" x" {
		t.Errorf("unknown kind falls back to the robot: %q", got)
	}
	long := strings.Repeat("é", 40)
	if got := windowName("", long); got != tmux.Icon+" "+strings.Repeat("é", 31)+"…" {
		t.Errorf("got %q", got)
	}
}

func TestRememberKind(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if got := rememberKind(Spec{Kind: "fix"}); got != "fix" {
		t.Errorf("no id: %q", got)
	}
	if got := rememberKind(Spec{ID: "s1", Kind: "review"}); got != "review" {
		t.Errorf("new: %q", got)
	}
	if got := rememberKind(Spec{ID: "s1"}); got != "review" {
		t.Errorf("a resumed session keeps its kind, got %q", got)
	}
	if got := rememberKind(Spec{ID: "s2"}); got != "" {
		t.Errorf("unknown session: %q", got)
	}
}

func gitRepo(t *testing.T, name string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	return dir
}

func TestSessionName(t *testing.T) {
	if got := SessionName(filepath.Join(gitRepo(t, "my.repo"), "sub")); got != "my_repo" {
		t.Errorf("repo subdir: %q", got)
	}
	if got := SessionName(gitRepo(t, "claudia")); got != "claudia-dev" {
		t.Errorf("claudia's own repo: %q", got)
	}
	plain := filepath.Join(t.TempDir(), "notes.d")
	os.Mkdir(plain, 0o755)
	if got := SessionName(plain); got != "notes_d" {
		t.Errorf("not a repo: %q", got)
	}
}

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNew(t *testing.T) {
	sp, id := New("/code/x", work, "fix-it", "do the thing")
	if !uuidRe.MatchString(id) {
		t.Errorf("id %q", id)
	}
	if sp.Dir != "/code/x" || sp.Profile != work || sp.Name != "fix-it" || sp.Session != "" {
		t.Errorf("spec %+v", sp)
	}
	if !slices.Equal(sp.Args[:4], []string{"--session-id", id, "-n", "fix-it"}) || sp.Args[4] != "--append-system-prompt" {
		t.Errorf("args %q", sp.Args)
	}
	if !strings.Contains(sp.Args[5], "ctl here") || sp.Args[len(sp.Args)-1] != "do the thing" {
		t.Errorf("hint/prompt %q", sp.Args[5:])
	}
	if sp, _ := New("/x", work, "n", ""); len(sp.Args) != 6 {
		t.Errorf("empty prompt passed: %q", sp.Args)
	}
	if a, b := NewID(), NewID(); a == b || !uuidRe.MatchString(a) {
		t.Errorf("ids %q %q", a, b)
	}
}

func TestResume(t *testing.T) {
	s := claude.Session{ID: "abcdef0123", Profile: personal, Live: &claude.Live{Cwd: "/code/y", Name: "named"}}
	sp := Resume(s)
	if sp.Dir != "/code/y" || sp.Profile != personal || sp.Name != "named" {
		t.Errorf("spec %+v", sp)
	}
	if !slices.Equal(sp.Args[:3], []string{"--resume", "abcdef0123", "--append-system-prompt"}) || len(sp.Args) != 4 {
		t.Errorf("args %q", sp.Args)
	}
}

func TestAssistant(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/state")
	sp := Assistant(work, "id-1", false)
	if sp.Dir != "/code" || sp.Session != AssistantSession || sp.Name != "assistant" || sp.Profile != work {
		t.Errorf("spec %+v", sp)
	}
	if !slices.Equal(sp.Args[:5], []string{"--session-id", "id-1", "-n", "assistant", "--append-system-prompt"}) {
		t.Errorf("args %q", sp.Args)
	}
	note := sp.Args[5]
	for _, want := range []string{"/state/claudia/brief.json", "Obsidian vault at /notes", "01 - Projects/<TICKET>/", "Ask before"} {
		if !strings.Contains(note, want) {
			t.Errorf("note misses %q: %s", want, note)
		}
	}
	if r := Assistant(work, "id-1", true); !slices.Equal(r.Args[:3], []string{"--resume", "id-1", "--append-system-prompt"}) {
		t.Errorf("resume args %q", r.Args)
	}
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/home/me")
	if !strings.Contains(Assistant(work, "x", false).Args[5], "/home/me/.local/state/claudia/brief.json") {
		t.Error("default state dir")
	}
}

// privateTmux points tmux at a server of the test's own, never the user's.
func privateTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux")
	}
	t.Setenv("TMUX", "")
	sock, err := os.MkdirTemp("/tmp", "cl") // t.TempDir is too long for a socket path
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sock) })
	t.Setenv("TMUX_TMPDIR", sock)
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-server").Run() })
}

func TestStart(t *testing.T) {
	privateTmux(t)

	bin := t.TempDir()
	cwdLog := filepath.Join(bin, "cwd")
	t.Setenv("CLAUDE_CWD_LOG", cwdLog)
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\npwd > \"$CLAUDE_CWD_LOG\"\nexec sleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))

	repo := gitRepo(t, "pay")
	target, err := Start(Spec{Dir: repo, Profile: work, Name: "fix-it", Args: []string{"--resume", "s1"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(target, "pay:") {
		t.Errorf("target %q", target)
	}
	second, err := Start(Spec{Dir: repo, Profile: personal, Name: "other", Args: []string{"-n", "x"}, Session: "elsewhere"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(second, "elsewhere:") {
		t.Errorf("explicit session target %q", second)
	}
	third, err := Start(Spec{Dir: repo, Profile: personal, Name: "again"})
	if err != nil || !strings.HasPrefix(third, "pay:") || third == target {
		t.Errorf("into existing session: %q %v", third, err)
	}

	show := func(target, f string) string {
		out, err := exec.Command("tmux", "display-message", "-p", "-t", target, f).Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	if got := show(target, "#{window_name}"); got != tmux.Icon+" fix-it" {
		t.Errorf("window name %q", got)
	}
	// pane_current_path isn't reliable on Linux right after start; the process reports its own.
	var cwd []byte
	for i := 0; i < 200 && len(cwd) == 0; i++ {
		time.Sleep(10 * time.Millisecond)
		cwd, _ = os.ReadFile(cwdLog)
	}
	if got := strings.TrimSpace(string(cwd)); !strings.HasSuffix(got, "pay") {
		t.Errorf("cwd %q", got)
	}
	cmd := show(target, "#{pane_start_command}")
	if !strings.Contains(cmd, filepath.Join(bin, "claude")) || !strings.Contains(cmd, "--remote-control") || !strings.Contains(cmd, "--resume") {
		t.Errorf("work command %q", cmd)
	}
	if cmd := show(second, "#{pane_start_command}"); strings.Contains(cmd, "--remote-control") {
		t.Errorf("remote control for personal: %q", cmd)
	}
}

func TestStartFallsBackToLocalBin(t *testing.T) {
	privateTmux(t)
	tm, _ := exec.LookPath("tmux")
	bin := t.TempDir()
	if err := os.Symlink(tm, filepath.Join(bin, "tmux")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	home := t.TempDir()
	t.Setenv("HOME", home)
	claudeBin := filepath.Join(home, ".local", "bin", "claude")
	os.MkdirAll(filepath.Dir(claudeBin), 0o755)
	if err := os.WriteFile(claudeBin, []byte("#!/bin/sh\nexec /bin/sleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	target, err := Start(Spec{Dir: t.TempDir(), Profile: personal, Name: "n", Session: "s"})
	if err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command("tmux", "display-message", "-p", "-t", target, "#{pane_start_command}").Output()
	if !strings.Contains(string(out), claudeBin) {
		t.Errorf("command %q", out)
	}
}

func TestNoExecutable(t *testing.T) {
	old := executable
	executable = func() (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { executable = old })
	if sp, _ := New("/x", work, "n", ""); !strings.Contains(sp.Args[5], "`claudia ctl here`") {
		t.Errorf("hint %q", sp.Args[5])
	}
	if sp := Assistant(work, "id", false); !strings.Contains(sp.Args[5], "`claudia ctl inbox`") {
		t.Errorf("note %q", sp.Args[5])
	}
}

package launch

import (
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/tmux"
)

// Spec describes a `claude` process to start in tmux. The binary is called
// directly rather than through the `claude` shell function, which would prompt
// for the profile.
type Spec struct {
	Dir     string
	Profile claude.Profile
	Name    string
	Args    []string
}

func Resume(s claude.Session) Spec {
	return Spec{Dir: s.Dir(), Profile: s.Profile, Name: s.Name(), Args: []string{"--resume", s.ID}}
}

// New starts a fresh session with a known id, so claudia can pair it with an MR or
// ticket before Claude writes anything.
func New(dir string, p claude.Profile, name, prompt string) (Spec, string) {
	id := uuid()
	args := []string{"--session-id", id, "-n", name}
	if prompt != "" {
		args = append(args, prompt)
	}
	return Spec{Dir: dir, Profile: p, Name: name, Args: args}, id
}

func uuid() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func Profile(name string, profiles []claude.Profile) claude.Profile {
	for _, p := range profiles {
		if p.Name == name {
			return p
		}
	}
	return profiles[0]
}

// Start opens the session in a window of the tmux session named after the repo,
// the same naming `tp` uses, so Claude sits next to that repo's nvim and shell.
func Start(sp Spec) (string, error) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		home, _ := os.UserHomeDir()
		bin = filepath.Join(home, ".local", "bin", "claude")
	}
	argv := append([]string{bin}, sp.Args...)
	env := []string{"CLAUDE_CONFIG_DIR=" + sp.Profile.Dir}
	return tmux.NewWindow(SessionName(sp.Dir), windowName(sp.Name), sp.Dir, env, argv)
}

func SessionName(dir string) string {
	root := dir
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output(); err == nil {
		root = strings.TrimSpace(string(out))
	}
	name := strings.ReplaceAll(filepath.Base(root), ".", "_")
	if name == tmux.Home {
		name += "-dev" // claudia's own repo; the bare name is claudia's session
	}
	return name
}

func windowName(name string) string {
	r := []rune(name)
	if len(r) > 24 {
		r = append(r[:23], '…')
	}
	return fmt.Sprintf("✻ %s", string(r))
}

// ProfileFor guesses which Claude license a directory belongs to.
func ProfileFor(dir string, profiles []claude.Profile) claude.Profile {
	want := "personal"
	c := config.Get()
	if strings.HasPrefix(dir, filepath.Join(c.CodeRoot, c.WorkDir)+"/") {
		want = "work"
	}
	for _, p := range profiles {
		if p.Name == want {
			return p
		}
	}
	return profiles[0]
}

// Package brief writes the natural-language status at the top of the inbox: a
// headless `claude -p` run, without tools, over claudia's own data.
package brief

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/MartyJRE/claudia/internal/state"
)

const (
	file        = "brief.json"
	requestFile = "brief-request.json"
)

type Brief struct {
	Text string    `json:"text"`
	At   time.Time `json:"at"`
	Err  string    `json:"error,omitempty"`
}

func Load() (Brief, bool) {
	var b Brief
	return b, state.Load(file, &b)
}

func Path() string { return state.Path(file) }

// Request asks the watcher for a fresh brief now.
func Request() error { return state.Save(requestFile, time.Now()) }

// TakeRequest reports (and clears) a pending request.
func TakeRequest() bool {
	p := state.Path(requestFile)
	if _, err := os.Stat(p); err != nil {
		return false
	}
	os.Remove(p)
	return true
}

const instructions = `You write a short status brief for a software developer, shown at the top of their terminal dashboard.
You get their current state as JSON: inbox (people waiting on them, with waiting_since), their sprint tickets, their open and recently merged MRs, their Claude sessions and the rest of today's meetings, plus your previous brief.
Write at most 4 short sentences, under 80 words in total, plain English, no headings, no lists, no preamble, no sign-off. Pick only what matters most:
- who has been waiting on them longest and for what (people unblock first),
- the next meeting when it's soon, since it bounds what they can start,
- anything failing or stuck (pipelines, rebases, unanswered questions),
- what changed since the previous brief, if anything,
- the one or two things you'd do next.
Refer to tickets by key and MRs as repo!iid. Use names as given. Don't invent anything that isn't in the data.`

// Generate runs claude headlessly over data. configDir selects the Claude
// profile (CLAUDE_CONFIG_DIR).
func Generate(data any, previous, configDir string) (string, error) {
	payload, err := json.MarshalIndent(data, "", " ")
	if err != nil {
		return "", err
	}
	if previous == "" {
		previous = "(none yet)"
	}
	return Headless(instructions, "Previous brief:\n"+previous+"\n\nCurrent state ("+time.Now().Format("Mon 2 Jan 15:04")+"):\n"+string(payload), configDir)
}

// Headless runs one tool-less `claude -p` that leaves no session behind, with
// input on stdin, and returns what it printed. extra goes after the system
// prompt, e.g. "--model", "haiku".
func Headless(system, input, configDir string, extra ...string) (string, error) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		home, _ := os.UserHomeDir()
		bin = filepath.Join(home, ".local", "bin", "claude")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	args := append([]string{"-p", "--no-session-persistence", "--tools", "", "--system-prompt", system}, extra...)
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+configDir)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", &genError{strings.TrimSpace(string(ee.Stderr))}
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

type genError struct{ msg string }

func (e *genError) Error() string { return e.msg }

func Save(b Brief) error { return state.Save(file, b) }

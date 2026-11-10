package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/MartyJRE/claudia/internal/state"
)

type Status string

const (
	StatusBusy    Status = "busy"
	StatusShell   Status = "shell"
	StatusIdle    Status = "idle"
	StatusWaiting Status = "waiting"
)

func (s Status) Working() bool { return s == StatusBusy || s == StatusShell }

type Live struct {
	PID        int    `json:"pid"`
	SessionID  string `json:"sessionId"`
	Cwd        string `json:"cwd"`
	Name       string `json:"name"`
	Status     Status `json:"status"`
	Kind       string `json:"kind"`
	Entrypoint string `json:"entrypoint"`
	StartedAt  int64  `json:"startedAt"`
	UpdatedAt  int64  `json:"statusUpdatedAt"`
	Bridge     string `json:"bridgeSessionId"`
	// Worker is the kind of claudia's own headless run this is (brief,
	// summary), or "" for a session of the user's.
	Worker string `json:"-"`
}

func (l Live) Since() time.Time { return time.UnixMilli(l.UpdatedAt) }

// ReadRegistry returns the live sessions Claude Code registers in <configDir>/sessions/<pid>.json.
// Files of exited processes linger after a crash, so the pid is checked too.
func ReadRegistry(configDir string) []Live {
	files, _ := filepath.Glob(filepath.Join(configDir, "sessions", "*.json"))
	workers := state.WorkerDir("")
	// Claude Code records the cwd with symlinks resolved, e.g. /private/tmp on macOS.
	resolved, _ := filepath.EvalSymlinks(workers)
	var out []Live
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var l Live
		if json.Unmarshal(b, &l) != nil || l.SessionID == "" || !alive(l.PID) {
			continue
		}
		if strings.HasPrefix(l.Kind, "daemon") {
			continue
		}
		for _, root := range []string{workers, resolved} {
			if rel, err := filepath.Rel(root, l.Cwd); root != "" && err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
				l.Worker, _, _ = strings.Cut(rel, string(filepath.Separator))
			}
		}
		out = append(out, l)
	}
	return out
}

func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

package claude

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// Activity says what a session's newest outstanding tool call does.
type Activity struct {
	// Verb is running, editing, reading, delegating or working; "" when no
	// tool call is waiting for its result.
	Verb   string
	Detail string
}

type pendingTool struct {
	id    string
	agent bool
	act   Activity
}

type toolInput struct {
	Command      string `json:"command"`
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
	Path         string `json:"path"`
	Pattern      string `json:"pattern"`
	URL          string `json:"url"`
	Query        string `json:"query"`
	Description  string `json:"description"`
}

// noteTools queues the message's tool calls until their results come back.
func (t *Transcript) noteTools(raw json.RawMessage) {
	var blocks []struct {
		Type  string    `json:"type"`
		ID    string    `json:"id"`
		Name  string    `json:"name"`
		Input toolInput `json:"input"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return
	}
	for _, b := range blocks {
		if b.Type == "tool_use" {
			act, agent := t.describe(b.Name, b.Input)
			t.pending = append(t.pending, pendingTool{b.ID, agent, act})
		}
	}
	t.Activity = t.current()
}

// noteResults drops the calls a user entry answers; a prompt ends them all,
// interrupted or not.
func (t *Transcript) noteResults(raw json.RawMessage) {
	var blocks []struct {
		Type      string `json:"type"`
		ToolUseID string `json:"tool_use_id"`
	}
	if len(raw) > 0 && json.Unmarshal(raw, &blocks) != nil {
		t.pending = nil
	}
	for _, b := range blocks {
		switch b.Type {
		case "tool_result":
			for i, p := range t.pending {
				if p.id == b.ToolUseID {
					t.pending = append(t.pending[:i], t.pending[i+1:]...)
					break
				}
			}
		case "text":
			t.pending = nil
		}
	}
	t.Activity = t.current()
}

func (t *Transcript) current() Activity {
	if len(t.pending) == 0 {
		return Activity{}
	}
	last := t.pending[len(t.pending)-1]
	if last.agent {
		n := 0
		for _, p := range t.pending {
			if p.agent {
				n++
			}
		}
		if n > 1 {
			return Activity{"delegating", fmt.Sprintf("to %d agents", n)}
		}
	}
	return last.act
}

func (t *Transcript) describe(name string, in toolInput) (Activity, bool) {
	switch name {
	case "Bash":
		return Activity{"running", strings.TrimSpace(firstLine(strings.TrimSpace(in.Command)))}, false
	case "Edit", "Write", "MultiEdit":
		return Activity{"editing", t.relPath(in.FilePath)}, false
	case "NotebookEdit":
		return Activity{"editing", t.relPath(in.NotebookPath)}, false
	case "Read":
		return Activity{"reading", t.relPath(in.FilePath)}, false
	case "LS":
		return Activity{"reading", t.relPath(in.Path)}, false
	case "Grep", "Glob":
		return Activity{"reading", in.Pattern}, false
	case "WebFetch":
		u, err := url.Parse(in.URL)
		if err != nil || u.Host == "" {
			return Activity{"reading", in.URL}, false
		}
		return Activity{"reading", u.Host}, false
	case "WebSearch":
		return Activity{"reading", in.Query}, false
	case "Agent", "Task":
		return Activity{"delegating", in.Description}, true
	}
	if i := strings.LastIndex(name, "__"); i >= 0 {
		name = name[i+2:]
	}
	return Activity{"working", name}, false
}

// relPath shows a file inside the session's directory relative to it.
func (t *Transcript) relPath(p string) string {
	if p == "" {
		return ""
	}
	if t.Cwd != "" {
		if rel, err := filepath.Rel(t.Cwd, p); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return filepath.Base(p)
}

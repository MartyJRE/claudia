package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"time"
)

type PRLink struct {
	Number int    `json:"prNumber"`
	URL    string `json:"prUrl"`
	Repo   string `json:"prRepository"`
}

type Usage struct {
	Requests      int
	Input         int
	Output        int
	CacheRead     int
	CacheCreation int
}

// Transcript is what we know about one session from its JSONL file. It is built
// incrementally: Feed only reads what was appended since the last call.
type Transcript struct {
	ID           string
	Path         string
	Cwd          string
	Branch       string
	TicketBranch string
	AITitle      string
	CustomTitle  string
	Color        string // set with /color; "" for the default
	FirstPrompt  string
	LastPrompt   string
	// Tail is the last few prompts and replies, oldest first, for a summary.
	Tail []Turn
	// PlanPath is the session's plan file in plan mode; Plan is the text of
	// its latest ExitPlanMode, for when the file is gone.
	PlanPath string
	Plan     string
	Model    string
	Models   []string
	PRs      []PRLink
	Usage    Usage
	Started  time.Time
	Updated  time.Time

	offset   int64
	size     int64
	mtime    time.Time
	requests map[string]bool
}

// Turn is one prompt (Role "user") or reply ("assistant") in a Tail.
type Turn struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

const (
	tailTurns = 6
	turnChars = 1500
)

// addTurn appends to the tail; a reply split over several entries is one turn.
func (t *Transcript) addTurn(role, text string) {
	if text == "" {
		return
	}
	if n := len(t.Tail); n > 0 && role == "assistant" && t.Tail[n-1].Role == "assistant" {
		t.Tail[n-1].Text = clip(t.Tail[n-1].Text + "\n" + text)
		return
	}
	t.Tail = append(t.Tail, Turn{role, clip(text)})
	if len(t.Tail) > tailTurns {
		t.Tail = slices.Clone(t.Tail[len(t.Tail)-tailTurns:])
	}
}

// clip keeps the end of a long text: a reply's question or wrap-up is last.
func clip(s string) string {
	if r := []rune(s); len(r) > turnChars {
		return "…" + string(r[len(r)-turnChars:])
	}
	return s
}

var skipPrefixes = [][]byte{
	[]byte(`{"type":"api-request`),
	[]byte(`{"type":"file-history`),
	[]byte(`{"type":"attachment"`),
	[]byte(`{"type":"mode"`),
	[]byte(`{"type":"permission-mode"`),
	[]byte(`{"type":"atis-latch"`),
}

type entry struct {
	Type        string    `json:"type"`
	Cwd         string    `json:"cwd"`
	GitBranch   string    `json:"gitBranch"`
	IsSidechain bool      `json:"isSidechain"`
	Timestamp   time.Time `json:"timestamp"`
	RequestID   string    `json:"requestId"`
	AITitle     string    `json:"aiTitle"`
	CustomTitle string    `json:"customTitle"`
	LastPrompt  string    `json:"lastPrompt"`
	AgentColor  string    `json:"agentColor"`
	Attachment  struct {
		Type         string `json:"type"`
		PlanFilePath string `json:"planFilePath"`
	} `json:"attachment"`
	PRLink
	Message struct {
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   struct {
			Input         int `json:"input_tokens"`
			Output        int `json:"output_tokens"`
			CacheRead     int `json:"cache_read_input_tokens"`
			CacheCreation int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

func (t *Transcript) Feed() error {
	st, err := os.Stat(t.Path)
	if err != nil {
		return err
	}
	if st.Size() == t.size && st.ModTime().Equal(t.mtime) {
		return nil
	}
	if st.Size() < t.offset {
		*t = Transcript{ID: t.ID, Path: t.Path}
	}
	t.size, t.mtime = st.Size(), st.ModTime()
	if t.requests == nil {
		t.requests = map[string]bool{}
	}

	f, err := os.Open(t.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return err
	}
	r := bufio.NewReaderSize(f, 256*1024)
	for {
		head, err := r.Peek(32)
		if len(head) == 0 {
			return nil
		}
		if skippable(head) {
			n, complete, err := discardLine(r)
			if !complete {
				return ignoreEOF(err)
			}
			t.offset += n
			continue
		}
		line, err := r.ReadBytes('\n')
		if err != nil {
			// Partial last line: Claude is mid-write, pick it up next time.
			return ignoreEOF(err)
		}
		t.offset += int64(len(line))
		t.apply(line)
	}
}

func skippable(head []byte) bool {
	for _, p := range skipPrefixes {
		if bytes.HasPrefix(head, p) {
			return true
		}
	}
	return false
}

func discardLine(r *bufio.Reader) (int64, bool, error) {
	var n int64
	for {
		chunk, err := r.ReadSlice('\n')
		n += int64(len(chunk))
		if err == nil {
			return n, true, nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return n, false, err
		}
	}
}

func ignoreEOF(err error) error {
	if err == nil || errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func (t *Transcript) apply(line []byte) {
	var e entry
	if json.Unmarshal(line, &e) != nil {
		return
	}
	switch e.Type {
	case "ai-title":
		t.AITitle = e.AITitle
	case "custom-title":
		t.CustomTitle = e.CustomTitle
	case "last-prompt":
		t.LastPrompt = e.LastPrompt
	case "agent-color":
		t.Color = strings.TrimPrefix(e.AgentColor, "default")
	case "attachment":
		if e.Attachment.Type == "plan_mode" && e.Attachment.PlanFilePath != "" {
			t.PlanPath = e.Attachment.PlanFilePath
		}
	case "pr-link":
		for _, p := range t.PRs {
			if p.URL == e.URL {
				return
			}
		}
		t.PRs = append(t.PRs, e.PRLink)
	case "user", "assistant":
		if e.IsSidechain {
			return
		}
		if e.Cwd != "" {
			t.Cwd = e.Cwd
		}
		if e.GitBranch != "" {
			t.Branch = e.GitBranch
			if TicketFrom(e.GitBranch) != "" {
				t.TicketBranch = e.GitBranch
			}
		}
		if !e.Timestamp.IsZero() {
			if t.Started.IsZero() {
				t.Started = e.Timestamp
			}
			t.Updated = e.Timestamp
		}
		if e.Type == "user" {
			if t.FirstPrompt == "" {
				t.FirstPrompt = promptText(e.Message.Content)
			}
			// Prompts with an attachment come as blocks; tool results have no text block.
			if p := replyText(e.Message.Content); !strings.HasPrefix(p, "<") {
				t.addTurn("user", p)
			}
			if p := promptText(e.Message.Content); p != "" {
				t.addTurn("user", p)
			}
			return
		}
		if e.Message.Model == "<synthetic>" {
			return
		}
		t.addTurn("assistant", replyText(e.Message.Content))
		t.notePlan(e.Message.Content)
		if m := e.Message.Model; m != "" {
			if m != t.Model {
				t.Models = append(t.Models, m)
			}
			t.Model = m
		}
		if e.RequestID != "" && !t.requests[e.RequestID] {
			t.requests[e.RequestID] = true
			u := e.Message.Usage
			t.Usage.Requests++
			t.Usage.Input += u.Input
			t.Usage.Output += u.Output
			t.Usage.CacheRead += u.CacheRead
			t.Usage.CacheCreation += u.CacheCreation
		}
	}
}

func promptText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "<") {
		return ""
	}
	return s
}

// notePlan keeps the plan from an ExitPlanMode call.
func (t *Transcript) notePlan(raw json.RawMessage) {
	var blocks []struct {
		Type  string `json:"type"`
		Name  string `json:"name"`
		Input struct {
			Plan         string `json:"plan"`
			PlanFilePath string `json:"planFilePath"`
		} `json:"input"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return
	}
	for _, b := range blocks {
		if b.Type == "tool_use" && b.Name == "ExitPlanMode" {
			if b.Input.Plan != "" {
				t.Plan = b.Input.Plan
			}
			if b.Input.PlanFilePath != "" {
				t.PlanPath = b.Input.PlanFilePath
			}
		}
	}
}

// replyText joins the text blocks of a message's content.
func replyText(raw json.RawMessage) string {
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, strings.TrimSpace(b.Text))
		}
	}
	return strings.Join(parts, "\n")
}

// Title prefers what the user named the session over Claude's generated title.
func (t *Transcript) Title() string {
	switch {
	case t.CustomTitle != "":
		return t.CustomTitle
	case t.AITitle != "":
		return t.AITitle
	default:
		return firstLine(t.FirstPrompt)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

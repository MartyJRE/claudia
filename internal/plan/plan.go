// Package plan holds your inline comments on a Claude session's plan and
// turns them into the feedback the plan prompt takes.
package plan

import (
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/MartyJRE/claudia/internal/state"
)

// Comment is about lines From..To (0-based, inclusive) of the plan. Quote is
// those lines as they were, so the comment follows them when the plan moves.
type Comment struct {
	From  int      `json:"from"`
	To    int      `json:"to"`
	Quote []string `json:"quote"`
	Text  string   `json:"text"`
}

// Text reads the plan: the file, else the text Claude last presented.
func Text(path, fallback string) string {
	if path != "" {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return string(b)
		}
	}
	return fallback
}

func Lines(text string) []string {
	text = strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// New is a comment on lines from..to of lines, in either order.
func New(lines []string, from, to int, text string) Comment {
	from, to = min(from, to), max(from, to)
	return Comment{From: from, To: to, Quote: slices.Clone(lines[from : to+1]), Text: strings.TrimSpace(text)}
}

// Feedback is what goes back to Claude: each comment under the lines it's
// about, quoted, in the order of the plan.
func Feedback(cs []Comment) string {
	cs = slices.Clone(cs)
	slices.SortStableFunc(cs, func(a, b Comment) int { return a.From - b.From })
	var b strings.Builder
	b.WriteString("Comments on the plan:\n")
	for _, c := range cs {
		b.WriteString("\n")
		for _, q := range c.Quote {
			b.WriteString("> " + q + "\n")
		}
		b.WriteString(c.Text + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

const file = "plan-comments.json"

var mu sync.Mutex

func loadAll() map[string][]Comment {
	all := map[string][]Comment{}
	state.Load(file, &all)
	return all
}

// Load returns the comments saved for a plan, moved to where their lines are
// now; a comment whose lines are gone from the plan is dropped.
func Load(key string, lines []string) []Comment {
	mu.Lock()
	defer mu.Unlock()
	var out []Comment
	for _, c := range loadAll()[key] {
		if at, ok := find(lines, c.Quote, c.From); ok {
			c.To, c.From = at+len(c.Quote)-1, at
			out = append(out, c)
		}
	}
	return out
}

// find locates quote in lines, preferring where it was.
func find(lines, quote []string, was int) (int, bool) {
	at := func(i int) bool {
		return i >= 0 && i+len(quote) <= len(lines) && slices.Equal(lines[i:i+len(quote)], quote)
	}
	if len(quote) == 0 {
		return 0, false
	}
	if at(was) {
		return was, true
	}
	for i := range lines {
		if at(i) {
			return i, true
		}
	}
	return 0, false
}

// Save keeps a plan's comments; none removes them.
func Save(key string, cs []Comment) error {
	mu.Lock()
	defer mu.Unlock()
	all := loadAll()
	if len(cs) == 0 {
		delete(all, key)
	} else {
		all[key] = cs
	}
	return state.Save(file, all)
}

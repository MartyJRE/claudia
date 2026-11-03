package atlassian

import (
	"encoding/json"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

type Mention struct {
	Key       string    `json:"key"`
	Summary   string    `json:"summary"`
	Author    string    `json:"author"`
	Created   time.Time `json:"created"`
	Text      string    `json:"text"`
	URL       string    `json:"url"`
	Answered  bool      `json:"answered"`
	CommentID string    `json:"comment_id"`
}

type comment struct {
	ID     string `json:"id"`
	Author struct {
		AccountID   string `json:"accountId"`
		DisplayName string `json:"displayName"`
	} `json:"author"`
	Created string          `json:"created"`
	Body    json.RawMessage `json:"body"`
}

// Mentions finds comments from the last 30 days that @-mention you. A mention
// counts as answered once you comment on that issue after it.
func (c *Client) Mentions(me string) ([]Mention, error) {
	issues, err := c.search("comment ~ currentUser() AND updated >= -30d ORDER BY updated DESC", me)
	if err != nil {
		return nil, err
	}
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		out []Mention
		sem = make(chan struct{}, 6)
	)
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	for _, is := range issues {
		wg.Add(1)
		go func(is Issue) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			// Newest first: mentions only count for 30 days, and a busy issue has more than one page.
			q := url.Values{"orderBy": {"-created"}, "maxResults": {"100"}}
			var res struct {
				Comments []comment `json:"comments"`
			}
			if c.do("GET", "/rest/api/3/issue/"+is.Key+"/comment", q, nil, &res) != nil {
				return
			}
			var lastMine time.Time
			for _, cm := range res.Comments {
				if cm.Author.AccountID == me {
					if t := parseJiraTime(cm.Created); t.After(lastMine) {
						lastMine = t
					}
				}
			}
			for _, cm := range res.Comments {
				created := parseJiraTime(cm.Created)
				if cm.Author.AccountID == me || created.Before(cutoff) || !mentions(cm.Body, me) {
					continue
				}
				m := Mention{
					Key:       is.Key,
					Summary:   is.Summary,
					Author:    cm.Author.DisplayName,
					Created:   created,
					Text:      oneLine(ADFToMarkdown(cm.Body, nil)),
					URL:       c.IssueURL(is.Key) + "?focusedCommentId=" + cm.ID,
					Answered:  lastMine.After(created),
					CommentID: cm.ID,
				}
				mu.Lock()
				out = append(out, m)
				mu.Unlock()
			}
		}(is)
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Answered != out[j].Answered {
			return !out[i].Answered
		}
		return out[i].Created.After(out[j].Created)
	})
	return out, nil
}

func mentions(body json.RawMessage, me string) bool {
	var n adfNode
	if json.Unmarshal(body, &n) != nil {
		return false
	}
	var walk func(adfNode) bool
	walk = func(n adfNode) bool {
		if n.Type == "mention" {
			if id, _ := n.Attrs["id"].(string); id == me {
				return true
			}
		}
		for _, c := range n.Content {
			if walk(c) {
				return true
			}
		}
		return false
	}
	return walk(n)
}

func parseJiraTime(s string) time.Time {
	t, _ := time.Parse("2006-01-02T15:04:05.000-0700", s)
	return t
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

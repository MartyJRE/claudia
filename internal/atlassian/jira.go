package atlassian

import (
	"encoding/json"
	"net/url"
	"time"

	"github.com/MartyJRE/claudia/internal/config"
)

type Issue struct {
	Key      string    `json:"key"`
	Summary  string    `json:"summary"`
	Status   string    `json:"status"`
	Category string    `json:"category"` // new | indeterminate | done
	Type     string    `json:"type"`
	Priority string    `json:"priority"`
	Assignee string    `json:"assignee"`
	Parent   string    `json:"parent"`
	Mine     bool      `json:"mine"`
	Updated  time.Time `json:"updated"`
}

type JiraSnapshot struct {
	Sprint    []Issue   `json:"sprint"`
	Team      []Issue   `json:"team"`
	Mentions  []Mention `json:"mentions"`
	FetchedAt time.Time `json:"fetched_at"`
}

type rawIssue struct {
	Key    string `json:"key"`
	Fields struct {
		Summary string `json:"summary"`
		Status  struct {
			Name     string `json:"name"`
			Category struct {
				Key string `json:"key"`
			} `json:"statusCategory"`
		} `json:"status"`
		IssueType struct {
			Name string `json:"name"`
		} `json:"issuetype"`
		Priority struct {
			Name string `json:"name"`
		} `json:"priority"`
		Assignee *struct {
			AccountID   string `json:"accountId"`
			DisplayName string `json:"displayName"`
		} `json:"assignee"`
		Parent *struct {
			Key string `json:"key"`
		} `json:"parent"`
		Updated string `json:"updated"`
	} `json:"fields"`
}

func (c *Client) search(jql string, me string) ([]Issue, error) {
	var out []Issue
	next := ""
	for {
		q := url.Values{
			"jql":        {jql},
			"fields":     {"summary,status,issuetype,priority,assignee,parent,updated"},
			"maxResults": {"100"},
		}
		if next != "" {
			q.Set("nextPageToken", next)
		}
		var res struct {
			Issues        []rawIssue `json:"issues"`
			NextPageToken string     `json:"nextPageToken"`
		}
		if err := c.do("GET", "/rest/api/3/search/jql", q, nil, &res); err != nil {
			return nil, err
		}
		for _, r := range res.Issues {
			i := Issue{
				Key:      r.Key,
				Summary:  r.Fields.Summary,
				Status:   r.Fields.Status.Name,
				Category: r.Fields.Status.Category.Key,
				Type:     r.Fields.IssueType.Name,
				Priority: r.Fields.Priority.Name,
			}
			if a := r.Fields.Assignee; a != nil {
				i.Assignee = a.DisplayName
				i.Mine = a.AccountID == me
			}
			if p := r.Fields.Parent; p != nil {
				i.Parent = p.Key
			}
			i.Updated, _ = time.Parse("2006-01-02T15:04:05.000-0700", r.Fields.Updated)
			out = append(out, i)
		}
		if res.NextPageToken == "" || len(out) >= 300 {
			return out, nil
		}
		next = res.NextPageToken
	}
}

func (c *Client) Me() (string, error) {
	var me struct {
		AccountID string `json:"accountId"`
	}
	err := c.do("GET", "/rest/api/3/myself", nil, nil, &me)
	return me.AccountID, err
}

func (c *Client) FetchJira() (JiraSnapshot, error) {
	var s JiraSnapshot
	me, err := c.Me()
	if err != nil {
		return s, err
	}
	s.Sprint, err = c.search("assignee = currentUser() AND (sprint in openSprints() OR statusCategory = indeterminate) ORDER BY Rank", me)
	if err != nil {
		return s, err
	}
	if p := config.Get().JiraProject; p != "" {
		s.Team, err = c.search("project = "+p+" AND sprint in openSprints() ORDER BY Rank", me)
		if err != nil {
			return s, err
		}
	}
	if s.Mentions, err = c.Mentions(me); err != nil {
		return s, err
	}
	s.FetchedAt = time.Now()
	return s, nil
}

type Transition struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	To   string `json:"to"`
}

func (c *Client) Transitions(key string) ([]Transition, error) {
	var res struct {
		Transitions []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			To   struct {
				Name string `json:"name"`
			} `json:"to"`
		} `json:"transitions"`
	}
	if err := c.do("GET", "/rest/api/3/issue/"+key+"/transitions", nil, nil, &res); err != nil {
		return nil, err
	}
	var out []Transition
	for _, t := range res.Transitions {
		out = append(out, Transition{t.ID, t.Name, t.To.Name})
	}
	return out, nil
}

func (c *Client) Transition(key, id string) error {
	body := map[string]any{"transition": map[string]string{"id": id}}
	return c.do("POST", "/rest/api/3/issue/"+key+"/transitions", nil, body, nil)
}

func (c *Client) IssueURL(key string) string { return c.Base + "/browse/" + key }

type fullIssue struct {
	Key    string `json:"key"`
	Fields struct {
		Summary     string          `json:"summary"`
		Description json.RawMessage `json:"description"`
		Status      struct {
			Name string `json:"name"`
		} `json:"status"`
		IssueType struct {
			Name string `json:"name"`
		} `json:"issuetype"`
		Parent *struct {
			Key    string `json:"key"`
			Fields struct {
				Summary string `json:"summary"`
			} `json:"fields"`
		} `json:"parent"`
		IssueLinks []struct {
			Type struct {
				Inward  string `json:"inward"`
				Outward string `json:"outward"`
			} `json:"type"`
			InwardIssue  *linked `json:"inwardIssue"`
			OutwardIssue *linked `json:"outwardIssue"`
		} `json:"issuelinks"`
		Attachment []struct {
			Filename string `json:"filename"`
			MimeType string `json:"mimeType"`
			Size     int64  `json:"size"`
			Content  string `json:"content"`
		} `json:"attachment"`
		Comment struct {
			Comments []struct {
				Author struct {
					DisplayName string `json:"displayName"`
				} `json:"author"`
				Created string          `json:"created"`
				Body    json.RawMessage `json:"body"`
			} `json:"comments"`
		} `json:"comment"`
	} `json:"fields"`
}

type linked struct {
	Key    string `json:"key"`
	Fields struct {
		Summary string `json:"summary"`
		Status  struct {
			Name string `json:"name"`
		} `json:"status"`
	} `json:"fields"`
}

func trimDate(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

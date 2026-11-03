package atlassian

import (
	"html"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Page struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Space    string    `json:"space"`
	URL      string    `json:"url"`
	Excerpt  string    `json:"excerpt"`
	Modified time.Time `json:"modified"`
	Comment  bool      `json:"comment"`
	OnPage   string    `json:"on_page"` // the page it's on: a comment's page, or the page itself
}

type ConfluenceSnapshot struct {
	Mentions  []Page    `json:"mentions"`
	Mine      []Page    `json:"mine"`
	Comments  []Page    `json:"comments"` // mine, from the last 30 days
	FetchedAt time.Time `json:"fetched_at"`
}

// flexID takes an id Confluence sends as a string or, for some containers, a number.
type flexID string

func (f *flexID) UnmarshalJSON(b []byte) error {
	*f = flexID(strings.Trim(string(b), `"`))
	if *f == "null" {
		*f = ""
	}
	return nil
}

func (c *Client) cql(cql string, limit int) ([]Page, error) {
	q := url.Values{"cql": {cql}, "limit": {strconv.Itoa(limit)}, "expand": {"content.space,content.container"}}
	var res struct {
		Results []struct {
			Content struct {
				ID    string `json:"id"`
				Type  string `json:"type"`
				Title string `json:"title"`
				Space struct {
					Key string `json:"key"`
				} `json:"space"`
				Container struct {
					ID flexID `json:"id"`
				} `json:"container"`
			} `json:"content"`
			Title        string `json:"title"`
			Excerpt      string `json:"excerpt"`
			URL          string `json:"url"`
			LastModified string `json:"lastModified"`
		} `json:"results"`
	}
	if err := c.do("GET", "/wiki/rest/api/search", q, nil, &res); err != nil {
		return nil, err
	}
	var out []Page
	for _, r := range res.Results {
		p := Page{
			ID:      r.Content.ID,
			Title:   html.UnescapeString(r.Title),
			Space:   r.Content.Space.Key,
			URL:     c.Base + "/wiki" + r.URL,
			Excerpt: r.Excerpt,
			Comment: r.Content.Type == "comment",
			OnPage:  r.Content.ID,
		}
		if p.Comment && r.Content.Container.ID != "" {
			p.OnPage = string(r.Content.Container.ID)
		}
		p.Modified, _ = time.Parse(time.RFC3339, r.LastModified)
		out = append(out, p)
	}
	return out, nil
}

func (c *Client) FetchConfluence() (ConfluenceSnapshot, error) {
	var s ConfluenceSnapshot
	var err error
	if s.Mentions, err = c.cql("mention = currentUser() and lastmodified >= now(\"-30d\") order by lastmodified desc", 25); err != nil {
		return s, err
	}
	if s.Mine, err = c.cql("contributor = currentUser() and type = page order by lastmodified desc", 25); err != nil {
		return s, err
	}
	if s.Comments, err = c.cql("type = comment and creator = currentUser() and created >= now(\"-30d\") order by created desc", 100); err != nil {
		return s, err
	}
	s.FetchedAt = time.Now()
	return s, nil
}

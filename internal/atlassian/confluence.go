package atlassian

import (
	"html"
	"net/url"
	"strconv"
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
}

type ConfluenceSnapshot struct {
	Mentions  []Page    `json:"mentions"`
	Mine      []Page    `json:"mine"`
	FetchedAt time.Time `json:"fetched_at"`
}

func (c *Client) cql(cql string, limit int) ([]Page, error) {
	q := url.Values{"cql": {cql}, "limit": {strconv.Itoa(limit)}, "expand": {"content.space"}}
	var res struct {
		Results []struct {
			Content struct {
				ID    string `json:"id"`
				Type  string `json:"type"`
				Title string `json:"title"`
				Space struct {
					Key string `json:"key"`
				} `json:"space"`
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
	s.FetchedAt = time.Now()
	return s, nil
}

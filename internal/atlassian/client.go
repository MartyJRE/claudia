package atlassian

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/MartyJRE/claudia/internal/config"
)

type Client struct {
	Base  string
	email string
	token string
	http  *http.Client
}

var ErrNoCredentials = errors.New("set ATLASSIAN_EMAIL and ATLASSIAN_API_TOKEN")

func New() (*Client, error) {
	c := &Client{
		Base:  config.Get().AtlassianURL,
		email: os.Getenv("ATLASSIAN_EMAIL"),
		token: os.Getenv("ATLASSIAN_API_TOKEN"),
		http:  &http.Client{Timeout: 30 * time.Second},
	}
	if c.Base == "" {
		return nil, errors.New("set atlassian_url in " + config.Path())
	}
	if c.email == "" || c.token == "" {
		return nil, ErrNoCredentials
	}
	return c, nil
}

func (c *Client) do(method, path string, q url.Values, body any, v any) error {
	u := c.Base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(method, u, rd)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.email, c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("%s %s: %s %s", method, path, resp.Status, strings.TrimSpace(string(b)))
	}
	if v == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func (c *Client) download(u string, w io.Writer) error {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.email, c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

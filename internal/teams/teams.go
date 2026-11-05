// Package teams reads Microsoft Teams through Graph, as you: the channel review
// requests are posted in, plus chats and @mentions still waiting on you; and
// replies or reacts there.
package teams

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/state"
)

type Post struct {
	ID      string    `json:"id"`
	From    string    `json:"from"`
	At      time.Time `json:"at"`
	WebURL  string    `json:"web_url"`
	Text    string    `json:"text"`
	MRURLs  []string  `json:"mr_urls"`
	Mention []string  `json:"mentions"`
	Replies []Reply   `json:"replies"`
	// ReactedBy are the user ids that reacted to the post itself.
	ReactedBy []string `json:"reacted_by,omitempty"`
}

type Reply struct {
	From string    `json:"from"`
	At   time.Time `json:"at"`
	Text string    `json:"text"`
}

// Reacted tells whether the user with this id reacted to the post.
func (p Post) Reacted(id string) bool { return id != "" && slices.Contains(p.ReactedBy, id) }

// RepliedBy tells whether name has answered in the thread.
func (p Post) RepliedBy(name string) bool {
	return name != "" && slices.ContainsFunc(p.Replies, func(r Reply) bool { return strings.EqualFold(r.From, name) })
}

func (p Post) Mentions(name string) bool {
	return name != "" && slices.ContainsFunc(p.Mention, func(m string) bool { return strings.EqualFold(m, name) })
}

type Snapshot struct {
	MeID      string    `json:"me_id,omitempty"`
	Posts     []Post    `json:"posts"`
	Waiting   []Waiting `json:"waiting"`
	Events    []Event   `json:"events"` // the next two weeks
	MailErr   string    `json:"mail_err,omitempty"`
	FetchedAt time.Time `json:"fetched_at"`
}

// ForMR returns the posts about an MR, newest first.
func (s Snapshot) ForMR(webURL string) []Post {
	var out []Post
	for _, p := range s.Posts {
		if slices.Contains(p.MRURLs, webURL) {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b Post) int { return b.At.Compare(a.At) })
	return out
}

func (s Snapshot) Post(id string) (Post, bool) {
	i := slices.IndexFunc(s.Posts, func(p Post) bool { return p.ID == id })
	if i < 0 {
		return Post{}, false
	}
	return s.Posts[i], true
}

type Client struct {
	cfg   config.Teams
	http  *http.Client
	login string
	graph string

	mu  sync.Mutex
	tok token

	meOnce sync.Mutex
	me     me
	chans  channelCache
}

type token struct {
	Access  string    `json:"access_token"`
	Refresh string    `json:"refresh_token"`
	Expires time.Time `json:"expires"`
	Scopes  string    `json:"scope,omitempty"`
}

const (
	tokenFile = "teams-token.json"
	graphURL  = "https://graph.microsoft.com/"
	// .default asks for whatever the app has been granted, so a permission
	// still waiting on an admin never fails the sign-in; granted says what came.
	scopes = graphURL + ".default offline_access"
)

// granted says whether the signed-in token carries a Graph permission, e.g.
// "Mail.Read". A token saved before claudia kept scopes is refreshed once.
func (c *Client) granted(perm string) bool {
	c.mu.Lock()
	unknown := c.tok.Scopes == "" && c.tok.Refresh != ""
	if unknown {
		c.tok.Access = ""
	}
	c.mu.Unlock()
	if unknown {
		if _, err := c.access(); err != nil {
			return false
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Contains(strings.Fields(c.tok.Scopes), graphURL+perm)
}

// need fails a feature whose permission the app hasn't been granted yet.
func (c *Client) need(perms ...string) error {
	for _, p := range perms {
		if !c.granted(p) {
			return fmt.Errorf("%w: %s (an admin grants it on the claudia app, then run `claudia teams login`)", ErrNotGranted, p)
		}
	}
	return nil
}

var (
	siteConfig = config.Get

	ErrUnconfigured = errors.New("Teams isn't set up: fill in teams.tenant and teams.client_id in config.json")
	ErrNoChannel    = errors.New("no review channel: set teams.team_id and teams.channel_id (see `claudia teams channels`)")
	ErrSignedOut    = errors.New("not signed in to Teams: run `claudia teams login`")
	ErrNotGranted   = errors.New("claudia hasn't been granted this permission yet")
)

// New fails with ErrUnconfigured until config.json names the app.
func New() (*Client, error) {
	t := siteConfig().Teams
	if t.Tenant == "" || t.ClientID == "" {
		return nil, ErrUnconfigured
	}
	c := &Client{
		cfg:   t,
		http:  &http.Client{Timeout: 30 * time.Second},
		login: "https://login.microsoftonline.com/" + url.PathEscape(t.Tenant) + "/oauth2/v2.0",
		graph: "https://graph.microsoft.com/v1.0",
	}
	state.Load(tokenFile, &c.tok)
	return c, nil
}

// Login runs the device-code sign-in; show gets the "go to … and enter …" line.
func (c *Client) Login(show func(string)) error {
	var dc struct {
		DeviceCode string `json:"device_code"`
		Message    string `json:"message"`
		Interval   int    `json:"interval"`
		ExpiresIn  int    `json:"expires_in"`
	}
	if err := c.form("/devicecode", url.Values{"client_id": {c.cfg.ClientID}, "scope": {scopes}}, &dc); err != nil {
		return err
	}
	show(dc.Message)
	wait := time.Duration(max(dc.Interval, 1)) * time.Second
	deadline := time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)
	for time.Now().Before(deadline) {
		sleep(wait)
		err := c.grant(url.Values{
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			"client_id":   {c.cfg.ClientID},
			"device_code": {dc.DeviceCode},
		})
		var oe *oauthError
		switch {
		case err == nil:
			return nil
		case errors.As(err, &oe) && oe.Code == "authorization_pending":
		case errors.As(err, &oe) && oe.Code == "slow_down":
			wait += 5 * time.Second
		default:
			return err
		}
	}
	return errors.New("the sign-in code expired; run `claudia teams login` again")
}

var sleep = time.Sleep

type oauthError struct {
	Code string `json:"error"`
	Desc string `json:"error_description"`
}

func (e *oauthError) Error() string {
	d, _, _ := strings.Cut(e.Desc, "\r\n")
	return "Teams sign-in: " + e.Code + ": " + d
}

func (c *Client) form(path string, v url.Values, out any) error {
	resp, err := c.http.PostForm(c.login+path, v)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		oe := &oauthError{}
		if json.Unmarshal(b, oe) == nil && oe.Code != "" {
			return oe
		}
		return fmt.Errorf("Teams sign-in: %s", resp.Status)
	}
	return json.Unmarshal(b, out)
}

func (c *Client) grant(v url.Values) error {
	var t struct {
		Access    string `json:"access_token"`
		Refresh   string `json:"refresh_token"`
		ExpiresIn int    `json:"expires_in"`
		Scope     string `json:"scope"`
	}
	if err := c.form("/token", v, &t); err != nil {
		return err
	}
	c.tok = token{Access: t.Access, Refresh: t.Refresh, Expires: time.Now().Add(time.Duration(t.ExpiresIn)*time.Second - time.Minute), Scopes: t.Scope}
	if c.tok.Refresh == "" {
		c.tok.Refresh = v.Get("refresh_token")
	}
	return state.Save(tokenFile, c.tok)
}

func (c *Client) access() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tok.Access != "" && time.Now().Before(c.tok.Expires) {
		return c.tok.Access, nil
	}
	if c.tok.Refresh == "" {
		return "", ErrSignedOut
	}
	err := c.grant(url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {c.cfg.ClientID},
		"refresh_token": {c.tok.Refresh},
		"scope":         {scopes},
	})
	var oe *oauthError
	if errors.As(err, &oe) && oe.Code == "invalid_grant" {
		return "", ErrSignedOut
	}
	return c.tok.Access, err
}

func (c *Client) do(method, path string, body any) ([]byte, error) {
	return c.doHeader(method, path, body, nil)
}

func (c *Client) doHeader(method, path string, body any, h map[string]string) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(mustJSON(body))
		h = maps.Clone(h)
		if h == nil {
			h = map[string]string{}
		}
		h["Content-Type"] = "application/json"
	}
	return c.send(method, c.graph+path, rd, h, true)
}

// send makes a request to an absolute URL; auth adds the bearer token (upload
// session URLs are pre-authorised and must go without it).
func (c *Client) send(method, u string, body io.Reader, h map[string]string, auth bool) ([]byte, error) {
	req, err := http.NewRequest(method, u, body)
	if err != nil {
		return nil, err
	}
	if auth {
		tok, err := c.access()
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	for k, v := range h {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		if len(out) > 500 {
			out = out[:500]
		}
		return nil, fmt.Errorf("Teams %s: %s %s", method, resp.Status, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func (c *Client) channel() string {
	return "/teams/" + url.PathEscape(c.cfg.TeamID) + "/channels/" + url.PathEscape(c.cfg.ChannelID) + "/messages"
}

// Fetch reads the review channel (when configured) and everything waiting on you.
func (c *Client) Fetch() (Snapshot, error) {
	posts, err := c.FetchReview()
	if err != nil && !errors.Is(err, ErrNoChannel) {
		return Snapshot{}, err
	}
	w, err := c.fetchWaiting()
	if err != nil {
		return Snapshot{}, err
	}
	evs, err := c.Calendar(now(), now().Add(lookback))
	if err != nil {
		return Snapshot{}, err
	}
	w = append(w, invites(evs)...)
	var mailErr string
	if mc := siteConfig().Teams.Mail; mc != nil && c.granted("Mail.Read") {
		mail, err := c.fetchMail(*mc)
		if err != nil {
			mailErr = err.Error()
		}
		w = append(w, mail...)
	}
	slices.SortFunc(w, func(a, b Waiting) int { return a.At.Compare(b.At) })
	return Snapshot{MeID: c.me.ID, Posts: posts, Waiting: w, Events: evs, MailErr: mailErr, FetchedAt: time.Now()}, nil
}

// FetchReview reads the last 50 posts of the review channel, replies included.
func (c *Client) FetchReview() ([]Post, error) {
	if c.cfg.TeamID == "" || c.cfg.ChannelID == "" {
		return nil, ErrNoChannel
	}
	b, err := c.do("GET", c.channel()+"?$top=50&$expand=replies", nil)
	if err != nil {
		return nil, err
	}
	return Parse(b, siteConfig().GitLabHost)
}

type Channel struct {
	Team      string `json:"team"`
	TeamID    string `json:"team_id"`
	Channel   string `json:"channel"`
	ChannelID string `json:"channel_id"`
}

// Channels lists every channel in the teams you're in.
func (c *Client) Channels() ([]Channel, error) {
	var teams struct {
		Value []struct{ ID, DisplayName string } `json:"value"`
	}
	if err := c.getJSON("/me/joinedTeams", &teams); err != nil {
		return nil, err
	}
	var out []Channel
	for _, t := range teams.Value {
		var chs struct {
			Value []struct{ ID, DisplayName string } `json:"value"`
		}
		if err := c.getJSON("/teams/"+url.PathEscape(t.ID)+"/channels", &chs); err != nil {
			return nil, err
		}
		for _, ch := range chs.Value {
			out = append(out, Channel{Team: t.DisplayName, TeamID: t.ID, Channel: ch.DisplayName, ChannelID: ch.ID})
		}
	}
	return out, nil
}

func (c *Client) getJSON(path string, v any) error {
	b, err := c.do("GET", path, nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// Reply sends text (plain, escaped here) and any files: into the chat, or as a
// reply in the channel thread. s is a ref (see Waiting) or a review-channel
// message id.
func (c *Client) Reply(s, text string, files ...string) error {
	r, err := c.parseRef(s)
	if err != nil {
		return err
	}
	path := r.messages()
	if r.chat == "" {
		path += "/" + url.PathEscape(r.msg) + "/replies"
	}
	_, err = c.sendTo(r, path, text, files)
	return err
}

// React puts an emoji reaction (e.g. "👍", "✅") on the message.
func (c *Client) React(s, emoji string) error {
	r, err := c.parseRef(s)
	if err != nil {
		return err
	}
	_, err = c.do("POST", r.messages()+"/"+url.PathEscape(r.msg)+"/setReaction", map[string]string{"reactionType": emoji})
	return err
}

type identity struct {
	User *struct {
		ID          string `json:"id"`
		DisplayName string `json:"displayName"`
	} `json:"user"`
	Application *struct {
		DisplayName string `json:"displayName"`
	} `json:"application"`
}

func (i *identity) name() string {
	switch {
	case i == nil:
		return ""
	case i.User != nil:
		return i.User.DisplayName
	case i.Application != nil:
		return i.Application.DisplayName
	}
	return ""
}

type reaction struct {
	By identity `json:"user"`
}

// reactors are the ids of the users who reacted (Graph leaves their names out).
func reactors(rs []reaction) []string {
	var out []string
	for _, r := range rs {
		if r.By.User != nil && r.By.User.ID != "" && !slices.Contains(out, r.By.User.ID) {
			out = append(out, r.By.User.ID)
		}
	}
	return out
}

type message struct {
	ID      string    `json:"id"`
	Created time.Time `json:"createdDateTime"`
	Deleted *string   `json:"deletedDateTime"`
	WebURL  string    `json:"webUrl"`
	From    *identity `json:"from"`
	Body    struct {
		Content string `json:"content"`
	} `json:"body"`
	Mentions []struct {
		Mentioned identity `json:"mentioned"`
	} `json:"mentions"`
	Attachments []struct {
		Content     *string `json:"content"`
		ContentType string  `json:"contentType"`
		ContentURL  string  `json:"contentUrl"`
		Name        string  `json:"name"`
	} `json:"attachments"`
	Replies   []message  `json:"replies"`
	Reactions []reaction `json:"reactions"`
}

// Parse reads Graph's message list ({"value": [...]} or a bare array).
func Parse(b []byte, gitlabHost string) ([]Post, error) {
	var msgs []message
	var wrapped struct {
		Value []message `json:"value"`
	}
	if err := json.Unmarshal(b, &wrapped); err == nil && wrapped.Value != nil {
		msgs = wrapped.Value
	} else if err := json.Unmarshal(b, &msgs); err != nil {
		return nil, fmt.Errorf("Teams answered with something other than messages: %w", err)
	}
	mrRe := regexp.MustCompile(`https://` + regexp.QuoteMeta(gitlabHost) + `/[\w./-]+?/-/merge_requests/\d+`)
	var out []Post
	for _, m := range msgs {
		if m.Deleted != nil {
			continue
		}
		p := Post{ID: m.ID, From: m.From.name(), At: m.Created, WebURL: m.WebURL, Text: plain(m.Body.Content), ReactedBy: reactors(m.Reactions)}
		hay := m.Body.Content
		for _, a := range m.Attachments {
			if a.Content != nil {
				hay += "\n" + *a.Content
			}
		}
		for _, u := range mrRe.FindAllString(hay, -1) {
			if !slices.Contains(p.MRURLs, u) {
				p.MRURLs = append(p.MRURLs, u)
			}
		}
		for _, x := range m.Mentions {
			if n := x.Mentioned.name(); n != "" && !slices.Contains(p.Mention, n) {
				p.Mention = append(p.Mention, n)
			}
		}
		for _, r := range m.Replies {
			if r.Deleted == nil {
				p.Replies = append(p.Replies, Reply{From: r.From.name(), At: r.Created, Text: plain(r.Body.Content)})
			}
		}
		slices.SortFunc(p.Replies, func(a, b Reply) int { return a.At.Compare(b.At) })
		out = append(out, p)
	}
	return out, nil
}

var (
	tagRe        = regexp.MustCompile(`<[^>]*>`)
	attachmentRe = regexp.MustCompile(`(?s)<attachment[^>]*>.*?</attachment>`)
)

func plain(s string) string {
	s = attachmentRe.ReplaceAllString(s, " ")
	s = strings.NewReplacer("<br>", " ", "<br/>", " ", "</p>", " ").Replace(s)
	return strings.Join(strings.Fields(html.UnescapeString(tagRe.ReplaceAllString(s, ""))), " ")
}

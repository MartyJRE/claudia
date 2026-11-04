package teams

import (
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

// Waiting is something in Teams where someone else had the last word: a chat
// whose last message isn't yours, or an @mention you haven't answered in its
// thread. Ref addresses it for Reply, React and Show.
type Waiting struct {
	Ref    string    `json:"ref"`
	Kind   string    `json:"kind"` // chat, mention
	Where  string    `json:"where"`
	From   string    `json:"from"`
	At     time.Time `json:"at"`
	Text   string    `json:"text"`
	WebURL string    `json:"web_url"`
	Unread bool      `json:"unread,omitempty"`
	MRURLs []string  `json:"mr_urls,omitempty"`
}

// lookback is how far back a message still counts as waiting on you.
const lookback = 14 * 24 * time.Hour

var now = time.Now

type me struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

func (c *Client) whoami() (me, error) {
	c.meOnce.Lock()
	defer c.meOnce.Unlock()
	if c.me.ID != "" {
		return c.me, nil
	}
	err := c.getJSON("/me?$select=id,displayName", &c.me)
	return c.me, err
}

type chatMessage struct {
	ID          string    `json:"id"`
	Created     time.Time `json:"createdDateTime"`
	Deleted     *string   `json:"deletedDateTime"`
	IsDeleted   bool      `json:"isDeleted"`
	MessageType string    `json:"messageType"`
	From        *identity `json:"from"`
	Body        struct {
		Content string `json:"content"`
	} `json:"body"`
	Attachments []attachment `json:"attachments"`
}

func (m chatMessage) human() bool {
	return !m.IsDeleted && m.Deleted == nil && (m.MessageType == "" || m.MessageType == "message") && m.From != nil && m.From.User != nil
}

type chat struct {
	ID        string `json:"id"`
	Topic     string `json:"topic"`
	ChatType  string `json:"chatType"`
	WebURL    string `json:"webUrl"`
	Viewpoint *struct {
		LastRead time.Time `json:"lastMessageReadDateTime"`
	} `json:"viewpoint"`
	Last *chatMessage `json:"lastMessagePreview"`
}

// waitingChats parses /me/chats: one-on-one and group chats (not meeting
// chats) whose last message, from the last two weeks, is someone else's.
func waitingChats(b []byte, myID string) ([]Waiting, error) {
	var v struct {
		Value []chat `json:"value"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	var out []Waiting
	for _, ch := range v.Value {
		l := ch.Last
		if l == nil || (ch.ChatType != "oneOnOne" && ch.ChatType != "group") || !l.human() || l.From.User.ID == myID || now().Sub(l.Created) > lookback {
			continue
		}
		where := ch.Topic
		if where == "" {
			where = "chat with " + l.From.name()
			if ch.ChatType == "group" {
				where = "group chat"
			}
		}
		out = append(out, Waiting{
			Ref: "chat/" + ch.ID + "/" + l.ID, Kind: "chat", Where: where, From: l.From.name(), At: l.Created,
			Text: plain(l.Body.Content), WebURL: ch.WebURL, Unread: ch.Viewpoint == nil || l.Created.After(ch.Viewpoint.LastRead),
		})
	}
	return out, nil
}

// unansweredMentions finds messages and replies in a channel that @mention
// you, where you haven't replied in the thread since.
func unansweredMentions(b []byte, ch Channel, myID, gitlabHost string) ([]Waiting, error) {
	var v struct {
		Value []message `json:"value"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	mine := func(m message) bool { return m.From != nil && m.From.User != nil && m.From.User.ID == myID }
	mentioned := func(m message) bool {
		return m.Deleted == nil && !mine(m) && slices.ContainsFunc(m.Mentions, func(x struct {
			Mentioned identity `json:"mentioned"`
		}) bool {
			return x.Mentioned.User != nil && x.Mentioned.User.ID == myID
		})
	}
	var out []Waiting
	for _, root := range v.Value {
		thread := append([]message{root}, root.Replies...)
		var hit *message
		for i := range thread {
			if mentioned(thread[i]) && now().Sub(thread[i].Created) <= lookback && (hit == nil || thread[i].Created.After(hit.Created)) {
				hit = &thread[i]
			}
		}
		// Answered: a reply of mine after it, or my reaction on it or on the thread's post.
		if hit == nil || slices.ContainsFunc(thread, func(m message) bool { return mine(m) && m.Created.After(hit.Created) }) ||
			slices.Contains(reactors(hit.Reactions), myID) || slices.Contains(reactors(root.Reactions), myID) {
			continue
		}
		posts, _ := Parse(mustJSON([]message{root}), gitlabHost)
		w := Waiting{
			Ref: ch.TeamID + "/" + ch.ChannelID + "/" + root.ID, Kind: "mention", Where: ch.Team + " › " + ch.Channel,
			From: hit.From.name(), At: hit.Created, Text: plain(hit.Body.Content), WebURL: hit.WebURL,
		}
		if w.WebURL == "" {
			w.WebURL = root.WebURL
		}
		if len(posts) == 1 {
			w.MRURLs = posts[0].MRURLs
		}
		out = append(out, w)
	}
	return out, nil
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

type channelCache struct {
	mu  sync.Mutex
	at  time.Time
	chs []Channel
}

func (c *Client) cachedChannels() ([]Channel, error) {
	c.chans.mu.Lock()
	defer c.chans.mu.Unlock()
	if c.chans.chs != nil && now().Sub(c.chans.at) < time.Hour {
		return c.chans.chs, nil
	}
	chs, err := c.Channels()
	if err != nil {
		return nil, err
	}
	c.chans.chs, c.chans.at = chs, now()
	return chs, nil
}

// fetchWaiting collects waiting chats and unanswered mentions across every
// channel you're in. A channel you can't read (private, archived) is skipped.
func (c *Client) fetchWaiting() ([]Waiting, error) {
	m, err := c.whoami()
	if err != nil {
		return nil, err
	}
	b, err := c.do("GET", "/me/chats?$expand=lastMessagePreview&$orderby=lastMessagePreview/createdDateTime%20desc&$top=50", nil)
	if err != nil {
		return nil, err
	}
	out, err := waitingChats(b, m.ID)
	if err != nil {
		return nil, err
	}
	chs, err := c.cachedChannels()
	if err != nil {
		return nil, err
	}
	host := siteConfig().GitLabHost
	for _, ch := range chs {
		b, err := c.do("GET", "/teams/"+url.PathEscape(ch.TeamID)+"/channels/"+url.PathEscape(ch.ChannelID)+"/messages?$top=20&$expand=replies", nil)
		if err != nil {
			continue
		}
		ws, err := unansweredMentions(b, ch, m.ID, host)
		if err == nil {
			out = append(out, ws...)
		}
	}
	return out, nil
}

// ref is chat/<chat>/<message>, <team>/<channel>/<message>, or a bare
// message id in the review channel. Graph ids never contain a slash.
type ref struct {
	chat, team, channel, msg string
}

func (c *Client) parseRef(s string) (ref, error) {
	p := strings.Split(s, "/")
	switch {
	case len(p) == 3 && p[0] == "chat":
		return ref{chat: p[1], msg: p[2]}, nil
	case len(p) == 3:
		return ref{team: p[0], channel: p[1], msg: p[2]}, nil
	case len(p) == 1 && s != "":
		if c.cfg.TeamID == "" || c.cfg.ChannelID == "" {
			return ref{}, ErrNoChannel
		}
		return ref{team: c.cfg.TeamID, channel: c.cfg.ChannelID, msg: s}, nil
	}
	return ref{}, errors.New("not a Teams message ref: " + s)
}

func (r ref) messages() string {
	if r.chat != "" {
		return "/chats/" + url.PathEscape(r.chat) + "/messages"
	}
	return "/teams/" + url.PathEscape(r.team) + "/channels/" + url.PathEscape(r.channel) + "/messages"
}

// Line is one message of a conversation, oldest first, for Show.
type Line struct {
	From  string    `json:"from"`
	At    time.Time `json:"at"`
	Text  string    `json:"text"`
	Files []File    `json:"files,omitempty"`
}

// Show returns the conversation a ref points into: the chat's recent
// messages, or the channel thread.
func (c *Client) Show(s string) ([]Line, error) {
	r, err := c.parseRef(s)
	if err != nil {
		return nil, err
	}
	var msgs []chatMessage
	if r.chat != "" {
		var v struct {
			Value []chatMessage `json:"value"`
		}
		if err := c.getJSON(r.messages()+"?$top=20", &v); err != nil {
			return nil, err
		}
		msgs = v.Value
	} else {
		var root chatMessage
		if err := c.getJSON(r.messages()+"/"+url.PathEscape(r.msg), &root); err != nil {
			return nil, err
		}
		var v struct {
			Value []chatMessage `json:"value"`
		}
		if err := c.getJSON(r.messages()+"/"+url.PathEscape(r.msg)+"/replies?$top=50", &v); err != nil {
			return nil, err
		}
		msgs = append([]chatMessage{root}, v.Value...)
	}
	var out []Line
	for _, m := range msgs {
		if m.human() {
			out = append(out, Line{From: m.From.name(), At: m.Created, Text: plain(m.Body.Content), Files: files(m.Attachments)})
		}
	}
	slices.SortFunc(out, func(a, b Line) int { return a.At.Compare(b.At) })
	return out, nil
}

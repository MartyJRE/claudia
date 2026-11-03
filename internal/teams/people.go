package teams

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

type Person struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// Person finds someone by email or by the start of their name; a name that
// matches several people is an error that lists them.
func (c *Client) Person(who string) (Person, error) {
	var v struct {
		Value []struct {
			ID                string `json:"id"`
			DisplayName       string `json:"displayName"`
			Mail              string `json:"mail"`
			UserPrincipalName string `json:"userPrincipalName"`
		} `json:"value"`
	}
	q := strings.ReplaceAll(who, "'", "''")
	filter := "startswith(displayName,'" + q + "') or startswith(givenName,'" + q + "') or startswith(surname,'" + q + "')"
	if strings.Contains(who, "@") {
		filter = "mail eq '" + q + "' or userPrincipalName eq '" + q + "'"
	}
	if err := c.getJSON("/users?$select=id,displayName,mail,userPrincipalName&$top=10&$filter="+url.QueryEscape(filter), &v); err != nil {
		return Person{}, err
	}
	switch len(v.Value) {
	case 0:
		return Person{}, fmt.Errorf("nobody called %q", who)
	case 1:
		u := v.Value[0]
		mail := u.Mail
		if mail == "" {
			mail = u.UserPrincipalName
		}
		return Person{ID: u.ID, Name: u.DisplayName, Email: mail}, nil
	}
	var names []string
	for _, u := range v.Value {
		names = append(names, u.DisplayName+" <"+u.Mail+">")
	}
	return Person{}, fmt.Errorf("%q could be %s; use their email", who, strings.Join(names, ", "))
}

// Chat opens the chat with these people (one-on-one for one, a group for
// more; Teams reuses an existing one-on-one), sends text and any files, and
// returns the new message's ref.
func (c *Client) Chat(who []string, text string, files ...string) (string, error) {
	if len(who) == 0 {
		return "", errors.New("whom to write to?")
	}
	m, err := c.whoami()
	if err != nil {
		return "", err
	}
	member := func(id string) map[string]any {
		return map[string]any{
			"@odata.type":     "#microsoft.graph.aadUserConversationMember",
			"roles":           []string{"owner"},
			"user@odata.bind": "https://graph.microsoft.com/v1.0/users('" + id + "')",
		}
	}
	members := []map[string]any{member(m.ID)}
	for _, w := range who {
		p, err := c.Person(w)
		if err != nil {
			return "", err
		}
		members = append(members, member(p.ID))
	}
	kind := "oneOnOne"
	if len(members) > 2 {
		kind = "group"
	}
	var ch struct {
		ID string `json:"id"`
	}
	if err := c.getJSONPost("/chats", map[string]any{"chatType": kind, "members": members}, &ch); err != nil {
		return "", err
	}
	return c.sendTo(ref{chat: ch.ID}, "/chats/"+url.PathEscape(ch.ID)+"/messages", text, files)
}

// Post starts a new thread in a channel, named "Team/Channel" or by ids, and
// returns the new message's ref.
func (c *Client) Post(channel, text string, files ...string) (string, error) {
	ch, err := c.findChannel(channel)
	if err != nil {
		return "", err
	}
	r := ref{team: ch.TeamID, channel: ch.ChannelID}
	return c.sendTo(r, r.messages(), text, files)
}

func (c *Client) sendTo(r ref, path, text string, files []string) (string, error) {
	msg, err := c.message(r, text, files)
	if err != nil {
		return "", err
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := c.getJSONPost(path, msg, &out); err != nil {
		return "", err
	}
	if r.chat != "" {
		return "chat/" + r.chat + "/" + out.ID, nil
	}
	return r.team + "/" + r.channel + "/" + out.ID, nil
}

// findChannel takes "Team/Channel" (names, any case) or "<team id>/<channel id>".
func (c *Client) findChannel(s string) (Channel, error) {
	team, name, ok := strings.Cut(s, "/")
	if !ok {
		return Channel{}, errors.New(`name the channel as "Team/Channel"`)
	}
	chs, err := c.cachedChannels()
	if err != nil {
		return Channel{}, err
	}
	for _, ch := range chs {
		if (ch.TeamID == team && ch.ChannelID == name) || (strings.EqualFold(ch.Team, team) && strings.EqualFold(ch.Channel, name)) {
			return ch, nil
		}
	}
	return Channel{}, fmt.Errorf("no channel %q among yours (see `claudia teams channels`)", s)
}

package teams

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/MartyJRE/claudia/internal/config"
)

type address struct {
	EmailAddress struct {
		Name    string `json:"name"`
		Address string `json:"address"`
	} `json:"emailAddress"`
}

type mailMessage struct {
	ID             string    `json:"id"`
	Subject        string    `json:"subject"`
	From           *address  `json:"from"`
	To             []address `json:"toRecipients"`
	Received       time.Time `json:"receivedDateTime"`
	Preview        string    `json:"bodyPreview"`
	WebLink        string    `json:"webLink"`
	Classification string    `json:"inferenceClassification"`
	UniqueBody     struct {
		Content string `json:"content"`
	} `json:"uniqueBody"`
}

func (m mailMessage) sender() string {
	if m.From == nil {
		return ""
	}
	if n := m.From.EmailAddress.Name; n != "" {
		return n
	}
	return m.From.EmailAddress.Address
}

func ruleMatches(r config.MailRule, m mailMessage) bool {
	if r.From == "" && r.Subject == "" {
		return false
	}
	has := func(hay, needle string) bool {
		return needle == "" || strings.Contains(strings.ToLower(hay), strings.ToLower(needle))
	}
	from := ""
	if m.From != nil {
		from = m.From.EmailAddress.Address + " " + m.From.EmailAddress.Name
	}
	return has(from, r.From) && has(m.Subject, r.Subject)
}

// parseMail keeps unread mail that a rule matches, or, with Direct on, that
// has you in To and that Outlook files as Focused, which leaves out
// newsletters and robots. Reading or replying in Outlook marks it read, so it
// leaves the list on its own.
func parseMail(b []byte, mine []string, cfg config.Mail) ([]Waiting, error) {
	var v struct {
		Value []mailMessage `json:"value"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	toMe := func(m mailMessage) bool {
		return slices.ContainsFunc(m.To, func(a address) bool {
			return slices.ContainsFunc(mine, func(x string) bool { return x != "" && strings.EqualFold(x, a.EmailAddress.Address) })
		})
	}
	var out []Waiting
	for _, m := range v.Value {
		keep := cfg.Direct() && toMe(m) && m.Classification == "focused"
		keep = keep || slices.ContainsFunc(cfg.Rules, func(r config.MailRule) bool { return ruleMatches(r, m) })
		if !keep {
			continue
		}
		subject := m.Subject
		if subject == "" {
			subject = "(no subject)"
		}
		out = append(out, Waiting{
			Ref: "mail/" + m.ID, Kind: "mail", Where: subject, From: m.sender(), At: m.Received,
			Text: strings.Join(strings.Fields(m.Preview), " "), WebURL: m.WebLink,
		})
	}
	return out, nil
}

func (c *Client) fetchMail(cfg config.Mail) ([]Waiting, error) {
	m, err := c.whoami()
	if err != nil {
		return nil, err
	}
	q := url.Values{
		"$filter": {"isRead eq false and receivedDateTime ge " + now().Add(-lookback).UTC().Format(time.RFC3339)},
		"$top":    {"50"},
		"$select": {"id,subject,from,toRecipients,receivedDateTime,bodyPreview,webLink,inferenceClassification"},
	}
	b, err := c.do("GET", "/me/mailFolders/inbox/messages?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	return parseMail(b, []string{m.Mail, m.UPN}, cfg)
}

// showMail returns the mail's conversation, oldest first, each message
// without the quoted history below it.
func (c *Client) showMail(id string) ([]Line, error) {
	var head struct {
		ConversationID string `json:"conversationId"`
	}
	if err := c.getJSON("/me/messages/"+url.PathEscape(id)+"?$select=conversationId", &head); err != nil {
		return nil, err
	}
	q := url.Values{
		"$filter": {"conversationId eq '" + strings.ReplaceAll(head.ConversationID, "'", "''") + "'"},
		"$top":    {"50"},
		"$select": {"from,receivedDateTime,uniqueBody"},
	}
	b, err := c.doHeader("GET", "/me/messages?"+q.Encode(), nil, map[string]string{"Prefer": `outlook.body-content-type="text"`})
	if err != nil {
		return nil, err
	}
	var v struct {
		Value []mailMessage `json:"value"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	var out []Line
	for _, m := range v.Value {
		out = append(out, Line{From: m.sender(), At: m.Received, Text: strings.TrimSpace(m.UniqueBody.Content)})
	}
	slices.SortFunc(out, func(a, b Line) int { return a.At.Compare(b.At) })
	return out, nil
}

package teams

import (
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"
)

type Event struct {
	ID        string    `json:"id"`
	Subject   string    `json:"subject"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	Organizer string    `json:"organizer"`
	Response  string    `json:"response"` // none, organizer, accepted, tentativelyAccepted, declined, notResponded
	Online    string    `json:"online,omitempty"`
	WebURL    string    `json:"web_url"`
	Location  string    `json:"location,omitempty"`
}

func (e Event) Ref() string { return "event/" + e.ID }

// graphTime is how calendarView sends times with Prefer: outlook.timezone="UTC".
type graphTime struct {
	DateTime string `json:"dateTime"`
}

func (g graphTime) parse() time.Time {
	t, _ := time.Parse("2006-01-02T15:04:05.9999999", g.DateTime)
	return t.UTC()
}

type event struct {
	ID          string    `json:"id"`
	Subject     string    `json:"subject"`
	Start       graphTime `json:"start"`
	End         graphTime `json:"end"`
	IsCancelled bool      `json:"isCancelled"`
	WebLink     string    `json:"webLink"`
	Organizer   struct {
		EmailAddress struct {
			Name string `json:"name"`
		} `json:"emailAddress"`
	} `json:"organizer"`
	ResponseStatus struct {
		Response string `json:"response"`
	} `json:"responseStatus"`
	OnlineMeeting *struct {
		JoinURL string `json:"joinUrl"`
	} `json:"onlineMeeting"`
	Location struct {
		DisplayName string `json:"displayName"`
	} `json:"location"`
}

func parseEvents(b []byte) ([]Event, error) {
	var v struct {
		Value []event `json:"value"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	var out []Event
	for _, e := range v.Value {
		if e.IsCancelled {
			continue
		}
		ev := Event{
			ID: e.ID, Subject: e.Subject, Start: e.Start.parse(), End: e.End.parse(), Organizer: e.Organizer.EmailAddress.Name,
			Response: e.ResponseStatus.Response, WebURL: e.WebLink, Location: e.Location.DisplayName,
		}
		if e.OnlineMeeting != nil {
			ev.Online = e.OnlineMeeting.JoinURL
		}
		out = append(out, ev)
	}
	slices.SortStableFunc(out, func(a, b Event) int { return a.Start.Compare(b.Start) })
	return out, nil
}

// Calendar lists the events between from and to, cancelled ones left out.
func (c *Client) Calendar(from, to time.Time) ([]Event, error) {
	q := url.Values{
		"startDateTime": {from.UTC().Format(time.RFC3339)},
		"endDateTime":   {to.UTC().Format(time.RFC3339)},
		"$orderby":      {"start/dateTime"},
		"$top":          {"100"},
		"$select":       {"id,subject,start,end,isCancelled,webLink,organizer,responseStatus,onlineMeeting,location"},
	}
	b, err := c.doHeader("GET", "/me/calendarView?"+q.Encode(), nil, map[string]string{"Prefer": `outlook.timezone="UTC"`})
	if err != nil {
		return nil, err
	}
	return parseEvents(b)
}

// invites turns events you haven't answered into waiting items.
func invites(evs []Event) []Waiting {
	var out []Waiting
	for _, e := range evs {
		if e.Response != "notResponded" || e.Start.Before(now()) {
			continue
		}
		out = append(out, Waiting{
			Ref: e.Ref(), Kind: "invite", Where: e.Start.Local().Format("Mon 2 Jan 15:04"), From: e.Organizer,
			At: e.Start, Text: e.Subject, WebURL: e.WebURL,
		})
	}
	return out
}

var responses = map[string]string{"accept": "accept", "tentative": "tentativelyAccept", "decline": "decline"}

// Respond answers an invite: accept, tentative or decline, with an optional
// comment that goes to the organizer.
func (c *Client) Respond(ref, answer, comment string) error {
	id, ok := strings.CutPrefix(ref, "event/")
	if !ok || id == "" {
		return errors.New("not an event ref: " + ref)
	}
	action, ok := responses[answer]
	if !ok {
		return errors.New("answer accept, tentative or decline, not " + answer)
	}
	_, err := c.do("POST", "/me/events/"+url.PathEscape(id)+"/"+action, map[string]any{"comment": comment, "sendResponse": true})
	return err
}

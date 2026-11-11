// Package standup writes the talking points for standup: what I did since the
// last one, what I'm doing today, and what's blocked.
package standup

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/MartyJRE/claudia/internal/brief"
	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/state"
	"github.com/MartyJRE/claudia/internal/teams"
)

// Lookback is how far back the calendar is read for the last standup.
const Lookback = 5 * 24 * time.Hour

// Since is when the last standup began: the latest meeting before now whose
// title has the configured word, else the previous working day at standup time.
func Since(events []teams.Event, now time.Time, c config.Standup) time.Time {
	var last time.Time
	for _, e := range events {
		s := e.Start.Local()
		if s.Before(now) && s.After(last) && e.Response != "declined" && strings.Contains(strings.ToLower(e.Subject), c.MeetingWord()) {
			last = s
		}
	}
	if !last.IsZero() {
		return last
	}
	h, m := c.At()
	day := now
	for {
		day = day.AddDate(0, 0, -1)
		if wd := day.Weekday(); wd != time.Saturday && wd != time.Sunday {
			break
		}
	}
	y, mo, d := day.Date()
	return time.Date(y, mo, d, h, m, 0, 0, now.Location())
}

const instructions = `You write a developer's standup talking points. They read them out at standup, so keep them short and plain: a few bullets per section, each a phrase they can say, with ticket keys where they help.

You get JSON facts gathered since the last standup: runs of their Claude steps, MRs opened, merged and reviewed, todos done, sessions worked in, the order agreed for today, cards in progress, today's meetings, and cards waiting on someone else with since when.

Write exactly three sections with these headings, in this order:
Yesterday
Today
Blocked

Under each, "- " bullets. Merge facts about the same ticket into one bullet. Skip noise (failed retries that later passed, sessions with nothing to show). Today follows the agreed order first. Under Blocked say who or what it waits on and for how long; write "- nothing" when there's nothing. Write in %s. Plain text, no Markdown beyond the bullets.`

// Write turns facts into talking points with a cheap headless run.
func Write(facts any, lang, configDir string) (string, error) {
	b, err := json.MarshalIndent(facts, "", " ")
	if err != nil {
		return "", err
	}
	return headless("standup", strings.Replace(instructions, "%s", lang, 1), string(b), configDir, "--model", "haiku")
}

var headless = brief.Headless

const file = "standup.json"

// Points are the last talking points written.
type Points struct {
	At    time.Time `json:"at"`
	Since time.Time `json:"since"`
	Text  string    `json:"text"`
	Err   string    `json:"error,omitempty"`
}

func Load() (Points, bool) {
	var p Points
	return p, state.Load(file, &p)
}

func Save(p Points) error { return state.Save(file, p) }

package config

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Autopilot lets the watcher start the Board's steps on its own, within limits.
type Autopilot struct {
	// Enabled turns it on; P on the Board pauses it without touching this.
	Enabled bool `json:"enabled"`
	// Steps sets each step's mode: auto, after-plan-ok (implement only), ask or off.
	Steps map[string]string `json:"steps"`
	// AllowAutoMerge is what lets merge be auto, here or on a card.
	AllowAutoMerge bool `json:"allow_auto_merge"`
	// ReviewApproveRaw: approve an MR whose review came back clean; default on.
	ReviewApproveRaw *bool `json:"review_approve"`
	// ReviewReactRaw is the emoji put on the MR's Teams post then; "" for none.
	ReviewReactRaw *string `json:"review_react"`
	// Hours and Days are when new runs may start, e.g. "09:00-18:00" and
	// ["mon", "tue"]; Monday to Friday, nine to six by default.
	Hours string   `json:"hours"`
	Days  []string `json:"days"`
	// MaxRunningRaw caps the runs going at once, mine included; default 1.
	MaxRunningRaw *int `json:"max_running"`
	// Plan usage, in percent, above which nothing new starts. The after-hours
	// cap applies to a 5h window that resets after the working hours end.
	MaxFiveHourRaw           *float64 `json:"max_five_hour"`
	MaxFiveHourAfterHoursRaw *float64 `json:"max_five_hour_after_hours"`
	MaxSevenDayRaw           *float64 `json:"max_seven_day"`
	// Dollars a ticket may cost before its steps stop starting on their own,
	// and what the autopilot's runs may cost in a day.
	MaxTicketUSDRaw *float64 `json:"max_ticket_usd"`
	MaxDayUSDRaw    *float64 `json:"max_day_usd"`
	// ReviewEveryRaw paces reviews: at most one started per period; default 1h.
	ReviewEveryRaw string `json:"review_every"`
	// ReviewStaleAfterRaw: a review request with no MR activity for this long
	// isn't reviewed on its own; default 720h (30 days).
	ReviewStaleAfterRaw string `json:"review_stale_after"`
	// PermissionModeRaw is the --permission-mode of the runs it starts:
	// default (your usual), acceptEdits, auto (the default) or bypassPermissions.
	PermissionModeRaw string `json:"permission_mode"`
	// CommentNoteRaw heads what a run posts to Jira, Confluence or GitLab.
	CommentNoteRaw string `json:"comment_note"`
}

const defaultCommentNote = "Written by Claude, an AI assistant, on my behalf — reply here as usual."

// CommentNote is the sentence a run's posts start with, so colleagues know who wrote them.
func (a Autopilot) CommentNote() string {
	if s := strings.TrimSpace(a.CommentNoteRaw); s != "" {
		return s
	}
	return defaultCommentNote
}

var PermissionModes = []string{"default", "acceptEdits", "auto", "bypassPermissions"}

// PermissionMode is the mode autopilot runs start in; "default" passes no flag.
func (a Autopilot) PermissionMode() string {
	if slices.Contains(PermissionModes, a.PermissionModeRaw) {
		return a.PermissionModeRaw
	}
	return "auto"
}

// The modes a step can be in.
const (
	ModeAuto        = "auto"
	ModeAfterPlanOK = "after-plan-ok"
	ModeAsk         = "ask"
	ModeOff         = "off"
)

var Modes = []string{ModeAuto, ModeAfterPlanOK, ModeAsk, ModeOff}

// AutoSteps are the steps the autopilot knows, with their default modes.
var AutoSteps = []string{"readiness", "plan", "implement", "mr", "feedback", "merge", "qa", "review"}

var defaultModes = map[string]string{
	"readiness": ModeAuto, "plan": ModeAuto, "implement": ModeAfterPlanOK, "mr": ModeAuto,
	"feedback": ModeAuto, "merge": ModeAsk, "qa": ModeAuto, "review": ModeAuto,
}

// Mode is the step's mode in the config, or its default.
func (a Autopilot) Mode(step string) string {
	if m := a.Steps[step]; ValidMode(step, m) == nil && m != "" {
		return m
	}
	if m, ok := defaultModes[step]; ok {
		return m
	}
	return ModeAsk
}

// ValidMode: one of Modes, and after-plan-ok only on implement.
func ValidMode(step, mode string) error {
	switch {
	case mode == "":
		return nil
	case !slices.Contains(Modes, mode):
		return fmt.Errorf("%q isn't one of %s", mode, strings.Join(Modes, ", "))
	case mode == ModeAfterPlanOK && step != "implement":
		return fmt.Errorf("%s only applies to implement", ModeAfterPlanOK)
	}
	return nil
}

func (a Autopilot) ReviewApprove() bool { return a.ReviewApproveRaw == nil || *a.ReviewApproveRaw }

func (a Autopilot) ReviewReact() string {
	if a.ReviewReactRaw == nil {
		return "✅"
	}
	return strings.TrimSpace(*a.ReviewReactRaw)
}

func (a Autopilot) MaxRunning() int {
	if a.MaxRunningRaw == nil || *a.MaxRunningRaw < 1 {
		return 1
	}
	return *a.MaxRunningRaw
}

func or(v *float64, def float64) float64 {
	if v == nil {
		return def
	}
	return *v
}

func (a Autopilot) MaxFiveHour() float64           { return or(a.MaxFiveHourRaw, 60) }
func (a Autopilot) MaxFiveHourAfterHours() float64 { return or(a.MaxFiveHourAfterHoursRaw, 90) }
func (a Autopilot) MaxSevenDay() float64           { return or(a.MaxSevenDayRaw, 80) }
func (a Autopilot) MaxTicketUSD() float64          { return or(a.MaxTicketUSDRaw, 50) }
func (a Autopilot) MaxDayUSD() float64             { return or(a.MaxDayUSDRaw, 30) }

// ReviewEvery is 0 for off: no pacing.
func (a Autopilot) ReviewEvery() time.Duration {
	if a.ReviewEveryRaw == "off" {
		return 0
	}
	if d, err := time.ParseDuration(a.ReviewEveryRaw); err == nil && d >= 0 {
		return d
	}
	return time.Hour
}

// ReviewStaleAfter is 0 for off: every review request counts as live.
func (a Autopilot) ReviewStaleAfter() time.Duration {
	if a.ReviewStaleAfterRaw == "off" {
		return 0
	}
	if d, err := time.ParseDuration(a.ReviewStaleAfterRaw); err == nil && d >= 0 {
		return d
	}
	return 30 * 24 * time.Hour
}

const defaultHours = "09:00-18:00"

// WorkHours are the minutes after midnight new runs may start between.
func (a Autopilot) WorkHours() (from, to int) {
	from, to, err := parseHours(a.Hours)
	if err != nil {
		from, to, _ = parseHours(defaultHours)
	}
	return from, to
}

func parseHours(s string) (int, int, error) {
	if strings.TrimSpace(s) == "" {
		s = defaultHours
	}
	a, b, ok := strings.Cut(s, "-")
	if !ok {
		return 0, 0, fmt.Errorf("%q isn't like 09:00-18:00", s)
	}
	var mins [2]int
	for i, part := range []string{a, b} {
		t, err := time.Parse("15:04", strings.TrimSpace(part))
		if err != nil {
			return 0, 0, fmt.Errorf("%q isn't like 09:00-18:00", s)
		}
		mins[i] = t.Hour()*60 + t.Minute()
	}
	if mins[0] >= mins[1] {
		return 0, 0, fmt.Errorf("%q ends before it starts", s)
	}
	return mins[0], mins[1], nil
}

var dayNames = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// Workday: new runs may start on that day.
func (a Autopilot) Workday(d time.Weekday) bool {
	if len(a.Days) == 0 {
		return d != time.Saturday && d != time.Sunday
	}
	return slices.ContainsFunc(a.Days, func(s string) bool { return dayIndex(s) == int(d) })
}

func dayIndex(s string) int {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) < 3 {
		return -1
	}
	return slices.Index(dayNames, s[:3])
}

// CheckAutopilot reports the autopilot settings claudia would misread.
func CheckAutopilot(a Autopilot) error {
	for step, mode := range a.Steps {
		if !slices.Contains(AutoSteps, step) {
			return fmt.Errorf("autopilot.steps: %q isn't a step: %s", step, strings.Join(AutoSteps, ", "))
		}
		if err := ValidMode(step, mode); err != nil {
			return fmt.Errorf("autopilot.steps.%s: %w", step, err)
		}
	}
	if _, _, err := parseHours(a.Hours); err != nil {
		return fmt.Errorf("autopilot.hours: %w", err)
	}
	for _, d := range a.Days {
		if dayIndex(d) < 0 {
			return fmt.Errorf("autopilot.days: %q isn't a day like mon or tuesday", d)
		}
	}
	if v := a.PermissionModeRaw; v != "" && !slices.Contains(PermissionModes, v) {
		return fmt.Errorf("autopilot.permission_mode: %q isn't one of %s", v, strings.Join(PermissionModes, ", "))
	}
	if err := CheckDuration("autopilot.review_every", a.ReviewEveryRaw); err != nil {
		return err
	}
	return CheckDuration("autopilot.review_stale_after", a.ReviewStaleAfterRaw)
}

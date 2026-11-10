package autopilot

import (
	"fmt"
	"time"

	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/config"
)

// InHours: new runs may start now.
// offHours is the pause reason outside working hours.
const offHours = "outside working hours"

func InHours(a config.Autopilot, now time.Time) bool {
	from, to := a.WorkHours()
	m := now.Hour()*60 + now.Minute()
	return a.Workday(now.Weekday()) && m >= from && m < to
}

// hoursEnd is when today's working hours end.
func hoursEnd(a config.Autopilot, now time.Time) time.Time {
	_, to := a.WorkHours()
	y, mo, d := now.Date()
	return time.Date(y, mo, d, 0, 0, 0, 0, now.Location()).Add(time.Duration(to) * time.Minute)
}

func startOfDay(now time.Time) time.Time {
	y, mo, d := now.Date()
	return time.Date(y, mo, d, 0, 0, 0, 0, now.Location())
}

// usage reads the plan windows the status line saved: why they stop new runs,
// and whether they're known at all. Data older than a window, or a window
// that has reset since, says nothing about now.
func usage(a config.Autopilot, l claude.Limits, ok bool, now time.Time) (reason string, known bool) {
	fresh := func(w *claude.Window, span time.Duration) bool {
		return ok && w != nil && now.Sub(l.At) < span && w.Resets().After(now)
	}
	five, seven := fresh(l.FiveHour, 5*time.Hour), fresh(l.SevenDay, 7*24*time.Hour)
	if five {
		max := a.MaxFiveHour()
		// What's left of a window that resets after hours would go unused.
		if !l.FiveHour.Resets().Before(hoursEnd(a, now)) {
			max = a.MaxFiveHourAfterHours()
		}
		if l.FiveHour.UsedPercentage >= max {
			return fmt.Sprintf("5h window %.0f%%", l.FiveHour.UsedPercentage), true
		}
	}
	if seven && l.SevenDay.UsedPercentage >= a.MaxSevenDay() {
		return fmt.Sprintf("7d window %.0f%%", l.SevenDay.UsedPercentage), true
	}
	return "", five && seven
}

// limit is why nothing new may start now, how many runs may go at once, and
// whether the plan usage is known; unknown allows one.
func limit(a config.Autopilot, now time.Time, l claude.Limits, ok bool, dayUSD float64) (string, int, bool) {
	reason, known := usage(a, l, ok, now)
	max := a.MaxRunning()
	if !known {
		max = 1
	}
	switch {
	case !InHours(a, now):
		reason = offHours
	case dayUSD >= a.MaxDayUSD():
		reason = fmt.Sprintf("spent $%.0f of $%.0f today", dayUSD, a.MaxDayUSD())
	}
	return reason, max, known
}

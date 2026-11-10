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

// limitsFresh is how old the saved plan usage may be: the status line only
// writes it while a session of that profile is open, so older data may be far
// behind.
const limitsFresh = 15 * time.Minute

// usage reads the plan windows the status line saved: why they stop new runs,
// and whether they're known at all. Old data, or a window that has reset
// since, says nothing about now.
func usage(a config.Autopilot, l claude.Limits, ok bool, now time.Time) (reason string, known bool) {
	fresh := func(w *claude.Window) bool {
		return ok && w != nil && now.Sub(l.At) < limitsFresh && w.Resets().After(now)
	}
	five, seven := fresh(l.FiveHour), fresh(l.SevenDay)
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

// limit is why nothing new may start now, whatever profile it would run on.
func limit(a config.Autopilot, now time.Time, dayUSD float64) string {
	switch {
	case !InHours(a, now):
		return offHours
	case dayUSD >= a.MaxDayUSD():
		return fmt.Sprintf("spent $%.0f of $%.0f today", dayUSD, a.MaxDayUSD())
	}
	return ""
}

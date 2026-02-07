package timeutil

import (
	"fmt"
	"time"
)

const weeklyResetLocation = "America/Los_Angeles"

func Location() *time.Location {
	loc, err := time.LoadLocation(weeklyResetLocation)
	if err != nil {
		return time.FixedZone("America/Los_Angeles", -8*3600)
	}
	return loc
}

// WeeklyResetHour is the hour when WoW weekly reset occurs (7am PST).
const WeeklyResetHour = 7

func WeeklyReset() time.Time {
	return WeeklyResetAt(time.Now())
}

func WeeklyResetAt(now time.Time) time.Time {
	return WeeklyResetIn(now, Location())
}

func WeeklyResetIn(now time.Time, loc *time.Location) time.Time {
	n := now.In(loc)
	diff := (int(n.Weekday()) - int(time.Tuesday) + 7) % 7
	tuesday := n.AddDate(0, 0, -diff)
	reset := time.Date(tuesday.Year(), tuesday.Month(), tuesday.Day(), WeeklyResetHour, 0, 0, 0, loc)
	if n.Before(reset) {
		reset = reset.AddDate(0, 0, -7)
	}
	return reset
}

// LastTuesday9AM is deprecated. Use WeeklyReset instead.
func LastTuesday9AM() time.Time {
	return WeeklyReset()
}

// LastTuesday9AMAt is deprecated. Use WeeklyResetAt instead.
func LastTuesday9AMAt(now time.Time) time.Time {
	return WeeklyResetAt(now)
}

// LastTuesday9AMIn is deprecated. Use WeeklyResetIn instead.
func LastTuesday9AMIn(now time.Time, loc *time.Location) time.Time {
	return WeeklyResetIn(now, loc)
}

func ParseRFC3339(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, fmt.Errorf("empty time")
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, value)
}

func WithinLastHour(t time.Time) bool {
	return WithinLastHourAt(t, time.Now())
}

func WithinLastHourAt(t time.Time, now time.Time) bool {
	return t.After(now.Add(-1 * time.Hour))
}

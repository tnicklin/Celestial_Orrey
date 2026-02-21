package clock

import "time"

// Clock provides wall-clock time. Implementations may correct for
// system clock drift (e.g. via NTP).
type Clock interface {
	Now() time.Time
}

// System returns a Clock backed by time.Now().
func System() Clock { return systemClock{} }

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

package clock

import (
	"testing"
	"time"
)

func TestSystemClock(t *testing.T) {
	c := System()
	before := time.Now()
	got := c.Now()
	after := time.Now()

	if got.Before(before) || got.After(after) {
		t.Fatalf("System().Now() = %v, want between %v and %v", got, before, after)
	}
}

func TestNTPClock_ZeroOffset(t *testing.T) {
	c := NewNTP()

	before := time.Now()
	got := c.Now()
	after := time.Now()

	if got.Before(before.Add(-time.Millisecond)) || got.After(after.Add(time.Millisecond)) {
		t.Fatalf("NTPClock.Now() with zero offset = %v, want ~time.Now()", got)
	}
}

func TestNTPClock_ManualOffset(t *testing.T) {
	c := NewNTP()

	c.mu.Lock()
	c.offset = 5 * time.Second
	c.mu.Unlock()

	before := time.Now().Add(5 * time.Second)
	got := c.Now()
	after := time.Now().Add(5 * time.Second)

	if got.Before(before.Add(-time.Millisecond)) || got.After(after.Add(time.Millisecond)) {
		t.Fatalf("NTPClock.Now() with +5s offset = %v, want ~%v", got, before)
	}

	if off := c.Offset(); off != 5*time.Second {
		t.Fatalf("Offset() = %v, want 5s", off)
	}
}

func TestNTPClock_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping NTP integration test in -short mode")
	}

	c := NewNTP(WithTimeout(10 * time.Second))

	c.sync()

	off := c.Offset()
	// A healthy system clock should be within 1 second of NTP.
	if off > time.Second || off < -time.Second {
		t.Logf("WARNING: system clock offset from NTP is %v", off)
	}

	got := c.Now()
	wall := time.Now()
	diff := got.Sub(wall)
	if diff < 0 {
		diff = -diff
	}
	// With any reasonable offset the difference should be small.
	if diff > 2*time.Second {
		t.Fatalf("NTPClock.Now() differs from time.Now() by %v after sync", diff)
	}
}

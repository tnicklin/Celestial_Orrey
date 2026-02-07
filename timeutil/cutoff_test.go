package timeutil

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T) *time.Location {
	loc, err := time.LoadLocation(weeklyResetLocation)
	if err != nil {
		t.Fatalf("failed to load location: %v", err)
	}
	return loc
}

func TestWeeklyResetIn(t *testing.T) {
	loc := mustLoc(t)

	cases := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{
			name: "wednesday",
			now:  time.Date(2026, 2, 4, 12, 0, 0, 0, loc),
			want: time.Date(2026, 2, 3, 7, 0, 0, 0, loc),
		},
		{
			name: "tuesday_after_reset",
			now:  time.Date(2026, 2, 3, 10, 0, 0, 0, loc),
			want: time.Date(2026, 2, 3, 7, 0, 0, 0, loc),
		},
		{
			name: "tuesday_before_reset",
			now:  time.Date(2026, 2, 3, 6, 0, 0, 0, loc),
			want: time.Date(2026, 1, 27, 7, 0, 0, 0, loc),
		},
		{
			name: "monday",
			now:  time.Date(2026, 2, 2, 10, 0, 0, 0, loc),
			want: time.Date(2026, 1, 27, 7, 0, 0, 0, loc),
		},
		{
			name: "post_dst_start",
			now:  time.Date(2026, 3, 10, 12, 0, 0, 0, loc),
			want: time.Date(2026, 3, 10, 7, 0, 0, 0, loc),
		},
	}

	for _, tc := range cases {
		got := WeeklyResetIn(tc.now, loc)
		if !got.Equal(tc.want) {
			t.Fatalf("%s: expected %s, got %s", tc.name, tc.want, got)
		}
	}
}

func TestLegacyLastTuesday9AM(t *testing.T) {
	// Verify deprecated functions still work
	loc := mustLoc(t)
	now := time.Date(2026, 2, 4, 12, 0, 0, 0, loc)
	got := LastTuesday9AMIn(now, loc)
	want := time.Date(2026, 2, 3, 7, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestParseRFC3339(t *testing.T) {
	cases := []string{
		"2026-02-01T01:23:45Z",
		"2026-02-01T01:23:45.123Z",
		"2026-02-01T01:23:45.123456789Z",
	}
	for _, value := range cases {
		if _, err := ParseRFC3339(value); err != nil {
			t.Fatalf("expected parse to succeed for %s: %v", value, err)
		}
	}
}

func TestWithinLastHourAt(t *testing.T) {
	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	inside := now.Add(-30 * time.Minute)
	outside := now.Add(-2 * time.Hour)

	if !WithinLastHourAt(inside, now) {
		t.Fatalf("expected inside to be within last hour")
	}
	if WithinLastHourAt(outside, now) {
		t.Fatalf("expected outside to be outside last hour")
	}
}

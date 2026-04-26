package simc

import "testing"

func TestNChoose2(t *testing.T) {
	tests := []struct {
		n    int
		want int
	}{
		{0, 0}, {1, 0}, {2, 1}, {3, 3}, {5, 10}, {10, 45},
	}
	for _, tt := range tests {
		if got := nChoose2(tt.n); got != tt.want {
			t.Errorf("C(%d,2) = %d, want %d", tt.n, got, tt.want)
		}
	}
}

func TestAnalyzeCandidates_EmptyAndDoubleEmpty(t *testing.T) {
	cands := map[Slot][]Item{
		SlotHead:    {{ItemID: 1, Track: TrackHero}},
		SlotFinger:  {{ItemID: 100, Track: TrackHero}}, // only 1, double-empty
		SlotTrinket: {},                                // double-empty (no items)
	}
	stats := AnalyzeCandidates(cands)
	if stats.Total == 0 {
		t.Errorf("total should be > 0")
	}
	foundDoubleFinger := false
	foundDoubleTrinket := false
	for _, s := range stats.DoubleEmpty {
		if s == SlotFinger {
			foundDoubleFinger = true
		}
		if s == SlotTrinket {
			foundDoubleTrinket = true
		}
	}
	if !foundDoubleFinger || !foundDoubleTrinket {
		t.Errorf("DoubleEmpty = %v, want both finger and trinket", stats.DoubleEmpty)
	}
}

func TestLoadoutFingerprint_Stable(t *testing.T) {
	a := Loadout{Items: map[Slot][]Item{
		SlotFinger: {
			{ItemID: 100, Track: TrackHero},
			{ItemID: 200, Track: TrackHero},
		},
	}}
	b := Loadout{Items: map[Slot][]Item{
		SlotFinger: {
			{ItemID: 200, Track: TrackHero},
			{ItemID: 100, Track: TrackHero},
		},
	}}
	if a.fingerprint() != b.fingerprint() {
		t.Errorf("ring order should not affect fingerprint:\na=%s\nb=%s", a.fingerprint(), b.fingerprint())
	}
}

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

func TestGenerateCombinations_CountMatchesAnalysis(t *testing.T) {
	// 2 head × 1 chest × C(3,2) finger × C(2,2) trinket = 2 * 1 * 3 * 1 = 6
	cands := map[Slot][]Item{
		SlotHead: {
			{ItemID: 1, Track: TrackHero},
			{ItemID: 2, Track: TrackHero},
		},
		SlotChest: {{ItemID: 10, Track: TrackMyth}},
		SlotFinger: {
			{ItemID: 100, Track: TrackHero},
			{ItemID: 101, Track: TrackHero},
			{ItemID: 102, Track: TrackMyth},
		},
		SlotTrinket: {
			{ItemID: 200, Track: TrackHero},
			{ItemID: 201, Track: TrackMyth},
		},
	}
	combos, stats, err := GenerateCombinations(cands, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(combos) != stats.Total {
		t.Errorf("len(combos) = %d, stats.Total = %d", len(combos), stats.Total)
	}
	if stats.Total != 6 {
		t.Errorf("total = %d, want 6", stats.Total)
	}
}

func TestGenerateCombinations_RespectsCap(t *testing.T) {
	cands := map[Slot][]Item{
		SlotHead: {
			{ItemID: 1, Track: TrackHero},
			{ItemID: 2, Track: TrackHero},
			{ItemID: 3, Track: TrackHero},
		},
	}
	_, _, err := GenerateCombinations(cands, 2)
	if err == nil {
		t.Fatal("expected error when exceeding cap")
	}
}

func TestGenerateCombinations_FingerPairs(t *testing.T) {
	cands := map[Slot][]Item{
		SlotFinger: {
			{ItemID: 1, Track: TrackHero},
			{ItemID: 2, Track: TrackHero},
			{ItemID: 3, Track: TrackHero},
		},
	}
	combos, _, err := GenerateCombinations(cands, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(combos) != 3 {
		t.Fatalf("len(combos) = %d, want 3", len(combos))
	}
	for _, c := range combos {
		if got := len(c.Items[SlotFinger]); got != 2 {
			t.Errorf("finger pair size = %d, want 2", got)
		}
	}
}

func TestGenerateCombinations_DoubleSlotSingle(t *testing.T) {
	cands := map[Slot][]Item{
		SlotFinger: {{ItemID: 1, Track: TrackHero}},
	}
	combos, _, err := GenerateCombinations(cands, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(combos) != 1 {
		t.Fatalf("len(combos) = %d, want 1", len(combos))
	}
	if got := len(combos[0].Items[SlotFinger]); got != 1 {
		t.Errorf("finger items = %d, want 1", got)
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

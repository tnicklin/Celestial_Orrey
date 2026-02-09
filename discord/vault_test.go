package discord

import (
	"testing"
)

func TestVaultRewardTable_GetThreshold(t *testing.T) {
	tests := []struct {
		name      string
		keyLevel  int
		wantILvl  int
		wantTrack string
		wantIsMH  bool
	}{
		{
			name:      "max key level",
			keyLevel:  18,
			wantILvl:  282,
			wantTrack: "Myth 4/6",
			wantIsMH:  true,
		},
		{
			name:      "myth 3",
			keyLevel:  15,
			wantILvl:  279,
			wantTrack: "Myth 3/6",
			wantIsMH:  true,
		},
		{
			name:      "myth 2",
			keyLevel:  12,
			wantILvl:  276,
			wantTrack: "Myth 2/6",
			wantIsMH:  true,
		},
		{
			name:      "myth 1",
			keyLevel:  10,
			wantILvl:  272,
			wantTrack: "Myth 1/6",
			wantIsMH:  true,
		},
		{
			name:      "hero 4",
			keyLevel:  7,
			wantILvl:  269,
			wantTrack: "Hero 4/6",
			wantIsMH:  false,
		},
		{
			name:      "hero 3",
			keyLevel:  6,
			wantILvl:  266,
			wantTrack: "Hero 3/6",
			wantIsMH:  false,
		},
		{
			name:      "hero 2",
			keyLevel:  4,
			wantILvl:  263,
			wantTrack: "Hero 2/6",
			wantIsMH:  false,
		},
		{
			name:      "hero 1",
			keyLevel:  2,
			wantILvl:  259,
			wantTrack: "Hero 1/6",
			wantIsMH:  false,
		},
		{
			name:      "key level 1 uses lowest threshold",
			keyLevel:  1,
			wantILvl:  259,
			wantTrack: "Hero 1/6",
			wantIsMH:  false,
		},
		{
			name:      "above max caps at max",
			keyLevel:  25,
			wantILvl:  282,
			wantTrack: "Myth 4/6",
			wantIsMH:  true,
		},
		{
			name:      "between thresholds (11)",
			keyLevel:  11,
			wantILvl:  272,
			wantTrack: "Myth 1/6",
			wantIsMH:  true,
		},
		{
			name:      "between thresholds (5)",
			keyLevel:  5,
			wantILvl:  263,
			wantTrack: "Hero 2/6",
			wantIsMH:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			threshold := VaultRewardsSeason1.GetThreshold(tt.keyLevel)
			if threshold.ItemLevel != tt.wantILvl {
				t.Errorf("GetThreshold(%d).ItemLevel = %d, want %d", tt.keyLevel, threshold.ItemLevel, tt.wantILvl)
			}
			if threshold.Track != tt.wantTrack {
				t.Errorf("GetThreshold(%d).Track = %q, want %q", tt.keyLevel, threshold.Track, tt.wantTrack)
			}
			if threshold.IsMythTrack != tt.wantIsMH {
				t.Errorf("GetThreshold(%d).IsMythTrack = %v, want %v", tt.keyLevel, threshold.IsMythTrack, tt.wantIsMH)
			}
		})
	}
}

func TestVaultRewardTable_GetItemLevel(t *testing.T) {
	tests := []struct {
		name     string
		keyLevel int
		wantILvl int
	}{
		{
			name:     "max key level",
			keyLevel: 18,
			wantILvl: 282,
		},
		{
			name:     "myth 1",
			keyLevel: 10,
			wantILvl: 272,
		},
		{
			name:     "hero 1",
			keyLevel: 2,
			wantILvl: 259,
		},
		{
			name:     "key level 1 uses lowest threshold",
			keyLevel: 1,
			wantILvl: 259,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := VaultRewardsSeason1.GetItemLevel(tt.keyLevel)
			if got != tt.wantILvl {
				t.Errorf("GetItemLevel(%d) = %d, want %d", tt.keyLevel, got, tt.wantILvl)
			}
		})
	}
}

func TestVaultRewardTable_GetVaultSlotDisplay(t *testing.T) {
	tests := []struct {
		name     string
		keyLevel int
		wantLen  int // expect non-empty string
	}{
		{
			name:     "max key level",
			keyLevel: 18,
			wantLen:  1,
		},
		{
			name:     "low key level",
			keyLevel: 2,
			wantLen:  1,
		},
		{
			name:     "zero key level returns empty slot",
			keyLevel: 0,
			wantLen:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := VaultRewards.GetVaultSlotDisplay(tt.keyLevel)
			if len(got) < tt.wantLen {
				t.Errorf("GetVaultSlotDisplay(%d) = %q, want non-empty string", tt.keyLevel, got)
			}
		})
	}
}

func TestEmptySlotDisplay(t *testing.T) {
	display := EmptySlotDisplay()
	if display == "" {
		t.Error("EmptySlotDisplay() returned empty string")
	}
}

func TestEmptySlotDisplayColored(t *testing.T) {
	display := EmptySlotDisplayColored()
	if display == "" {
		t.Error("EmptySlotDisplayColored() returned empty string")
	}
}

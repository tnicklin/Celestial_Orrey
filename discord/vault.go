package discord

import "fmt"

// Discord ANSI escape codes for code blocks
const (
	ansiReset  = "\033[0m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiGray   = "\033[90m"
)

var VaultRewards = VaultRewardTable{
	Season:      "Midnight Season 1",
	MaxKeyLevel: 18,

	Thresholds: []VaultThreshold{
		{18, 282, "Myth 4/6", "M4", true},
		{15, 279, "Myth 3/6", "M3", true},
		{12, 276, "Myth 2/6", "M2", true},
		{10, 272, "Myth 1/6", "M1", true},
		{7, 269, "Hero 4/6", "H4", false},
		{6, 266, "Hero 3/6", "H3", false},
		{4, 263, "Hero 2/6", "H2", false},
		{2, 259, "Hero 1/6", "H1", false},
	},
	DefaultItemLevel:   259,
	DefaultShortCode:   "H1",
	DefaultIsMythTrack: false,
}

// VaultRewardTable holds the complete vault reward configuration for a season.
type VaultRewardTable struct {
	Season             string
	MaxKeyLevel        int
	Thresholds         []VaultThreshold
	DefaultItemLevel   int
	DefaultShortCode   string
	DefaultIsMythTrack bool
}

// VaultThreshold represents a single reward tier.
type VaultThreshold struct {
	MinKeyLevel int
	ItemLevel   int
	Track       string
	ShortCode   string
	IsMythTrack bool
}

// GetItemLevel returns the vault item level reward for a given key level.
func (v VaultRewardTable) GetItemLevel(keyLevel int) int {
	for _, t := range v.Thresholds {
		if keyLevel >= t.MinKeyLevel {
			return t.ItemLevel
		}
	}
	return v.DefaultItemLevel
}

// GetThreshold returns the full threshold info for a given key level.
func (v VaultRewardTable) GetThreshold(keyLevel int) VaultThreshold {
	for _, t := range v.Thresholds {
		if keyLevel >= t.MinKeyLevel {
			return t
		}
	}
	return VaultThreshold{
		MinKeyLevel: 0,
		ItemLevel:   v.DefaultItemLevel,
		Track:       "Hero 1/6",
		ShortCode:   v.DefaultShortCode,
		IsMythTrack: v.DefaultIsMythTrack,
	}
}

// GetTrack returns the upgrade track name for a given key level.
func (v VaultRewardTable) GetTrack(keyLevel int) string {
	return v.GetThreshold(keyLevel).Track
}

// GetVaultSlotDisplay returns a formatted string for displaying in the vault progress.
// Format: [282 M4]
func (v VaultRewardTable) GetVaultSlotDisplay(keyLevel int) string {
	t := v.GetThreshold(keyLevel)
	return fmt.Sprintf("[%d %s]", t.ItemLevel, t.ShortCode)
}

// GetVaultSlotDisplayColored returns a colored display for ANSI code blocks.
// Green for Myth track, Yellow for Hero track.
func (v VaultRewardTable) GetVaultSlotDisplayColored(keyLevel int) string {
	t := v.GetThreshold(keyLevel)
	slot := fmt.Sprintf("[%d %s]", t.ItemLevel, t.ShortCode)
	if t.IsMythTrack {
		return ansiGreen + slot + ansiReset
	}
	return ansiYellow + slot + ansiReset
}

// EmptySlotDisplay returns the display for an empty vault slot.
func EmptySlotDisplay() string {
	return "[      ]"
}

// EmptySlotDisplayColored returns the colored display for an empty vault slot.
func EmptySlotDisplayColored() string {
	return ansiGray + "[      ]" + ansiReset
}

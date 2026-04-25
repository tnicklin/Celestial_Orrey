package simc

// Slot is a gear slot the BiB orchestrator permutes over.
type Slot int

const (
	SlotUnknown Slot = iota
	SlotHead
	SlotNeck
	SlotShoulders
	SlotBack
	SlotChest
	SlotWrists
	SlotHands
	SlotWaist
	SlotLegs
	SlotFeet
	SlotFinger
	SlotTrinket
	SlotMainHand
	SlotOffHand
)

// String returns the canonical simc slot keyword.
func (s Slot) String() string {
	switch s {
	case SlotHead:
		return "head"
	case SlotNeck:
		return "neck"
	case SlotShoulders:
		return "shoulders"
	case SlotBack:
		return "back"
	case SlotChest:
		return "chest"
	case SlotWrists:
		return "wrists"
	case SlotHands:
		return "hands"
	case SlotWaist:
		return "waist"
	case SlotLegs:
		return "legs"
	case SlotFeet:
		return "feet"
	case SlotFinger:
		return "finger"
	case SlotTrinket:
		return "trinket"
	case SlotMainHand:
		return "main_hand"
	case SlotOffHand:
		return "off_hand"
	}
	return "unknown"
}

// IsDoubleSlot reports whether the slot can hold two distinct items
// (rings, trinkets).
func (s Slot) IsDoubleSlot() bool {
	return s == SlotFinger || s == SlotTrinket
}

// Track is the upgrade track encoded in the addon dump's per-item comment.
type Track int

const (
	TrackUnknown Track = iota
	TrackExplorer
	TrackAdventurer
	TrackVeteran
	TrackChampion
	TrackHero
	TrackMyth
)

// String returns the canonical track name.
func (t Track) String() string {
	switch t {
	case TrackExplorer:
		return "Explorer"
	case TrackAdventurer:
		return "Adventurer"
	case TrackVeteran:
		return "Veteran"
	case TrackChampion:
		return "Champion"
	case TrackHero:
		return "Hero"
	case TrackMyth:
		return "Myth"
	}
	return "Unknown"
}

// IsBiBEligible reports whether items on this track are included in the
// BiB candidate set per the user's spec (hero + myth only).
func (t Track) IsBiBEligible() bool {
	return t == TrackHero || t == TrackMyth
}

// Item represents a single piece of gear from the addon dump. Gems and
// enchants are intentionally not preserved — BiB sims them bare.
type Item struct {
	Slot         Slot
	AddonSlotKey string // original key e.g. "finger1"
	Equipped     bool
	Name         string
	ItemID       int
	BonusIDs     string // raw "10384/10299/..." string, preserved as-is
	CraftedStats string // "crafted_stats=..." raw value, empty if none
	Context      string // "context=..." raw value, empty if none
	Track        Track
	OriginalIlvl int
	Extras       []string // any other key=value tokens (e.g. drop_level=)
}

// Render emits the simc-format item line with the given target ilvl and
// gems/enchants stripped per spec.
func (it Item) Render(targetIlvl int) string {
	parts := []string{",id=" + itoa(it.ItemID)}
	if it.BonusIDs != "" {
		parts = append(parts, "bonus_id="+it.BonusIDs)
	}
	if it.CraftedStats != "" {
		parts = append(parts, "crafted_stats="+it.CraftedStats)
	}
	if it.Context != "" {
		parts = append(parts, "context="+it.Context)
	}
	for _, e := range it.Extras {
		parts = append(parts, e)
	}
	if targetIlvl > 0 {
		parts = append(parts, "ilevel="+itoa(targetIlvl))
	}
	return joinFields(parts)
}

// EffectiveIlvl returns the rescaled ilvl per the user's spec.
func (it Item) EffectiveIlvl() int {
	switch it.Track {
	case TrackHero:
		return HeroTargetIlvl
	case TrackMyth:
		return MythTargetIlvl
	}
	return it.OriginalIlvl
}

// fingerprint is a stable identity used to dedupe candidates across
// equipped+bag.
func (it Item) fingerprint() string {
	return itoa(it.ItemID) + "|" + it.BonusIDs + "|" + it.CraftedStats
}

// HeroTargetIlvl is the ilvl every hero-track item is rescaled to.
const HeroTargetIlvl = 276

// MythTargetIlvl is the ilvl every myth-track item is rescaled to.
const MythTargetIlvl = 289

package simc

import (
	"strconv"
	"testing"
)

func TestMainstatGemIDForSpec(t *testing.T) {
	// Marksmanship hunter is agility — should resolve to the Flawless
	// Agility gem (ID 240889 in the catalog).
	got := mainstatGemIDForSpec("hunter", "marksmanship")
	if got != 240889 {
		t.Errorf("mainstatGemIDForSpec(hunter, marksmanship) = %d, want 240889", got)
	}

	// Frost mage is intellect.
	got = mainstatGemIDForSpec("mage", "frost")
	if got != 240890 {
		t.Errorf("mainstatGemIDForSpec(mage, frost) = %d, want 240890", got)
	}

	// Unknown class returns 0.
	if got := mainstatGemIDForSpec("notaclass", "x"); got != 0 {
		t.Errorf("unknown class -> %d, want 0", got)
	}
}

func TestMainstatGemUsedElsewhere(t *testing.T) {
	const mainstat = 240890 // Flawless Intellect
	other := strconv.Itoa(mainstat)
	otherSlot := SlotHead

	l := Loadout{Items: map[Slot][]Item{
		SlotHead: {{ItemID: 1, GemIDs: other}},
		SlotChest: {{ItemID: 2, GemIDs: "240892"}},
	}}

	// Querying for the chest slot: head has the mainstat → true.
	if !mainstatGemUsedElsewhere(l, mainstat, SlotChest, 0) {
		t.Errorf("expected mainstat-elsewhere for chest, got false")
	}
	// Querying for the head slot itself: it doesn't count against itself.
	if mainstatGemUsedElsewhere(l, mainstat, otherSlot, 0) {
		t.Errorf("expected false when querying the slot already wearing the mainstat")
	}

	// Multi-socket: only first socket counts toward uniqueness.
	l2 := Loadout{Items: map[Slot][]Item{
		SlotHead: {{ItemID: 1, GemIDs: "240892/" + other}},
	}}
	if mainstatGemUsedElsewhere(l2, mainstat, SlotChest, 0) {
		t.Errorf("multi-socket second slot should not count toward uniqueness")
	}
}

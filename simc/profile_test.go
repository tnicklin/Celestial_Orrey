package simc

import (
	"strings"
	"testing"
)

// sampleProfile is a minimal /simc-style dump exercising hero, myth, and
// champion tracks across single, double, and weapon slots.
const sampleProfile = `# Askr - Marksmanship - 2026-04-25
hunter="Askr"
level=80
race=blood_elf
region=us
server=area-52
spec=marksmanship
talents=ABCDEF

# Hero Helm (272 Hero 7/8)
head=,id=212014,bonus_id=10384/10299/8902/8783,gem_id=213743,enchant_id=7901
# Champion Neck (252 Champion 5/8)
neck=,id=212436,bonus_id=10299/8902
# Myth Shoulders (285 Myth 2/8)
shoulders=,id=212017,bonus_id=10000/9999
# Hero Cloak (272 Hero 7/8)
back=,id=212018,bonus_id=10384/10299
# Myth Chest (285 Myth 2/8)
chest=,id=212020,bonus_id=10001/9998,gem_id=213743
# Hero Wrists (272 Hero 7/8)
wrists=,id=212021,bonus_id=10384
# Hero Hands (272 Hero 7/8)
hands=,id=212022,bonus_id=10384
# Hero Belt (272 Hero 7/8)
waist=,id=212023,bonus_id=10384
# Myth Legs (285 Myth 2/8)
legs=,id=212024,bonus_id=10001
# Hero Boots (272 Hero 7/8)
feet=,id=212025,bonus_id=10384
# Hero Ring A (272 Hero 7/8)
finger1=,id=212100,bonus_id=10384,gem_id=213743
# Myth Ring A (285 Myth 2/8)
finger2=,id=212101,bonus_id=10001
# Hero Trinket A (272 Hero 7/8)
trinket1=,id=212200,bonus_id=10384
# Myth Trinket A (285 Myth 2/8)
trinket2=,id=212201,bonus_id=10001
# Hero Bow (272 Hero 7/8)
main_hand=,id=212300,bonus_id=10384,enchant_id=7901

### Bag Items
# Bag Hero Helm (272 Hero 7/8)
#head=,id=212015,bonus_id=10384/10299
# Bag Champion Helm (252 Champion 5/8)
#head=,id=212016,bonus_id=10299
# Bag Myth Ring (285 Myth 2/8)
#finger1=,id=212102,bonus_id=10001
# Bag Hero Trinket (272 Hero 7/8)
#trinket1=,id=212202,bonus_id=10384
`

func TestParseProfile(t *testing.T) {
	p, err := ParseProfile([]byte(sampleProfile))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) == 0 {
		t.Fatal("no items parsed")
	}

	var heads []Item
	for _, it := range p.Items {
		if it.Slot == SlotHead {
			heads = append(heads, it)
		}
	}
	if len(heads) != 3 {
		t.Fatalf("head items = %d, want 3 (1 equipped + 2 bag)", len(heads))
	}

	for _, it := range p.Items {
		if it.OriginalIlvl == 0 {
			t.Errorf("item id=%d slot=%s missing ilvl/track from comment", it.ItemID, it.Slot)
		}
		if it.Track == TrackUnknown {
			t.Errorf("item id=%d slot=%s missing track", it.ItemID, it.Slot)
		}
	}

	for _, it := range p.Items {
		if strings.Contains(it.Render(0), "gem_id=") {
			t.Errorf("rendered item still contains gem_id: %s", it.Render(0))
		}
		if strings.Contains(it.Render(0), "enchant_id=") {
			t.Errorf("rendered item still contains enchant_id: %s", it.Render(0))
		}
	}
}

func TestCandidatesBySlot_FiltersChampion(t *testing.T) {
	p, err := ParseProfile([]byte(sampleProfile))
	if err != nil {
		t.Fatal(err)
	}
	cands := p.CandidatesBySlot()
	if _, ok := cands[SlotNeck]; ok {
		t.Errorf("champion neck should be excluded; got %d candidates", len(cands[SlotNeck]))
	}
	if got := len(cands[SlotHead]); got != 2 {
		t.Errorf("head candidates = %d, want 2 (equipped hero + bag hero, champion bag dropped)", got)
	}
}

func TestEffectiveIlvl(t *testing.T) {
	hero := Item{Track: TrackHero, OriginalIlvl: 272}
	myth := Item{Track: TrackMyth, OriginalIlvl: 285}
	if hero.EffectiveIlvl() != HeroTargetIlvl {
		t.Errorf("hero -> %d, want %d", hero.EffectiveIlvl(), HeroTargetIlvl)
	}
	if myth.EffectiveIlvl() != MythTargetIlvl {
		t.Errorf("myth -> %d, want %d", myth.EffectiveIlvl(), MythTargetIlvl)
	}
}

func TestRender_AddsIlvelAndStripsGemEnchant(t *testing.T) {
	it := Item{
		Slot:    SlotHead,
		ItemID:  212014,
		BonusIDs: "10384/10299",
		Track:   TrackHero,
	}
	rendered := it.Render(it.EffectiveIlvl())
	if !strings.Contains(rendered, "ilevel=276") {
		t.Errorf("missing ilevel=276 in: %s", rendered)
	}
	if strings.Contains(rendered, "gem_id") || strings.Contains(rendered, "enchant_id") {
		t.Errorf("contains stripped fields: %s", rendered)
	}
}

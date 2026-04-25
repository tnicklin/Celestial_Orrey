package simc

import (
	"strings"
	"testing"
)

// midnightPaste is a verbatim slice of the actual /simc addon dump from
// Midnight 12.0.5 (Askrlol shadow priest). Comment format is "# Name (ilvl)"
// with no track keyword — track must be inferred from the ilvl band.
const midnightPaste = `# Askrlol - Shadow - 2026-04-25 17:19 - US/Mal'Ganis
# SimC Addon 12.0.5-01

priest="Askrlol"
level=90
race=gnome
region=us
server=malganis
spec=shadow
talents=ABCDEF

# Brambledawn Halo (276)
head=,id=251080,enchant_id=7961,gem_id=240890,bonus_id=13440/6652/13534/13577/12699/12798
# Masterwork Sin'dorei Amulet (285)
neck=,id=240950,gem_id=240983,bonus_id=12214/13667/12497/12066/8793/13622,crafted_stats=36/40,crafting_quality=5
# Blind Oath's Seraphguards (276)
shoulder=,id=250049,enchant_id=8031,bonus_id=6652/13440/13340/13574/12798
# Shroud of the Soulhunter (276)
back=,id=251161,bonus_id=13440/41/13577/12699/12798
# Blind Oath's Raiment (289)
chest=,id=250054,enchant_id=7987,bonus_id=12806/42/13440/13336/13575/3174
# Voracious Wristwraps (276)
wrist=,id=249315,bonus_id=6652/12667/13577/13334/12798
# Blind Oath's Touch (276)
hands=,id=250052,bonus_id=6652/13440/13337/13574/12798
# Arcanoweave Cord (285)
waist=,id=239664,bonus_id=12214/8960/12497/12066/13622/12667,crafting_quality=5
# Blind Oath's Leggings (276)
legs=,id=250050,enchant_id=7935,bonus_id=13339/6652/13575/12798
# Blind Oath's Slippers (289)
feet=,id=250053,enchant_id=7993,bonus_id=6652/13440/12806
# Eredath Seal of Nobility (289)
finger1=,id=151308,enchant_id=7997,gem_id=240890,bonus_id=13440/6652/13668/12699/12806
# Platinum Star Band (276)
finger2=,id=193708,enchant_id=7997,gem_id=240890,bonus_id=13440/6652/13668/12699/12798
# Emberwing Feather (276)
trinket1=,id=250144,bonus_id=13440/6652/12699/12798
# Drum of Renewed Bonds (276)
trinket2=,id=248583,bonus_id=6652/12798/13185
# Aln'hara Cane (285)
main_hand=,id=245770,enchant_id=8039,bonus_id=12214/12497/12066/12693/8960/8793/13622,crafted_stats=49/40,crafting_quality=5

### Gear from Bags
#
# Sprawling Stoloncollar (227)
# head=,id=249632,bonus_id=12771/6652/12667/13578
#
# Organized Pontificator's Mask (276)
# head=,id=193703,enchant_id=7961,gem_id=240892,bonus_id=13440/41/13534/13577/12699/12798
#
# Silvermoon Sunspire (246)
# head=,id=266429,bonus_id=13577/12785/12667
#
# Amani Heartstring Pendant (263)
# neck=,id=265739,gem_id=240983,bonus_id=6652/13668/12790
#
# Mantle of Dark Devotion (266)
# shoulder=,id=251085,bonus_id=12795/13440/6652/13577/12699
#
# Lightbinder Shoulderguards (266)
# shoulder=,id=258578,bonus_id=12795/13440/6652/13577/12699
#
# Defiant Defender's Drape (276)
# back=,id=260312,bonus_id=13440/42/13577/12699/12798
#
# Bifurcation Band (276)
# finger1=,id=251115,enchant_id=8025,gem_id=240892,bonus_id=13440/6652/13668/12699/12798
#
# Heart of Wind (276)
# trinket1=,id=250256,bonus_id=13440/40/12699/12798
`

func TestParseProfile_Midnight(t *testing.T) {
	p, err := ParseProfile([]byte(midnightPaste))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) == 0 {
		t.Fatal("no items parsed")
	}

	// Spot-check: Brambledawn Halo equipped at 276.
	var halo *Item
	for i := range p.Items {
		if p.Items[i].ItemID == 251080 {
			halo = &p.Items[i]
			break
		}
	}
	if halo == nil {
		t.Fatal("did not find Brambledawn Halo (251080)")
	}
	if halo.OriginalIlvl != 276 {
		t.Errorf("Brambledawn Halo ilvl = %d, want 276", halo.OriginalIlvl)
	}
	if halo.Track != TrackHero {
		t.Errorf("Brambledawn Halo track = %s, want Hero (inferred from 276)", halo.Track)
	}
	if !strings.Contains(halo.Name, "Brambledawn") {
		t.Errorf("Brambledawn Halo name = %q, want substring 'Brambledawn'", halo.Name)
	}

	// Spot-check: Eredath Seal at 289 should be Myth.
	var ring *Item
	for i := range p.Items {
		if p.Items[i].ItemID == 151308 && p.Items[i].Equipped {
			ring = &p.Items[i]
			break
		}
	}
	if ring == nil {
		t.Fatal("did not find equipped Eredath Seal (151308)")
	}
	if ring.Track != TrackMyth {
		t.Errorf("Eredath Seal at 289 → track = %s, want Myth", ring.Track)
	}
}

func TestParseProfile_Midnight_HasCandidates(t *testing.T) {
	p, err := ParseProfile([]byte(midnightPaste))
	if err != nil {
		t.Fatal(err)
	}
	cands := p.CandidatesBySlot()

	// Should have at least one candidate per equipped slot in the equipped
	// set (every equipped item is hero/myth in this paste).
	wantNonEmpty := []Slot{
		SlotHead, SlotNeck, SlotShoulders, SlotBack, SlotChest, SlotWrists,
		SlotHands, SlotWaist, SlotLegs, SlotFeet,
		SlotFinger, SlotTrinket, SlotMainHand,
	}
	for _, s := range wantNonEmpty {
		if len(cands[s]) == 0 {
			t.Errorf("slot %s has 0 candidates; expected at least 1", s)
		}
	}

	// Champion-track bag items (e.g. 246, 250 ilvl) should be excluded.
	for _, items := range cands {
		for _, it := range items {
			if it.Track == TrackChampion {
				t.Errorf("found champion item in candidates: id=%d ilvl=%d", it.ItemID, it.OriginalIlvl)
			}
			if it.OriginalIlvl < 252 {
				t.Errorf("found item below hero threshold: id=%d ilvl=%d", it.ItemID, it.OriginalIlvl)
			}
		}
	}
}

func TestParseProfile_Midnight_NoOffHand(t *testing.T) {
	p, err := ParseProfile([]byte(midnightPaste))
	if err != nil {
		t.Fatal(err)
	}
	equipped := p.EquippedBySlot()
	if len(equipped[SlotOffHand]) != 0 {
		t.Errorf("expected no equipped off_hand (priest with 2H staff), got %d", len(equipped[SlotOffHand]))
	}
}

package simc

import (
	"fmt"
	"sort"
	"strings"
)

// bibSlotOrder is the canonical order slots are emitted in a generated
// profile, matching the addon's typical layout.
var bibSlotOrder = []Slot{
	SlotHead, SlotNeck, SlotShoulders, SlotBack, SlotChest, SlotWrists,
	SlotHands, SlotWaist, SlotLegs, SlotFeet,
	SlotFinger, SlotTrinket,
	SlotMainHand, SlotOffHand,
}

// Loadout is one full gear configuration. For finger and trinket, two items
// appear in Items keyed by SlotFinger / SlotTrinket via slot order in
// SlotPairs.
type Loadout struct {
	// Items maps each slot to the chosen items. Single slots have one item;
	// finger and trinket have two.
	Items map[Slot][]Item
}

// fingerprint is a stable identity for de-dup detection.
func (l Loadout) fingerprint() string {
	var parts []string
	for _, s := range bibSlotOrder {
		items := l.Items[s]
		fps := make([]string, 0, len(items))
		for _, it := range items {
			fps = append(fps, it.fingerprint())
		}
		sort.Strings(fps)
		parts = append(parts, s.String()+":"+strings.Join(fps, ","))
	}
	return strings.Join(parts, "|")
}

// Render emits the simc item lines for this loadout. For finger and trinket,
// two lines (finger1/finger2, trinket1/trinket2) are emitted in stable order.
func (l Loadout) Render() string {
	var sb strings.Builder
	for _, s := range bibSlotOrder {
		items, ok := l.Items[s]
		if !ok || len(items) == 0 {
			continue
		}
		switch s {
		case SlotFinger:
			renderPair(&sb, "finger1", "finger2", items)
		case SlotTrinket:
			renderPair(&sb, "trinket1", "trinket2", items)
		default:
			fmt.Fprintf(&sb, "%s=%s\n", s.String(), items[0].Render(items[0].EffectiveIlvl()))
		}
	}
	return sb.String()
}

func renderPair(sb *strings.Builder, key1, key2 string, items []Item) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(sb, "%s=%s\n", key1, items[0].Render(items[0].EffectiveIlvl()))
	if len(items) > 1 {
		fmt.Fprintf(sb, "%s=%s\n", key2, items[1].Render(items[1].EffectiveIlvl()))
	}
}

// CombinationStats summarises the candidate space.
type CombinationStats struct {
	Total       int
	BySlot      map[Slot]int // candidate count per slot
	Empty       []Slot       // slots with zero candidates
	DoubleEmpty []Slot       // finger/trinket with <2 candidates
}

// AnalyzeCandidates returns counts and the total combination size.
func AnalyzeCandidates(cands map[Slot][]Item) CombinationStats {
	stats := CombinationStats{BySlot: make(map[Slot]int), Total: 1}
	for _, s := range bibSlotOrder {
		items := cands[s]
		stats.BySlot[s] = len(items)
		var opts int
		switch {
		case s.IsDoubleSlot():
			if len(items) < 2 {
				stats.DoubleEmpty = append(stats.DoubleEmpty, s)
				opts = max1(len(items))
			} else {
				opts = nChoose2(len(items))
			}
		default:
			if len(items) == 0 {
				stats.Empty = append(stats.Empty, s)
				opts = 1
			} else {
				opts = len(items)
			}
		}
		stats.Total *= opts
	}
	return stats
}

// GenerateCombinations enumerates every loadout from the candidate set.
// Slots with zero candidates are simply omitted from each loadout (simc
// will sim with the slot bare). Finger and trinket use C(n,2) pairs; if a
// slot has only one candidate, that single item fills the first sub-slot.
//
// maxCombos is a safety cap. If the cross-product would exceed it, the
// function returns an error and emits no loadouts.
func GenerateCombinations(cands map[Slot][]Item, maxCombos int) ([]Loadout, CombinationStats, error) {
	stats := AnalyzeCandidates(cands)
	if maxCombos > 0 && stats.Total > maxCombos {
		return nil, stats, fmt.Errorf("combination space too large: %d candidates exceeds cap of %d", stats.Total, maxCombos)
	}

	options := make(map[Slot][][]Item, len(bibSlotOrder))
	for _, s := range bibSlotOrder {
		items := cands[s]
		if s.IsDoubleSlot() {
			options[s] = doubleSlotOptions(items)
			continue
		}
		options[s] = singleSlotOptions(items)
	}

	var out []Loadout
	cur := Loadout{Items: make(map[Slot][]Item)}
	bibCombine(0, options, cur, &out)
	return out, stats, nil
}

func bibCombine(i int, options map[Slot][][]Item, cur Loadout, out *[]Loadout) {
	if i == len(bibSlotOrder) {
		// Copy to detach from the cursor.
		clone := Loadout{Items: make(map[Slot][]Item, len(cur.Items))}
		for k, v := range cur.Items {
			cp := append([]Item(nil), v...)
			clone.Items[k] = cp
		}
		*out = append(*out, clone)
		return
	}
	slot := bibSlotOrder[i]
	opts := options[slot]
	if len(opts) == 0 {
		bibCombine(i+1, options, cur, out)
		return
	}
	for _, choice := range opts {
		if len(choice) == 0 {
			delete(cur.Items, slot)
		} else {
			cur.Items[slot] = choice
		}
		bibCombine(i+1, options, cur, out)
	}
	delete(cur.Items, slot)
}

func singleSlotOptions(items []Item) [][]Item {
	if len(items) == 0 {
		return [][]Item{nil}
	}
	out := make([][]Item, 0, len(items))
	for _, it := range items {
		out = append(out, []Item{it})
	}
	return out
}

func doubleSlotOptions(items []Item) [][]Item {
	switch len(items) {
	case 0:
		return [][]Item{nil}
	case 1:
		return [][]Item{{items[0]}}
	}
	out := make([][]Item, 0, nChoose2(len(items)))
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			out = append(out, []Item{items[i], items[j]})
		}
	}
	return out
}

func nChoose2(n int) int {
	if n < 2 {
		return 0
	}
	return n * (n - 1) / 2
}

func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

// BuildProfile assembles a complete .simc profile by replacing the source
// profile's item lines with the loadout's items. The header (character,
// talents, profession lines) is preserved verbatim.
func BuildProfile(p *Profile, l Loadout) []byte {
	var sb strings.Builder
	for _, line := range p.Header {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	if len(p.Header) > 0 && !strings.HasSuffix(p.Header[len(p.Header)-1], "\n") {
		// no-op; we always append \n above
	}
	sb.WriteString(l.Render())
	for _, line := range p.Footer {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	return []byte(sb.String())
}

// BuildEquippedBaseline returns a profile containing only the user's
// currently-equipped items, gems and enchants stripped, ilvl unchanged.
// Used for the "current DPS" baseline sim.
func BuildEquippedBaseline(p *Profile) []byte {
	var sb strings.Builder
	for _, line := range p.Header {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	for _, it := range p.Items {
		if !it.Equipped {
			continue
		}
		fmt.Fprintf(&sb, "%s=%s\n", it.AddonSlotKey, it.Render(0))
	}
	for _, line := range p.Footer {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	return []byte(sb.String())
}

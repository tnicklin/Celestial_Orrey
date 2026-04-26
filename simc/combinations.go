package simc

import (
	"fmt"
	"sort"
	"strings"
)

// slotOrder is the canonical order slots are emitted in a generated
// profile, matching the addon's typical layout.
var slotOrder = []Slot{
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
	for _, s := range slotOrder {
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
	for _, s := range slotOrder {
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

// AnalyzeCandidates returns counts and the total combination size. Used
// for the Discord intro warnings (empty / double-empty slots) — the
// orchestrator no longer enumerates the cross product.
func AnalyzeCandidates(cands map[Slot][]Item) CombinationStats {
	stats := CombinationStats{BySlot: make(map[Slot]int), Total: 1}
	for _, s := range slotOrder {
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

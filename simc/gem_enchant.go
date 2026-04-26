package simc

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// GemChange records a single per-item gem swap chosen by the gem
// optimization phase. Used by the Discord layer to render a "gems
// changed" section in the result embed.
type GemChange struct {
	Slot   Slot
	ItemID int
	Before string // raw gem_id= value before optimization
	After  string // raw gem_id= value after optimization
	Name   string // human-friendly name of the chosen gem (when known)
}

// EnchantChange records a single per-item enchant swap. Same usage
// pattern as GemChange.
type EnchantChange struct {
	Slot      Slot
	ItemID    int
	Before    string
	After     string
	Name      string
}

// OptimizeGemsAndEnchants runs the gem and ring-enchant optimization
// phases on top of an already-optimized loadout. Returns the new
// loadout (items mutated to reflect new gem/enchant IDs) plus the
// per-slot change lists for display.
//
// Strategy:
//   - For each item with a *current* gem_id (we can detect a socket
//     from the addon paste only via existing gem), greedy-pick the best
//     option from GemsForSpec narrowed to top-2 priority secondaries
//     (plus mainstat). Refinement pass to handle DR / stat caps.
//   - For each ring, greedy-pick the best ring enchant from
//     RingEnchantsForSpec. Other slots' enchants are left alone.
//
// Both phases preserve the option of "don't change the current pick"
// by including the existing gem_id / enchant_id as a candidate.
func OptimizeGemsAndEnchants(ctx context.Context, p *Profile, base Loadout, fs FightStyle, iters int, runner SimRunner, tel *GreedyTelemetry) (Loadout, []GemChange, []EnchantChange, error) {
	cur := cloneLoadout(base)
	class := p.ClassName()
	spec := p.Spec()

	// Snapshot the starting state per slot so we can diff after both
	// phases finish.
	startGems := snapshotGems(cur)
	startEnchants := snapshotEnchants(cur)

	gemOptions := GemsForSpec(class, spec, 2) // mainstat + top-2 secondaries (plus their combo)
	if len(gemOptions) > 0 {
		if err := optimizeGemsPhase(ctx, p, cur, gemOptions, fs, iters, runner, tel); err != nil {
			return Loadout{}, nil, nil, fmt.Errorf("gem phase: %w", err)
		}
	}

	enchantOptions := RingEnchantsForSpec(class, spec, 2)
	if len(enchantOptions) > 0 {
		if err := optimizeRingEnchantsPhase(ctx, p, cur, enchantOptions, fs, iters, runner, tel); err != nil {
			return Loadout{}, nil, nil, fmt.Errorf("enchant phase: %w", err)
		}
	}

	gemChanges := diffGems(cur, startGems)
	enchantChanges := diffEnchants(cur, startEnchants)
	return cur, gemChanges, enchantChanges, nil
}

// optimizeGemsPhase iterates every item with a current gem_id and
// greedy-picks the best gem from `options`. Two passes catch
// stat-stacking DR (e.g. 5 sockets all picking Haste in pass 1, then
// pass 2 swapping some to Mastery once Haste is over the soft cap).
func optimizeGemsPhase(ctx context.Context, p *Profile, cur Loadout, options []GemOption, fs FightStyle, iters int, runner SimRunner, tel *GreedyTelemetry) error {
	const maxPasses = 2
	for pass := 0; pass < maxPasses; pass++ {
		changed := false
		for _, slot := range slotOrder {
			items, ok := cur.Items[slot]
			if !ok {
				continue
			}
			for itemIdx, it := range items {
				if it.GemIDs == "" {
					continue
				}
				flipped, err := pickBestGemForItem(ctx, p, cur, slot, itemIdx, options, fs, iters, runner, tel)
				if err != nil {
					return err
				}
				if flipped {
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}
	return nil
}

// pickBestGemForItem sims each gem option in `options` (plus the item's
// current gem to guarantee we never get worse) and replaces the item's
// GemIDs with the winner. Only mutates the FIRST socket on items with
// multiple sockets — most Midnight S1 gear has at most one. Multi-
// socket items can be handled in a follow-up by sweeping per socket
// index.
func pickBestGemForItem(ctx context.Context, p *Profile, cur Loadout, slot Slot, itemIdx int, options []GemOption, fs FightStyle, iters int, runner SimRunner, tel *GreedyTelemetry) (bool, error) {
	originalItems := append([]Item(nil), cur.Items[slot]...)
	originalItem := originalItems[itemIdx]
	originalGem := originalItem.GemIDs

	// Build the candidate gem-id list. Always include the current value
	// at index 0 so a tie keeps the user's existing choice.
	candidates := []string{originalGem}
	seen := map[string]bool{originalGem: true}
	for _, opt := range options {
		id := strconv.Itoa(opt.ID)
		if seen[id] {
			continue
		}
		seen[id] = true
		candidates = append(candidates, id)
	}

	// For multi-socket items, replace just the first socket's gem (the
	// rest preserve their existing values). Most items have one socket.
	swapFirst := func(gemIDs string, newFirst string) string {
		if !strings.Contains(gemIDs, "/") {
			return newFirst
		}
		parts := strings.SplitN(gemIDs, "/", 2)
		return newFirst + "/" + parts[1]
	}

	bodies := make([][]byte, len(candidates))
	for i, gemID := range candidates {
		modified := originalItem
		modified.GemIDs = swapFirst(originalGem, gemID)
		newItems := append([]Item(nil), originalItems...)
		newItems[itemIdx] = modified
		trial := withSlot(cur, slot, newItems)
		bodies[i] = BuildProfile(p, trial)
	}

	scores, err := runFanout(ctx, bodies, fs, iters, runner, tel, slot)
	if err != nil {
		return false, err
	}
	bestIdx := 0
	bestDPS := scores[0]
	for i := 1; i < len(scores); i++ {
		if scores[i] > bestDPS {
			bestDPS = scores[i]
			bestIdx = i
		}
	}

	winner := candidates[bestIdx]
	if winner == originalGem {
		return false, nil
	}
	updated := originalItem
	updated.GemIDs = swapFirst(originalGem, winner)
	newItems := append([]Item(nil), originalItems...)
	newItems[itemIdx] = updated
	cur.Items[slot] = newItems
	return true, nil
}

// optimizeRingEnchantsPhase iterates SlotFinger items and picks the
// best ring enchant from `options`. Two passes for DR symmetry with
// the gem phase.
func optimizeRingEnchantsPhase(ctx context.Context, p *Profile, cur Loadout, options []EnchantOption, fs FightStyle, iters int, runner SimRunner, tel *GreedyTelemetry) error {
	const maxPasses = 2
	for pass := 0; pass < maxPasses; pass++ {
		changed := false
		items, ok := cur.Items[SlotFinger]
		if !ok {
			break
		}
		for itemIdx := range items {
			flipped, err := pickBestEnchantForItem(ctx, p, cur, SlotFinger, itemIdx, options, fs, iters, runner, tel)
			if err != nil {
				return err
			}
			if flipped {
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return nil
}

func pickBestEnchantForItem(ctx context.Context, p *Profile, cur Loadout, slot Slot, itemIdx int, options []EnchantOption, fs FightStyle, iters int, runner SimRunner, tel *GreedyTelemetry) (bool, error) {
	originalItems := append([]Item(nil), cur.Items[slot]...)
	originalItem := originalItems[itemIdx]
	originalEnchant := originalItem.EnchantID

	candidates := []string{originalEnchant}
	seen := map[string]bool{originalEnchant: true}
	for _, opt := range options {
		id := strconv.Itoa(opt.ID)
		if seen[id] {
			continue
		}
		seen[id] = true
		candidates = append(candidates, id)
	}

	bodies := make([][]byte, len(candidates))
	for i, enchantID := range candidates {
		modified := originalItem
		modified.EnchantID = enchantID
		newItems := append([]Item(nil), originalItems...)
		newItems[itemIdx] = modified
		trial := withSlot(cur, slot, newItems)
		bodies[i] = BuildProfile(p, trial)
	}

	scores, err := runFanout(ctx, bodies, fs, iters, runner, tel, slot)
	if err != nil {
		return false, err
	}
	bestIdx := 0
	bestDPS := scores[0]
	for i := 1; i < len(scores); i++ {
		if scores[i] > bestDPS {
			bestDPS = scores[i]
			bestIdx = i
		}
	}

	winner := candidates[bestIdx]
	if winner == originalEnchant {
		return false, nil
	}
	updated := originalItem
	updated.EnchantID = winner
	newItems := append([]Item(nil), originalItems...)
	newItems[itemIdx] = updated
	cur.Items[slot] = newItems
	return true, nil
}

// snapshotGems / snapshotEnchants record the per-(slot,itemIdx) values
// at the start of the optimization so we can diff at the end.
func snapshotGems(l Loadout) map[Slot][]string {
	out := make(map[Slot][]string, len(l.Items))
	for slot, items := range l.Items {
		vals := make([]string, len(items))
		for i, it := range items {
			vals[i] = it.GemIDs
		}
		out[slot] = vals
	}
	return out
}

func snapshotEnchants(l Loadout) map[Slot][]string {
	out := make(map[Slot][]string, len(l.Items))
	for slot, items := range l.Items {
		vals := make([]string, len(items))
		for i, it := range items {
			vals[i] = it.EnchantID
		}
		out[slot] = vals
	}
	return out
}

func diffGems(cur Loadout, before map[Slot][]string) []GemChange {
	var out []GemChange
	for _, slot := range slotOrder {
		items, ok := cur.Items[slot]
		if !ok {
			continue
		}
		prevs := before[slot]
		for i, it := range items {
			var prev string
			if i < len(prevs) {
				prev = prevs[i]
			}
			if it.GemIDs == prev {
				continue
			}
			out = append(out, GemChange{
				Slot:   slot,
				ItemID: it.ItemID,
				Before: prev,
				After:  it.GemIDs,
				Name:   gemNameForID(it.GemIDs),
			})
		}
	}
	return out
}

func diffEnchants(cur Loadout, before map[Slot][]string) []EnchantChange {
	var out []EnchantChange
	for _, slot := range slotOrder {
		items, ok := cur.Items[slot]
		if !ok {
			continue
		}
		prevs := before[slot]
		for i, it := range items {
			var prev string
			if i < len(prevs) {
				prev = prevs[i]
			}
			if it.EnchantID == prev {
				continue
			}
			out = append(out, EnchantChange{
				Slot:   slot,
				ItemID: it.ItemID,
				Before: prev,
				After:  it.EnchantID,
				Name:   enchantNameForID(it.EnchantID),
			})
		}
	}
	return out
}

// gemNameForID resolves the chosen gem_id back to a human-friendly
// name for display. The string passed in may include slash-separated
// extra-socket gem ids; we look up the first one. Returns "" if the
// gem isn't in our catalog.
func gemNameForID(gemIDs string) string {
	first := gemIDs
	if i := strings.Index(gemIDs, "/"); i >= 0 {
		first = gemIDs[:i]
	}
	id, err := strconv.Atoi(first)
	if err != nil {
		return ""
	}
	for _, g := range flawlessGems {
		if g.ID == id {
			return g.Name
		}
	}
	return ""
}

func enchantNameForID(enchantID string) string {
	id, err := strconv.Atoi(enchantID)
	if err != nil {
		return ""
	}
	for _, e := range ringEnchants {
		if e.ID == id {
			return e.Name
		}
	}
	return ""
}

// cloneLoadout returns a deep-enough copy of l so callers can mutate
// items without affecting the original. Item slices are duplicated;
// the items themselves are value types so a shallow item copy is
// enough.
func cloneLoadout(l Loadout) Loadout {
	out := Loadout{Items: make(map[Slot][]Item, len(l.Items))}
	for k, v := range l.Items {
		cp := make([]Item, len(v))
		copy(cp, v)
		out.Items[k] = cp
	}
	return out
}

// MaxGemEnchantSims returns an upper-bound sim count for the gem +
// enchant phases given a loadout and spec.
func MaxGemEnchantSims(loadout Loadout, class, spec string) int {
	gemOpts := len(GemsForSpec(class, spec, 2)) + 1 // +1 for current
	enchantOpts := len(RingEnchantsForSpec(class, spec, 2)) + 1

	gemSims := 0
	for _, items := range loadout.Items {
		for _, it := range items {
			if it.GemIDs != "" {
				gemSims += gemOpts
			}
		}
	}

	ringItems := len(loadout.Items[SlotFinger])
	enchantSims := ringItems * enchantOpts

	const passes = 2
	return passes * (gemSims + enchantSims)
}
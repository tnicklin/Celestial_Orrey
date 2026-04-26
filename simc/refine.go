package simc

import (
	"context"
	"fmt"
	"sort"
)

// IndeterminateThreshold is the DPS gap (as a fraction, e.g. 0.003 = 0.3%)
// below which a slot's top-1 vs top-2 is considered ambiguous and the
// runner-up gets included in the cross-product refinement step.
const IndeterminateThreshold = 0.003

// MaxCrossProductSlots caps how many slots can participate in the cross
// product. 7 → 128 combos at most; anything bigger means the algorithm
// is uncertain on too many slots and we should trust the greedy result
// rather than blow up sim count.
const MaxCrossProductSlots = 7

// IndeterminateSlot describes a slot whose greedy winner has a near-tie
// runner-up and therefore deserves a second look in the cross-product
// refinement.
type IndeterminateSlot struct {
	Slot       Slot
	TopItem    Item
	RunnerUp   Item
	TopDPS     float64
	RunnerDPS  float64
	GapPct     float64 // (top - runner) / top
}

// indeterminateSlotsFromGreedy returns slots where the top-1 vs top-2
// gap is below the threshold AND a viable runner-up exists. Sorted by
// ascending gap so the closest ties come first. Caller may truncate at
// MaxCrossProductSlots.
func indeterminateSlotsFromGreedy(results map[Slot]GreedySlotResult) []IndeterminateSlot {
	var out []IndeterminateSlot
	for slot, res := range results {
		if len(res.Pool) < 2 {
			continue
		}
		top := res.TopN(2)
		if len(top) < 2 {
			continue
		}
		topDPS := res.Scores[top[0]]
		runnerDPS := res.Scores[top[1]]
		if topDPS <= 0 {
			continue
		}
		gap := (topDPS - runnerDPS) / topDPS
		if gap > IndeterminateThreshold {
			continue
		}
		out = append(out, IndeterminateSlot{
			Slot:      slot,
			TopItem:   res.Pool[top[0]],
			RunnerUp:  res.Pool[top[1]],
			TopDPS:    topDPS,
			RunnerDPS: runnerDPS,
			GapPct:    gap,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GapPct < out[j].GapPct })
	if len(out) > MaxCrossProductSlots {
		out = out[:MaxCrossProductSlots]
	}
	return out
}

// CrossProductRefine takes the greedy winning loadout and the per-slot
// greedy scores. For slots where top-1 and top-2 are within the
// indeterminate threshold, it brute-forces every 2^k assignment and
// picks the loadout with the highest sim DPS. Returns the refined
// loadout (== greedy if no indeterminate slots).
func CrossProductRefine(ctx context.Context, p *Profile, base Loadout, results map[Slot]GreedySlotResult, fs FightStyle, iters int, runner SimRunner, tel *GreedyTelemetry) (Loadout, error) {
	indet := indeterminateSlotsFromGreedy(results)
	if len(indet) == 0 {
		return base, nil
	}

	// Generate every 2^k combination of "top vs runner-up" for the
	// indeterminate slots. Slot k's pick is encoded by bit k of i:
	// 0 = top, 1 = runner-up.
	combos := 1 << len(indet)
	bodies := make([][]byte, combos)
	loadouts := make([]Loadout, combos)
	for i := 0; i < combos; i++ {
		l := withSlot(base, indet[0].Slot, base.Items[indet[0].Slot]) // shallow copy via withSlot trick on a no-op slot
		// Re-do as a clean copy of base.
		l = Loadout{Items: make(map[Slot][]Item, len(base.Items))}
		for k, v := range base.Items {
			l.Items[k] = v
		}
		for bit, slot := range indet {
			pick := slot.TopItem
			if i&(1<<bit) != 0 {
				pick = slot.RunnerUp
			}
			l.Items[slot.Slot] = []Item{pick}
		}
		loadouts[i] = l
		bodies[i] = BuildProfile(p, l)
	}

	scores, err := runFanout(ctx, bodies, fs, iters, runner, tel, indet[0].Slot)
	if err != nil {
		return Loadout{}, fmt.Errorf("cross-product refine: %w", err)
	}

	bestIdx := 0
	bestDPS := scores[0]
	for i := 1; i < len(scores); i++ {
		if scores[i] > bestDPS {
			bestDPS = scores[i]
			bestIdx = i
		}
	}
	return loadouts[bestIdx], nil
}

// MaxCrossProductSims returns the upper-bound sim count for the
// cross-product step. With k indeterminate slots, the worst case is
// 2^k combinations.
func MaxCrossProductSims(maxK int) int {
	if maxK > MaxCrossProductSlots {
		maxK = MaxCrossProductSlots
	}
	return 1 << maxK
}

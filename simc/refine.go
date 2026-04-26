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
// loadout (== greedy if no indeterminate slots) and a structured
// report of every combo tried.
func CrossProductRefine(ctx context.Context, p *Profile, base Loadout, results map[Slot]GreedySlotResult, fs FightStyle, iters int, runner SimRunner, tel *GreedyTelemetry) (Loadout, CrossProductReport, error) {
	indet := indeterminateSlotsFromGreedy(results)
	report := CrossProductReport{}
	if len(indet) == 0 {
		report.Skipped = true
		report.SkipReason = "no_indeterminate_slots"
		return base, report, nil
	}

	for _, s := range indet {
		report.IndeterminateSlots = append(report.IndeterminateSlots, IndetSlot{
			Slot:   s.Slot.String(),
			GapPct: s.GapPct * 100,
		})
	}

	combos := 1 << len(indet)
	report.CombosTried = combos
	bodies := make([][]byte, combos)
	loadouts := make([]Loadout, combos)
	for i := 0; i < combos; i++ {
		l := Loadout{Items: make(map[Slot][]Item, len(base.Items))}
		for k, v := range base.Items {
			l.Items[k] = v
		}
		picks := map[string]int{}
		for bit, slot := range indet {
			pick := slot.TopItem
			if i&(1<<bit) != 0 {
				pick = slot.RunnerUp
			}
			l.Items[slot.Slot] = []Item{pick}
			picks[slot.Slot.String()] = pick.ItemID
		}
		loadouts[i] = l
		bodies[i] = BuildProfile(p, l)
		report.Combos = append(report.Combos, CrossCombo{
			Index: i,
			Picks: picks,
		})
	}

	scores, err := runFanout(ctx, bodies, fs, iters, runner, tel, indet[0].Slot)
	if err != nil {
		return Loadout{}, report, fmt.Errorf("cross-product refine: %w", err)
	}
	for i, s := range scores {
		report.Combos[i].DPS = s
	}

	bestIdx := 0
	bestDPS := scores[0]
	for i := 1; i < len(scores); i++ {
		if scores[i] > bestDPS {
			bestDPS = scores[i]
			bestIdx = i
		}
	}
	report.WinnerIndex = bestIdx

	// FlippedFromGreedy: any slot whose pick in the winner combo
	// differs from the greedy top item.
	for bit, slot := range indet {
		if bestIdx&(1<<bit) != 0 {
			report.FlippedFromGreedy = append(report.FlippedFromGreedy, slot.Slot.String())
		}
	}
	return loadouts[bestIdx], report, nil
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

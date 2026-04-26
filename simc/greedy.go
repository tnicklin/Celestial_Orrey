package simc

import (
	"context"
	"errors"
	"fmt"
)

// SimRunner dispatches a single sim and waits for the result. The
// orchestrator implements this by submitting through its queue; tests
// supply fakes that return canned DPS values. Concurrency() reports
// how many sims the runner can satisfy in parallel — greedy bounds
// its per-slot fanout to this value so we never overflow the queue.
type SimRunner interface {
	Run(ctx context.Context, body []byte, fs FightStyle, iters int) (SimResult, error)
	Concurrency() int
}

// GreedyTelemetry is what the optimizer reports back to its caller for
// progress accounting and logging.
type GreedyTelemetry struct {
	SimsRun   int
	PassesRun int
}

// GreedyProgress is invoked between sweeps and slot picks so the caller
// can publish status updates. All fields are best-effort.
type GreedyProgress func(pass int, slot Slot, slotIdx, slotsTotal int)

// GreedySlotResult captures the most recent per-slot pick made during
// the final sweep: the candidate pool and the DPS scored by each. The
// orchestrator uses this to find "indeterminate" slots (top-1 vs top-2
// gap below noise) for the cross-product refinement step.
//
// Populated only for single slots — double slots use sequential picking
// which doesn't yield a clean per-candidate score.
type GreedySlotResult struct {
	Pool   []Item
	Scores []float64
}

// GreedyDoubleSlotResult captures the per-half scoring for a double
// slot's sequential pick. Both pools are the candidate sets that were
// sim'd; PrimaryWinner / SecondaryWinner are the items chosen.
type GreedyDoubleSlotResult struct {
	PrimaryPool      []Item
	PrimaryScores    []float64
	PrimaryWinner    Item
	SecondaryPool    []Item
	SecondaryScores  []float64
	SecondaryWinner  Item
}

// GreedyResults bundles per-slot scoring data from the final greedy
// sweep — single slots in Slots, double slots in Doubles. Consumed by
// the cross-product refinement (single-slot only) and the report
// writer (both).
type GreedyResults struct {
	Slots   map[Slot]GreedySlotResult
	Doubles map[Slot]GreedyDoubleSlotResult
}

// TopN returns the top-n indices into Pool sorted by score descending.
// If n exceeds len(Pool), all indices are returned.
func (r GreedySlotResult) TopN(n int) []int {
	type kv struct {
		i int
		s float64
	}
	pairs := make([]kv, len(r.Scores))
	for i, s := range r.Scores {
		pairs[i] = kv{i, s}
	}
	// Simple selection — slot pools are tiny (≤10).
	for i := 0; i < len(pairs); i++ {
		for j := i + 1; j < len(pairs); j++ {
			if pairs[j].s > pairs[i].s {
				pairs[i], pairs[j] = pairs[j], pairs[i]
			}
		}
	}
	if n > len(pairs) {
		n = len(pairs)
	}
	out := make([]int, n)
	for i := 0; i < n; i++ {
		out[i] = pairs[i].i
	}
	return out
}

// GreedyOptimize runs a per-slot greedy search over the candidate pool
// for a single fight style. It returns the best assembled loadout, the
// per-slot scoring data from the final sweep, and the number of sims
// actually executed.
//
// The algorithm:
//  1. Seed the loadout with the user's currently equipped items, but
//     only for slots whose equipped item is on the eligible track
//     (Hero/Myth) AND appears in the candidate set.
//  2. Sweep slots in `slotOrder`. For each slot, sim every candidate
//     held against the current best for the other slots, pick the
//     winner. Finger/trinket use sequential picking.
//  3. If any slot's winner differed from the previous best, do one
//     refinement sweep. Bail early when a sweep produces no changes.
func GreedyOptimize(ctx context.Context, p *Profile, cands map[Slot][]Item, fs FightStyle, iters int, runner SimRunner, onProgress GreedyProgress) (Loadout, GreedyResults, GreedyTelemetry, error) {
	cur := seedLoadout(p, cands)
	tel := GreedyTelemetry{}
	results := GreedyResults{
		Slots:   make(map[Slot]GreedySlotResult),
		Doubles: make(map[Slot]GreedyDoubleSlotResult),
	}

	const maxPasses = 2
	for pass := 0; pass < maxPasses; pass++ {
		changed := false
		tel.PassesRun = pass + 1
		for slotIdx, slot := range slotOrder {
			items := cands[slot]
			if len(items) == 0 {
				continue
			}
			if onProgress != nil {
				onProgress(pass, slot, slotIdx, len(slotOrder))
			}

			if slot.IsDoubleSlot() {
				dres, slotChanged, err := pickDoubleSlot(ctx, p, cur, slot, items, fs, iters, runner, &tel)
				if err != nil {
					return Loadout{}, GreedyResults{}, tel, err
				}
				results.Doubles[slot] = dres
				if slotChanged {
					changed = true
				}
				continue
			}

			res, slotChanged, err := pickSingleSlot(ctx, p, cur, slot, items, fs, iters, runner, &tel)
			if err != nil {
				return Loadout{}, GreedyResults{}, tel, err
			}
			results.Slots[slot] = res
			if slotChanged {
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	return cur, results, tel, nil
}

// seedLoadout returns the starting loadout: the user's currently-equipped
// items, but only for slots that have at least one eligible candidate.
// The seed is mutable — callers update it as the sweep progresses.
func seedLoadout(p *Profile, cands map[Slot][]Item) Loadout {
	out := Loadout{Items: make(map[Slot][]Item)}
	equipped := p.EquippedBySlot()
	for _, slot := range slotOrder {
		if len(cands[slot]) == 0 {
			continue
		}
		eq := equipped[slot]
		if len(eq) == 0 {
			if slot.IsDoubleSlot() {
				if len(cands[slot]) >= 2 {
					out.Items[slot] = []Item{cands[slot][0], cands[slot][1]}
				} else {
					out.Items[slot] = []Item{cands[slot][0]}
				}
			} else {
				out.Items[slot] = []Item{cands[slot][0]}
			}
			continue
		}
		if slot.IsDoubleSlot() && len(eq) >= 2 {
			out.Items[slot] = []Item{eq[0], eq[1]}
			continue
		}
		out.Items[slot] = []Item{eq[0]}
	}
	return out
}

// pickSingleSlot sims each candidate (plus whatever is currently in the
// slot, dedup'd by fingerprint) and updates cur with the winner.
// Returns the per-candidate scores and whether the winner differed.
func pickSingleSlot(ctx context.Context, p *Profile, cur Loadout, slot Slot, items []Item, fs FightStyle, iters int, runner SimRunner, tel *GreedyTelemetry) (GreedySlotResult, bool, error) {
	pool := mergeWithCurrent(items, cur.Items[slot])

	bodies := make([][]byte, len(pool))
	for i, c := range pool {
		trial := withSlot(cur, slot, []Item{c})
		bodies[i] = BuildProfile(p, trial)
	}

	scores, err := runFanout(ctx, bodies, fs, iters, runner, tel, slot)
	if err != nil {
		return GreedySlotResult{}, false, err
	}
	bestIdx := -1
	bestDPS := -1.0
	for i, s := range scores {
		if s > bestDPS {
			bestDPS = s
			bestIdx = i
		}
	}
	if bestIdx < 0 {
		return GreedySlotResult{}, false, fmt.Errorf("sim slot %s: no successful candidate", slot)
	}
	prev := cur.Items[slot]
	cur.Items[slot] = []Item{pool[bestIdx]}
	return GreedySlotResult{Pool: pool, Scores: scores}, !sameItems(prev, cur.Items[slot]), nil
}

// pickDoubleSlot does the sequential pick for finger/trinket: first the
// "primary" item (held against the equipped secondary), then the
// "secondary" (held against the freshly-picked primary). Updates cur in
// place and returns the per-half scoring data plus whether either pick
// differed.
func pickDoubleSlot(ctx context.Context, p *Profile, cur Loadout, slot Slot, items []Item, fs FightStyle, iters int, runner SimRunner, tel *GreedyTelemetry) (GreedyDoubleSlotResult, bool, error) {
	prev := append([]Item(nil), cur.Items[slot]...)
	dres := GreedyDoubleSlotResult{}

	if len(items) < 2 {
		cur.Items[slot] = []Item{items[0]}
		dres.PrimaryPool = []Item{items[0]}
		dres.PrimaryWinner = items[0]
		return dres, !sameItems(prev, cur.Items[slot]), nil
	}

	var heldSecondary Item
	if len(prev) >= 2 {
		heldSecondary = prev[1]
	} else {
		heldSecondary = items[0]
	}

	primaryPool := mergeWithCurrent(items, []Item{cur.Items[slot][0]})
	winner1, primaryScores, err := bestForDoubleSlot(ctx, p, cur, slot, primaryPool, heldSecondary, true, fs, iters, runner, tel)
	if err != nil {
		return dres, false, err
	}
	dres.PrimaryPool = primaryPool
	dres.PrimaryScores = primaryScores
	dres.PrimaryWinner = winner1

	secondaryItems := excludeByFingerprint(items, winner1)
	if len(prev) >= 2 && prev[1].fingerprint() != winner1.fingerprint() {
		secondaryItems = mergeWithCurrent(secondaryItems, []Item{prev[1]})
	}
	if len(secondaryItems) == 0 {
		cur.Items[slot] = []Item{winner1}
		return dres, !sameItems(prev, cur.Items[slot]), nil
	}
	winner2, secondaryScores, err := bestForDoubleSlot(ctx, p, cur, slot, secondaryItems, winner1, false, fs, iters, runner, tel)
	if err != nil {
		return dres, false, err
	}
	dres.SecondaryPool = secondaryItems
	dres.SecondaryScores = secondaryScores
	dres.SecondaryWinner = winner2
	cur.Items[slot] = []Item{winner1, winner2}
	return dres, !sameItems(prev, cur.Items[slot]), nil
}

// bestForDoubleSlot iterates a candidate pool for one half of a double
// slot, holding `held` in the other half. `primary` controls position.
// Returns the winning item and the per-candidate scores.
func bestForDoubleSlot(ctx context.Context, p *Profile, cur Loadout, slot Slot, pool []Item, held Item, primary bool, fs FightStyle, iters int, runner SimRunner, tel *GreedyTelemetry) (Item, []float64, error) {
	bodies := make([][]byte, len(pool))
	for i, c := range pool {
		var pair []Item
		if primary {
			pair = []Item{c, held}
		} else {
			pair = []Item{held, c}
		}
		trial := withSlot(cur, slot, pair)
		bodies[i] = BuildProfile(p, trial)
	}
	scores, err := runFanout(ctx, bodies, fs, iters, runner, tel, slot)
	if err != nil {
		return Item{}, nil, err
	}
	bestIdx := -1
	bestDPS := -1.0
	for i, s := range scores {
		if s > bestDPS {
			bestDPS = s
			bestIdx = i
		}
	}
	if bestIdx < 0 {
		return Item{}, nil, fmt.Errorf("sim slot %s: no successful candidate", slot)
	}
	return pool[bestIdx], scores, nil
}

// runFanout sims the supplied bodies in parallel up to runner.Concurrency()
// and returns per-index DPS scores. Indices that errored are returned as
// -1; the first hard error short-circuits the rest. tel.SimsRun is
// incremented per successful sim.
func runFanout(ctx context.Context, bodies [][]byte, fs FightStyle, iters int, runner SimRunner, tel *GreedyTelemetry, slot Slot) ([]float64, error) {
	type result struct {
		idx int
		dps float64
		err error
	}

	conc := runner.Concurrency()
	if conc < 1 {
		conc = 1
	}
	if conc > len(bodies) {
		conc = len(bodies)
	}
	sem := make(chan struct{}, conc)
	results := make(chan result, len(bodies))

	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	for i, body := range bodies {
		i, body := i, body
		go func() {
			select {
			case sem <- struct{}{}:
			case <-subCtx.Done():
				results <- result{idx: i, err: subCtx.Err()}
				return
			}
			defer func() { <-sem }()
			r, err := runner.Run(subCtx, body, fs, iters)
			results <- result{idx: i, dps: r.DPS, err: err}
		}()
	}

	scores := make([]float64, len(bodies))
	for i := range scores {
		scores[i] = -1
	}
	var firstErr error
	for i := 0; i < len(bodies); i++ {
		r := <-results
		if r.err != nil {
			if firstErr == nil && !errors.Is(r.err, context.Canceled) {
				firstErr = fmt.Errorf("sim slot %s candidate %d: %w", slot, r.idx, r.err)
				cancel()
			}
			continue
		}
		tel.SimsRun++
		scores[r.idx] = r.dps
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return scores, nil
}

// withSlot returns a shallow copy of l with `slot` replaced by `items`.
// Other slot entries are reused (callers must not mutate them).
func withSlot(l Loadout, slot Slot, items []Item) Loadout {
	out := Loadout{Items: make(map[Slot][]Item, len(l.Items))}
	for k, v := range l.Items {
		out.Items[k] = v
	}
	out.Items[slot] = items
	return out
}

// mergeWithCurrent appends each item in `current` to `pool` unless it's
// already present (by fingerprint).
func mergeWithCurrent(pool, current []Item) []Item {
	if len(current) == 0 {
		return pool
	}
	seen := make(map[string]struct{}, len(pool))
	for _, it := range pool {
		seen[it.fingerprint()] = struct{}{}
	}
	out := append([]Item(nil), pool...)
	for _, it := range current {
		fp := it.fingerprint()
		if _, ok := seen[fp]; ok {
			continue
		}
		seen[fp] = struct{}{}
		out = append(out, it)
	}
	return out
}

// excludeByFingerprint returns a copy of items with anything matching
// `drop`'s fingerprint removed.
func excludeByFingerprint(items []Item, drop Item) []Item {
	dropFP := drop.fingerprint()
	out := make([]Item, 0, len(items))
	for _, it := range items {
		if it.fingerprint() == dropFP {
			continue
		}
		out = append(out, it)
	}
	return out
}

// MaxGreedySims returns the upper bound on sims the optimizer will run
// for one fight style given a candidate set.
func MaxGreedySims(cands map[Slot][]Item) int {
	const passes = 2
	per := 0
	for _, slot := range slotOrder {
		n := len(cands[slot])
		if n == 0 {
			continue
		}
		if slot.IsDoubleSlot() {
			if n < 2 {
				per += n
			} else {
				per += 2*n - 1
			}
			continue
		}
		per += n
	}
	return passes * per
}

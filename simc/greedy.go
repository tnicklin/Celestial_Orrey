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

// GreedyOptimize runs a per-slot greedy search over the candidate pool
// for a single fight style. It returns the best assembled loadout and
// the number of sims actually executed.
//
// The algorithm:
//   1. Seed the loadout with the user's currently equipped items, but
//      only for slots whose equipped item is on the eligible track
//      (Hero/Myth) AND appears in the candidate set. This avoids
//      double-counting equipped items that the parser already included
//      in `cands`.
//   2. Sweep slots in `slotOrder`. For each slot, sim every candidate
//      held against the current best for the other slots, pick the
//      winner. For finger/trinket: pick the best ring/trinket #1 first,
//      then sim the remaining candidates against the new winner to pick
//      #2 (sequential picking captures basic pair interaction).
//   3. If any slot's winner differed from the previous best, do one
//      refinement sweep using the new winners as the baseline. Bail
//      early when a sweep produces no changes.
//
// pickBest always treats the slot's currently-held items as candidates
// too, so we can never produce a loadout worse than the seed.
func GreedyOptimize(ctx context.Context, p *Profile, cands map[Slot][]Item, fs FightStyle, iters int, runner SimRunner, onProgress GreedyProgress) (Loadout, GreedyTelemetry, error) {
	cur := seedLoadout(p, cands)
	tel := GreedyTelemetry{}

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
				slotChanged, err := pickDoubleSlot(ctx, p, cur, slot, items, fs, iters, runner, &tel)
				if err != nil {
					return Loadout{}, tel, err
				}
				if slotChanged {
					changed = true
				}
				continue
			}

			slotChanged, err := pickSingleSlot(ctx, p, cur, slot, items, fs, iters, runner, &tel)
			if err != nil {
				return Loadout{}, tel, err
			}
			if slotChanged {
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	return cur, tel, nil
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
			// Slot has candidates but nothing equipped — let the first
			// sweep pick. Use the first candidate (or first two for
			// double slots) as a starting placeholder.
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
// Returns whether the winner differed from what was already in cur.
func pickSingleSlot(ctx context.Context, p *Profile, cur Loadout, slot Slot, items []Item, fs FightStyle, iters int, runner SimRunner, tel *GreedyTelemetry) (bool, error) {
	pool := mergeWithCurrent(items, cur.Items[slot])

	bodies := make([][]byte, len(pool))
	for i, c := range pool {
		trial := withSlot(cur, slot, []Item{c})
		bodies[i] = BuildProfile(p, trial)
	}

	bestIdx, err := runFanout(ctx, bodies, fs, iters, runner, tel, slot)
	if err != nil {
		return false, err
	}
	prev := cur.Items[slot]
	cur.Items[slot] = []Item{pool[bestIdx]}
	return !sameItems(prev, cur.Items[slot]), nil
}

// pickDoubleSlot does the sequential pick for finger/trinket: first the
// "primary" item (held against the equipped secondary), then the
// "secondary" (held against the freshly-picked primary). Updates cur in
// place and returns whether either pick differed.
func pickDoubleSlot(ctx context.Context, p *Profile, cur Loadout, slot Slot, items []Item, fs FightStyle, iters int, runner SimRunner, tel *GreedyTelemetry) (bool, error) {
	prev := append([]Item(nil), cur.Items[slot]...)

	// With a single candidate we can't form a pair — drop it into the
	// first sub-slot and bail.
	if len(items) < 2 {
		cur.Items[slot] = []Item{items[0]}
		return !sameItems(prev, cur.Items[slot]), nil
	}

	var heldSecondary Item
	if len(prev) >= 2 {
		heldSecondary = prev[1]
	} else {
		// No equipped pair to hold; use the first candidate as a stand-in.
		heldSecondary = items[0]
	}

	primaryPool := mergeWithCurrent(items, []Item{cur.Items[slot][0]})
	winner1, err := bestForDoubleSlot(ctx, p, cur, slot, primaryPool, heldSecondary, true, fs, iters, runner, tel)
	if err != nil {
		return false, err
	}

	secondaryItems := excludeByFingerprint(items, winner1)
	if len(prev) >= 2 && prev[1].fingerprint() != winner1.fingerprint() {
		secondaryItems = mergeWithCurrent(secondaryItems, []Item{prev[1]})
	}
	if len(secondaryItems) == 0 {
		// User only had one eligible item for this double slot; keep one.
		cur.Items[slot] = []Item{winner1}
		return !sameItems(prev, cur.Items[slot]), nil
	}
	winner2, err := bestForDoubleSlot(ctx, p, cur, slot, secondaryItems, winner1, false, fs, iters, runner, tel)
	if err != nil {
		return false, err
	}
	cur.Items[slot] = []Item{winner1, winner2}
	return !sameItems(prev, cur.Items[slot]), nil
}

// bestForDoubleSlot iterates a candidate pool for one half of a double
// slot, holding `held` in the other half. `primary` controls position.
func bestForDoubleSlot(ctx context.Context, p *Profile, cur Loadout, slot Slot, pool []Item, held Item, primary bool, fs FightStyle, iters int, runner SimRunner, tel *GreedyTelemetry) (Item, error) {
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
	bestIdx, err := runFanout(ctx, bodies, fs, iters, runner, tel, slot)
	if err != nil {
		return Item{}, err
	}
	return pool[bestIdx], nil
}

// runFanout sims the supplied bodies in parallel up to runner.Concurrency()
// and returns the index of the highest-DPS result. The first runner error
// short-circuits the rest. tel.SimsRun is incremented per successful sim.
func runFanout(ctx context.Context, bodies [][]byte, fs FightStyle, iters int, runner SimRunner, tel *GreedyTelemetry, slot Slot) (int, error) {
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

	// Use a shared cancelable subcontext so the first error tears down
	// the rest. We still wait for already-running goroutines to settle.
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

	bestIdx := -1
	bestDPS := -1.0
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
		if r.dps > bestDPS {
			bestDPS = r.dps
			bestIdx = r.idx
		}
	}
	if firstErr != nil {
		return 0, firstErr
	}
	if bestIdx < 0 {
		return 0, fmt.Errorf("sim slot %s: no successful candidate", slot)
	}
	return bestIdx, nil
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
// already present (by fingerprint). Order: pool first, then any new
// current items appended at the end.
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
// for one fight style given a candidate set. Used by the orchestrator to
// pre-fill RunInfo.TotalSims so the progress fraction never goes >100%.
func MaxGreedySims(cands map[Slot][]Item) int {
	const passes = 2
	per := 0
	for _, slot := range slotOrder {
		n := len(cands[slot])
		if n == 0 {
			continue
		}
		if slot.IsDoubleSlot() {
			// pickDoubleSlot does at most 2n-1 sims (n primary + n-1 secondary)
			// when n >= 2; n sims when n == 1.
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

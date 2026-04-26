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
	Slot       Slot
	ItemID     int
	Before     string // raw gem_id= value before optimization
	After      string // raw gem_id= value after optimization
	BeforeName string // human-friendly name of the previous gem (when known)
	Name       string // human-friendly name of the chosen gem (when known)
}

// EnchantChange records a single per-item enchant swap. Same usage
// pattern as GemChange.
type EnchantChange struct {
	Slot       Slot
	ItemID     int
	Before     string
	After      string
	BeforeName string
	Name       string
}

// GemEnchantOutcome is the post-phase bundle returned from
// OptimizeGemsAndEnchants. It carries both the user-visible diffs
// (GemChanges/EnchantChanges) and the per-candidate scoring detail
// the report writer consumes. SimsGem / SimsEnchant let the caller
// attribute sim cost to the right phase.
type GemEnchantOutcome struct {
	Loadout        Loadout
	GemChanges     []GemChange
	EnchantChanges []EnchantChange
	GemPhase       GemPhaseReport
	EnchantPhase   EnchantPhaseReport
	SimsGem        int
	SimsEnchant    int
}

// OptimizeGemsAndEnchants runs the gem and ring-enchant optimization
// phases on top of an already-optimized loadout. Returns the new
// loadout (items mutated to reflect new gem/enchant IDs), the
// per-slot change lists for display, and the structured per-phase
// reports captured by the report writer.
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
func OptimizeGemsAndEnchants(ctx context.Context, p *Profile, base Loadout, fs FightStyle, iters int, runner SimRunner, tel *GreedyTelemetry) (GemEnchantOutcome, error) {
	cur := cloneLoadout(base)
	class := p.ClassName()
	spec := p.Spec()

	startGems := snapshotGems(cur)
	startEnchants := snapshotEnchants(cur)

	out := GemEnchantOutcome{
		GemPhase: GemPhaseReport{
			MainstatUniqueID:   mainstatGemIDForSpec(class, spec),
			MainstatUniqueName: gemNameForID(strconv.Itoa(mainstatGemIDForSpec(class, spec))),
		},
	}

	gemOptions := GemsForSpec(class, spec, 2)
	if len(gemOptions) > 0 {
		before := tel.SimsRun
		passes, items, err := optimizeGemsPhase(ctx, p, cur, gemOptions, fs, iters, runner, tel)
		if err != nil {
			return GemEnchantOutcome{}, fmt.Errorf("gem phase: %w", err)
		}
		out.GemPhase.PassesRun = passes
		out.GemPhase.Items = items
		out.SimsGem = tel.SimsRun - before
	}

	enchantOptions := RingEnchantsForSpec(class, spec, 2)
	if len(enchantOptions) > 0 {
		before := tel.SimsRun
		passes, items, err := optimizeRingEnchantsPhase(ctx, p, cur, enchantOptions, fs, iters, runner, tel)
		if err != nil {
			return GemEnchantOutcome{}, fmt.Errorf("enchant phase: %w", err)
		}
		out.EnchantPhase.PassesRun = passes
		out.EnchantPhase.Items = items
		out.SimsEnchant = tel.SimsRun - before
	}

	out.Loadout = cur
	out.GemChanges = diffGems(cur, startGems)
	out.EnchantChanges = diffEnchants(cur, startEnchants)
	return out, nil
}

// optimizeGemsPhase iterates every item with a current gem_id and
// greedy-picks the best gem from `options`. Two passes catch
// stat-stacking DR. Mainstat mono gems are unique-equipped: the
// picker's candidate set excludes the mainstat gem when another item
// is already wearing it. Returns (passesRun, perItemReports, error).
//
// Per-item reports accumulate across passes — a re-pick on pass 2
// overwrites the pass-1 entry so the report reflects final state, not
// intermediate noise.
func optimizeGemsPhase(ctx context.Context, p *Profile, cur Loadout, options []GemOption, fs FightStyle, iters int, runner SimRunner, tel *GreedyTelemetry) (int, []GemItemReport, error) {
	const maxPasses = 2
	mainstatID := mainstatGemIDForSpec(p.ClassName(), p.Spec())
	type key struct {
		slot Slot
		idx  int
	}
	reports := map[key]GemItemReport{}
	passesRun := 0
	for pass := 0; pass < maxPasses; pass++ {
		passesRun = pass + 1
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
				inUseBy := mainstatGemInUseBy(cur, mainstatID, slot, itemIdx)
				flipped, rep, err := pickBestGemForItem(ctx, p, cur, slot, itemIdx, options, mainstatID, inUseBy, fs, iters, runner, tel)
				if err != nil {
					return passesRun, nil, err
				}
				reports[key{slot, itemIdx}] = rep
				if flipped {
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}

	out := make([]GemItemReport, 0, len(reports))
	for _, slot := range slotOrder {
		items, ok := cur.Items[slot]
		if !ok {
			continue
		}
		for i := range items {
			if r, ok := reports[key{slot, i}]; ok {
				out = append(out, r)
			}
		}
	}
	return passesRun, out, nil
}

// pickBestGemForItem sims each gem option in `options` (plus the item's
// current gem to guarantee we never get worse) and replaces the item's
// GemIDs with the winner. Only mutates the FIRST socket on items with
// multiple sockets. The mainstat mono gem is unique-equipped: when
// inUseBySlot is non-empty (some other item is already wearing it) the
// candidate list omits it, except via the always-preserved "keep
// current" candidate at index 0. Returns (changed, perItemReport, err).
func pickBestGemForItem(ctx context.Context, p *Profile, cur Loadout, slot Slot, itemIdx int, options []GemOption, mainstatID int, inUseBySlot string, fs FightStyle, iters int, runner SimRunner, tel *GreedyTelemetry) (bool, GemItemReport, error) {
	originalItems := append([]Item(nil), cur.Items[slot]...)
	originalItem := originalItems[itemIdx]
	originalGem := originalItem.GemIDs
	forbidMainstat := inUseBySlot != ""

	candidates := []string{originalGem}
	seen := map[string]bool{originalGem: true}
	var excluded []ExcludedGem
	for _, opt := range options {
		if forbidMainstat && opt.ID == mainstatID {
			excluded = append(excluded, ExcludedGem{
				ID:          strconv.Itoa(opt.ID),
				Name:        opt.Name,
				Reason:      "mainstat_unique_in_use_by",
				InUseBySlot: inUseBySlot,
			})
			continue
		}
		id := strconv.Itoa(opt.ID)
		if seen[id] {
			continue
		}
		seen[id] = true
		candidates = append(candidates, id)
	}

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
		return false, GemItemReport{}, err
	}
	bestIdx := 0
	bestDPS := scores[0]
	for i := 1; i < len(scores); i++ {
		if scores[i] > bestDPS {
			bestDPS = scores[i]
			bestIdx = i
		}
	}

	report := GemItemReport{
		Slot:                 slot.String(),
		ItemID:               originalItem.ItemID,
		Before:               originalGem,
		BeforeName:           gemNameForID(originalGem),
		Candidates:           buildGemCandidates(candidates, scores, bestIdx),
		ExcludedByConstraint: excluded,
		GapPct:               topTwoGapPct(scores),
	}

	winner := candidates[bestIdx]
	report.After = swapFirst(originalGem, winner)
	report.AfterName = gemNameForID(winner)

	if winner == originalGem {
		return false, report, nil
	}
	updated := originalItem
	updated.GemIDs = swapFirst(originalGem, winner)
	newItems := append([]Item(nil), originalItems...)
	newItems[itemIdx] = updated
	cur.Items[slot] = newItems
	return true, report, nil
}

// optimizeRingEnchantsPhase iterates SlotFinger items and picks the
// best ring enchant from `options`. Two passes for DR symmetry with
// the gem phase. Returns (passesRun, perItemReports, error).
func optimizeRingEnchantsPhase(ctx context.Context, p *Profile, cur Loadout, options []EnchantOption, fs FightStyle, iters int, runner SimRunner, tel *GreedyTelemetry) (int, []EnchantItemReport, error) {
	const maxPasses = 2
	type key struct {
		slot Slot
		idx  int
	}
	reports := map[key]EnchantItemReport{}
	passesRun := 0
	for pass := 0; pass < maxPasses; pass++ {
		passesRun = pass + 1
		changed := false
		items, ok := cur.Items[SlotFinger]
		if !ok {
			break
		}
		for itemIdx := range items {
			flipped, rep, err := pickBestEnchantForItem(ctx, p, cur, SlotFinger, itemIdx, options, fs, iters, runner, tel)
			if err != nil {
				return passesRun, nil, err
			}
			reports[key{SlotFinger, itemIdx}] = rep
			if flipped {
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	out := make([]EnchantItemReport, 0, len(reports))
	if items, ok := cur.Items[SlotFinger]; ok {
		for i := range items {
			if r, ok := reports[key{SlotFinger, i}]; ok {
				out = append(out, r)
			}
		}
	}
	return passesRun, out, nil
}

func pickBestEnchantForItem(ctx context.Context, p *Profile, cur Loadout, slot Slot, itemIdx int, options []EnchantOption, fs FightStyle, iters int, runner SimRunner, tel *GreedyTelemetry) (bool, EnchantItemReport, error) {
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
		return false, EnchantItemReport{}, err
	}
	bestIdx := 0
	bestDPS := scores[0]
	for i := 1; i < len(scores); i++ {
		if scores[i] > bestDPS {
			bestDPS = scores[i]
			bestIdx = i
		}
	}

	report := EnchantItemReport{
		Slot:       slot.String(),
		ItemID:     originalItem.ItemID,
		Before:     originalEnchant,
		BeforeName: enchantNameForID(originalEnchant),
		Candidates: buildEnchantCandidates(candidates, scores, bestIdx),
		GapPct:     topTwoGapPct(scores),
	}

	winner := candidates[bestIdx]
	report.After = winner
	report.AfterName = enchantNameForID(winner)

	if winner == originalEnchant {
		return false, report, nil
	}
	updated := originalItem
	updated.EnchantID = winner
	newItems := append([]Item(nil), originalItems...)
	newItems[itemIdx] = updated
	cur.Items[slot] = newItems
	return true, report, nil
}

// buildGemCandidates pairs candidate IDs with their sim scores and a
// rank derived from sorting. Winner is flagged on the chosen candidate.
func buildGemCandidates(ids []string, scores []float64, winnerIdx int) []GemCandidate {
	type indexed struct {
		i     int
		score float64
	}
	pairs := make([]indexed, len(scores))
	for i, s := range scores {
		pairs[i] = indexed{i, s}
	}
	for i := 0; i < len(pairs); i++ {
		for j := i + 1; j < len(pairs); j++ {
			if pairs[j].score > pairs[i].score {
				pairs[i], pairs[j] = pairs[j], pairs[i]
			}
		}
	}
	rank := make(map[int]int, len(pairs))
	for r, p := range pairs {
		rank[p.i] = r + 1
	}
	out := make([]GemCandidate, len(ids))
	for i, id := range ids {
		out[i] = GemCandidate{
			ID:     id,
			Name:   gemNameForID(id),
			DPS:    scores[i],
			Rank:   rank[i],
			Winner: i == winnerIdx,
		}
	}
	return out
}

func buildEnchantCandidates(ids []string, scores []float64, winnerIdx int) []EnchantCandidate {
	type indexed struct {
		i     int
		score float64
	}
	pairs := make([]indexed, len(scores))
	for i, s := range scores {
		pairs[i] = indexed{i, s}
	}
	for i := 0; i < len(pairs); i++ {
		for j := i + 1; j < len(pairs); j++ {
			if pairs[j].score > pairs[i].score {
				pairs[i], pairs[j] = pairs[j], pairs[i]
			}
		}
	}
	rank := make(map[int]int, len(pairs))
	for r, p := range pairs {
		rank[p.i] = r + 1
	}
	out := make([]EnchantCandidate, len(ids))
	for i, id := range ids {
		out[i] = EnchantCandidate{
			ID:     id,
			Name:   enchantNameForID(id),
			DPS:    scores[i],
			Rank:   rank[i],
			Winner: i == winnerIdx,
		}
	}
	return out
}

// topTwoGapPct returns (top-1 - top-2) / top-1 × 100 for a score slice.
// 0 if fewer than 2 finite scores.
func topTwoGapPct(scores []float64) float64 {
	if len(scores) < 2 {
		return 0
	}
	first, second := -1.0, -1.0
	for _, s := range scores {
		switch {
		case s > first:
			second = first
			first = s
		case s > second:
			second = s
		}
	}
	if first <= 0 {
		return 0
	}
	return (first - second) / first * 100
}

// mainstatGemIDForSpec returns the gem ID of the spec's mainstat mono
// Flawless gem, or 0 if no match is found in the catalog.
func mainstatGemIDForSpec(class, spec string) int {
	ms := MainStatFor(class, spec)
	if ms == "" {
		return 0
	}
	for _, g := range flawlessGems {
		if len(g.Stats) == 1 && g.Stats[0] == ms {
			return g.ID
		}
	}
	return 0
}

// mainstatGemUsedElsewhere reports whether any item OTHER than
// (excludeSlot, excludeIdx) currently has the mainstat mono gem in
// its first socket.
func mainstatGemUsedElsewhere(l Loadout, mainstatID int, excludeSlot Slot, excludeIdx int) bool {
	return mainstatGemInUseBy(l, mainstatID, excludeSlot, excludeIdx) != ""
}

// mainstatGemInUseBy returns the slot name of the OTHER item currently
// wearing the mainstat mono gem (excluding excludeSlot/excludeIdx),
// or "" if none. Used both to enforce uniqueness and to report
// ExcludedGem.InUseBySlot to the consumer.
func mainstatGemInUseBy(l Loadout, mainstatID int, excludeSlot Slot, excludeIdx int) string {
	if mainstatID == 0 {
		return ""
	}
	target := strconv.Itoa(mainstatID)
	for slot, items := range l.Items {
		for i, it := range items {
			if slot == excludeSlot && i == excludeIdx {
				continue
			}
			if it.GemIDs == "" {
				continue
			}
			first := it.GemIDs
			if j := strings.Index(first, "/"); j >= 0 {
				first = first[:j]
			}
			if first == target {
				return slot.String()
			}
		}
	}
	return ""
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
				Slot:       slot,
				ItemID:     it.ItemID,
				Before:     prev,
				After:      it.GemIDs,
				BeforeName: gemNameForID(prev),
				Name:       gemNameForID(it.GemIDs),
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
				Slot:       slot,
				ItemID:     it.ItemID,
				Before:     prev,
				After:      it.EnchantID,
				BeforeName: enchantNameForID(prev),
				Name:       enchantNameForID(it.EnchantID),
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
	gemOpts := len(GemsForSpec(class, spec, 2)) + 1
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

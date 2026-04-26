package simc

// GemOption is a single Flawless gem candidate. ID is what gets emitted
// as `gem_id=...` in the simc profile; Stats lists every stat the gem
// provides (one for monos, two for split-stat combos).
type GemOption struct {
	ID    int
	Name  string
	Stats []string
}

// EnchantOption is a single enchant candidate for a slot. Stat is used
// to filter by spec priority where applicable (e.g. ring stat enchants).
type EnchantOption struct {
	ID   int
	Name string
	Stat string
}

// flawlessGems is the catalog of Flawless gems for Midnight Season 1:
// 7 mono-stat gems (mainstat + 4 secondaries) plus the 6 combo
// secondary-stat gems (C(4,2) = haste+crit, haste+mastery, etc.).
//
// IDs below are placeholders derived from the known item-ID range
// (240888–240918). They have NOT been individually verified against
// wowhead. simc will silently apply zero stats for an unknown ID,
// which would make the gem phase pick arbitrary winners — verify
// before relying on results.
//
// TODO(midnight-s1): verify each ID against wowhead.
var flawlessGems = []GemOption{
	// Mono gems
	{ID: 240888, Name: "Flawless Strength", Stats: []string{StatStrength}},
	{ID: 240889, Name: "Flawless Agility", Stats: []string{StatAgility}},
	{ID: 240890, Name: "Flawless Intellect", Stats: []string{StatIntellect}},
	{ID: 240891, Name: "Flawless Haste", Stats: []string{StatHaste}},
	{ID: 240892, Name: "Flawless Critical Strike", Stats: []string{StatCrit}},
	{ID: 240893, Name: "Flawless Mastery", Stats: []string{StatMastery}},
	{ID: 240894, Name: "Flawless Versatility", Stats: []string{StatVersatility}},

	// Combo gems (split-stat).
	{ID: 240895, Name: "Flawless Haste / Critical Strike", Stats: []string{StatHaste, StatCrit}},
	{ID: 240896, Name: "Flawless Haste / Mastery", Stats: []string{StatHaste, StatMastery}},
	{ID: 240897, Name: "Flawless Haste / Versatility", Stats: []string{StatHaste, StatVersatility}},
	{ID: 240898, Name: "Flawless Critical Strike / Mastery", Stats: []string{StatCrit, StatMastery}},
	{ID: 240899, Name: "Flawless Critical Strike / Versatility", Stats: []string{StatCrit, StatVersatility}},
	{ID: 240900, Name: "Flawless Mastery / Versatility", Stats: []string{StatMastery, StatVersatility}},
}

// FlawlessGems returns a copy of the catalog so callers can't mutate
// the package-level slice.
func FlawlessGems() []GemOption {
	out := make([]GemOption, len(flawlessGems))
	copy(out, flawlessGems)
	return out
}

// GemsForSpec returns the gem candidates worth trying per socket for a
// given class+spec. Includes:
//   - The mainstat mono gem.
//   - Every mono gem whose stat is in the top-N priority secondaries.
//   - Every combo gem whose BOTH stats are in the top-N priority
//     secondaries (e.g. Haste/Mastery for a Haste>Mastery spec; the
//     Crit/Versatility combo is excluded if those aren't top-N).
//
// topN <= 0 means use all 4 secondaries (no filtering).
func GemsForSpec(class, spec string, topN int) []GemOption {
	mainstat := MainStatFor(class, spec)
	priorities := StatPriorityFor(class, spec)
	if topN > 0 && topN < len(priorities) {
		priorities = priorities[:topN]
	}

	wanted := map[string]bool{}
	for _, s := range priorities {
		wanted[s] = true
	}

	out := make([]GemOption, 0, len(flawlessGems))
	for _, g := range flawlessGems {
		// Mainstat mono — always include the spec's mainstat gem.
		if len(g.Stats) == 1 && g.Stats[0] == mainstat {
			out = append(out, g)
			continue
		}
		// Every other gem: every stat must be in `wanted`.
		allWanted := len(g.Stats) > 0
		for _, s := range g.Stats {
			if !wanted[s] {
				allWanted = false
				break
			}
		}
		if allWanted {
			out = append(out, g)
		}
	}
	return out
}

// ringEnchants is the catalog of meaningful ring enchant options for
// Midnight Season 1. Other slots (chest/legs/wrists/feet/cloak) on the
// current expansion either have a single dominant choice or are
// defensive — we don't optimize them.
//
// TODO(midnight-s1): verify each enchant ID against wowhead. The IDs
// below are placeholders.
var ringEnchants = []EnchantOption{
	{ID: 7997, Name: "Radiant Critical Strike", Stat: StatCrit},
	{ID: 7998, Name: "Radiant Haste", Stat: StatHaste},
	{ID: 7999, Name: "Radiant Mastery", Stat: StatMastery},
	{ID: 8000, Name: "Radiant Versatility", Stat: StatVersatility},
}

// RingEnchantsForSpec returns the ring-enchant candidates worth trying
// for a class+spec. Same narrowing logic as GemsForSpec.
func RingEnchantsForSpec(class, spec string, topN int) []EnchantOption {
	priorities := StatPriorityFor(class, spec)
	if topN > 0 && topN < len(priorities) {
		priorities = priorities[:topN]
	}
	wanted := map[string]bool{}
	for _, s := range priorities {
		wanted[s] = true
	}
	out := make([]EnchantOption, 0, len(wanted))
	for _, e := range ringEnchants {
		if wanted[e.Stat] {
			out = append(out, e)
		}
	}
	return out
}

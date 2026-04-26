package simc

import "strings"

// Stat names. We use lowercase strings throughout so spec priority
// tables and gem catalog can be cross-referenced cheaply.
const (
	StatStrength    = "strength"
	StatAgility     = "agility"
	StatIntellect   = "intellect"
	StatHaste       = "haste"
	StatCrit        = "crit"
	StatMastery     = "mastery"
	StatVersatility = "versatility"
)

// classMainStat maps class identifier (lowercased) to its primary
// attribute for current expansions. Demon Hunter, Druid, Hunter, Monk,
// Rogue, Shaman (enh) → agi; Death Knight, Paladin, Warrior → str;
// Evoker, Mage, Priest, Warlock, Druid (caster), Shaman (caster) → int.
//
// For dual-attribute classes (Druid, Shaman) we pick by spec in the
// table below; this fallback catches anything missed.
var classMainStat = map[string]string{
	"deathknight": StatStrength,
	"demonhunter": StatAgility,
	"druid":       StatAgility, // overridden per-spec
	"evoker":      StatIntellect,
	"hunter":      StatAgility,
	"mage":        StatIntellect,
	"monk":        StatAgility,
	"paladin":     StatStrength,
	"priest":      StatIntellect,
	"rogue":       StatAgility,
	"shaman":      StatAgility, // overridden per-spec
	"warlock":     StatIntellect,
	"warrior":     StatStrength,
}

// specOverrides handles dual-attribute classes where main stat
// depends on the spec.
var specOverrides = map[string]string{ // key: "class:spec"
	"druid:balance":      StatIntellect,
	"druid:restoration":  StatIntellect,
	"druid:guardian":     StatAgility,
	"druid:feral":        StatAgility,
	"shaman:elemental":   StatIntellect,
	"shaman:restoration": StatIntellect,
	"shaman:enhancement": StatAgility,
}

// MainStatFor returns the primary attribute (one of strength/agility/
// intellect) for the given class+spec. Empty string if unknown.
func MainStatFor(class, spec string) string {
	class = strings.ToLower(strings.TrimSpace(class))
	spec = strings.ToLower(strings.TrimSpace(spec))
	if v, ok := specOverrides[class+":"+spec]; ok {
		return v
	}
	return classMainStat[class]
}

// SpecStatPriorities lists the top secondary stats (descending priority)
// for each DPS/tank/healer spec on the current expansion. The optimizer
// uses these to narrow the gem candidate set per socket — only the top
// N entries are tried instead of all four secondaries, which keeps the
// gem phase short on large bags.
//
// Source: distilled from murlok.gg / icyveins / bloodmallet rankings.
// Not authoritative — re-verify per patch if simc results suggest the
// optimizer is biased the wrong way.
//
// TODO(midnight-s1): refresh per major patch.
var SpecStatPriorities = map[string][]string{
	// Death Knight
	"deathknight:blood":       {StatHaste, StatVersatility, StatMastery, StatCrit},
	"deathknight:frost":       {StatMastery, StatHaste, StatCrit, StatVersatility},
	"deathknight:unholy":      {StatHaste, StatMastery, StatCrit, StatVersatility},
	// Demon Hunter
	"demonhunter:havoc":       {StatCrit, StatHaste, StatMastery, StatVersatility},
	"demonhunter:vengeance":   {StatHaste, StatVersatility, StatMastery, StatCrit},
	// Druid
	"druid:balance":           {StatHaste, StatMastery, StatCrit, StatVersatility},
	"druid:feral":             {StatMastery, StatCrit, StatVersatility, StatHaste},
	"druid:guardian":          {StatVersatility, StatHaste, StatMastery, StatCrit},
	"druid:restoration":       {StatHaste, StatMastery, StatVersatility, StatCrit},
	// Evoker
	"evoker:devastation":      {StatCrit, StatHaste, StatMastery, StatVersatility},
	"evoker:augmentation":     {StatHaste, StatVersatility, StatMastery, StatCrit},
	"evoker:preservation":     {StatHaste, StatVersatility, StatMastery, StatCrit},
	// Hunter
	"hunter:beast_mastery":    {StatHaste, StatCrit, StatMastery, StatVersatility},
	"hunter:marksmanship":     {StatCrit, StatMastery, StatHaste, StatVersatility},
	"hunter:survival":         {StatHaste, StatVersatility, StatMastery, StatCrit},
	// Mage
	"mage:arcane":             {StatMastery, StatHaste, StatVersatility, StatCrit},
	"mage:fire":               {StatHaste, StatCrit, StatVersatility, StatMastery},
	"mage:frost":              {StatHaste, StatCrit, StatMastery, StatVersatility},
	// Monk
	"monk:brewmaster":         {StatVersatility, StatHaste, StatCrit, StatMastery},
	"monk:windwalker":         {StatHaste, StatVersatility, StatCrit, StatMastery},
	"monk:mistweaver":         {StatHaste, StatCrit, StatVersatility, StatMastery},
	// Paladin
	"paladin:protection":      {StatHaste, StatVersatility, StatMastery, StatCrit},
	"paladin:retribution":     {StatHaste, StatMastery, StatCrit, StatVersatility},
	"paladin:holy":            {StatHaste, StatCrit, StatMastery, StatVersatility},
	// Priest
	"priest:discipline":       {StatHaste, StatCrit, StatVersatility, StatMastery},
	"priest:holy":             {StatHaste, StatCrit, StatVersatility, StatMastery},
	"priest:shadow":           {StatHaste, StatMastery, StatCrit, StatVersatility},
	// Rogue
	"rogue:assassination":     {StatMastery, StatCrit, StatVersatility, StatHaste},
	"rogue:outlaw":            {StatHaste, StatVersatility, StatMastery, StatCrit},
	"rogue:subtlety":          {StatMastery, StatCrit, StatVersatility, StatHaste},
	// Shaman
	"shaman:elemental":        {StatHaste, StatMastery, StatCrit, StatVersatility},
	"shaman:enhancement":      {StatHaste, StatMastery, StatCrit, StatVersatility},
	"shaman:restoration":      {StatHaste, StatCrit, StatMastery, StatVersatility},
	// Warlock
	"warlock:affliction":      {StatHaste, StatMastery, StatCrit, StatVersatility},
	"warlock:demonology":      {StatHaste, StatCrit, StatMastery, StatVersatility},
	"warlock:destruction":     {StatHaste, StatCrit, StatMastery, StatVersatility},
	// Warrior
	"warrior:arms":            {StatMastery, StatHaste, StatCrit, StatVersatility},
	"warrior:fury":            {StatMastery, StatHaste, StatCrit, StatVersatility},
	"warrior:protection":      {StatHaste, StatVersatility, StatMastery, StatCrit},
}

// StatPriorityFor returns the ordered secondary-stat priority for a
// class+spec. Returns the canonical four secondaries in arbitrary order
// when the spec is unknown, so the gem phase still has something to
// try (just unfiltered).
func StatPriorityFor(class, spec string) []string {
	class = strings.ToLower(strings.TrimSpace(class))
	spec = strings.ToLower(strings.TrimSpace(spec))
	if pri, ok := SpecStatPriorities[class+":"+spec]; ok {
		return pri
	}
	return []string{StatHaste, StatMastery, StatCrit, StatVersatility}
}

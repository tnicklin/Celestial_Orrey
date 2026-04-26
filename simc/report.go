package simc

import "time"

// ReportSchemaVersion bumps any time the JSON shape changes in a way
// that would break a downstream consumer (the wow-simc-runner-expert
// agent reads this directly). Old reports remain readable; consumers
// should branch on this value.
const ReportSchemaVersion = 1

// Report is the per-run JSON artifact written to disk. It captures
// everything an out-of-process consumer (notably the
// wow-simc-runner-expert subagent) needs to second-guess the
// orchestrator's choices: per-candidate DPS at every phase, the noise
// floor implied by the configured target_error, which constraints
// excluded which gems, how often cross-product actually flipped a
// greedy pick, and the final winning loadout inline as base64 simc.
type Report struct {
	SchemaVersion int                            `json:"schema_version"`
	Run           RunMeta                        `json:"run"`
	Config        ConfigSnapshot                 `json:"config"`
	Input         InputSummary                   `json:"input"`
	FightStyles   map[FightStyle]FightStyleReport `json:"fight_styles"`
	Totals        Totals                         `json:"totals"`
}

// RunMeta is the request-level header. character/realm/spec are pulled
// from the parsed profile; main_stat/stat_priority from the embedded
// catalogs so the agent doesn't need its own copy.
type RunMeta struct {
	ID              uint64    `json:"id"`
	SubmittedAt     time.Time `json:"submitted_at"`
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at"`
	DurationSeconds float64   `json:"duration_seconds"`
	Requester       string    `json:"requester"`
	Character       string    `json:"character"`
	Realm           string    `json:"realm,omitempty"`
	Region          string    `json:"region,omitempty"`
	Class           string    `json:"class"`
	Spec            string    `json:"spec"`
	MainStat        string    `json:"main_stat,omitempty"`
	StatPriority    []string  `json:"stat_priority,omitempty"`
}

// ConfigSnapshot records the orchestrator + queue knobs in effect for
// the run. Lets the agent recommend specific config changes
// ("RankPassIterations is too low for this gap").
type ConfigSnapshot struct {
	RankPassIterations        int     `json:"rank_pass_iterations"`
	FinalPassIterations       int     `json:"final_pass_iterations"`
	RankTargetError           float64 `json:"rank_target_error"`
	QueueWorkers              int     `json:"queue_workers"`
	QueueThreadsPerWorker     int     `json:"queue_threads_per_worker"`
	IndeterminateThresholdPct float64 `json:"indeterminate_threshold_pct"`
	MaxCrossProductSlots      int     `json:"max_cross_product_slots"`
}

// InputSummary describes what the user submitted. profile_b64 is the
// original /simc paste verbatim; agent uses it as the source of truth
// when reconstructing scenarios.
type InputSummary struct {
	ProfileBytes      int            `json:"profile_bytes"`
	ProfileSHA256     string         `json:"profile_sha256"`
	ProfileB64        string         `json:"profile_b64"`
	CandidatesPerSlot map[string]int `json:"candidates_per_slot"`
	Warnings          InputWarnings  `json:"warnings"`
}

// InputWarnings flags slots the candidate analyzer flagged as
// problematic. Agent uses these to explain unexpectedly small deltas.
type InputWarnings struct {
	NoHeroOrMyth  []string `json:"no_hero_or_myth,omitempty"`
	FewerThanTwo  []string `json:"fewer_than_two,omitempty"`
}

// FightStyleReport is the per-fight-style payload — the workhorse of
// the report. Every phase carries enough per-candidate data for the
// agent to spot indeterminacies, DR effects, dominant winners.
type FightStyleReport struct {
	Style          FightStyle           `json:"style"`
	BaselineDPS    float64              `json:"baseline_dps"`
	BestDPS        float64              `json:"best_dps"`
	DeltaDPS       float64              `json:"delta_dps"`
	DeltaPct       float64              `json:"delta_pct"`
	NoiseFloorDPS  float64              `json:"noise_floor_dps"`
	Greedy         GreedyReport         `json:"greedy"`
	CrossProduct   CrossProductReport   `json:"cross_product"`
	GemPhase       GemPhaseReport       `json:"gem_phase"`
	EnchantPhase   EnchantPhaseReport   `json:"enchant_phase"`
	FinalPass      FinalPassReport      `json:"final_pass"`
	WinningLoadout LoadoutReport        `json:"winning_loadout"`
	Diff           DiffReport           `json:"diff_vs_baseline"`
	Phases         PhaseStats           `json:"phases"`
}

// PhaseStats is per-fight-style sim count + wallclock attribution.
// Keys are the canonical phase labels: baseline, greedy,
// cross_product, gem, enchant, final.
type PhaseStats struct {
	SimsByPhase             map[string]int     `json:"sims_by_phase"`
	WallclockSecondsByPhase map[string]float64 `json:"wallclock_seconds_by_phase"`
}

// GreedyReport summarizes the per-slot greedy sweep. SlotPicks has one
// entry per slot that had at least one candidate.
type GreedyReport struct {
	PassesRun int        `json:"passes_run"`
	SlotPicks []SlotPick `json:"slot_picks"`
}

// SlotPick captures one slot's per-candidate scoring + the pick. For
// double slots (finger/trinket) the sequential pick is recorded via
// PrimaryPick / SecondaryPick + their pools; single slots use
// Candidates with WinnerID and RunnerUpID populated.
type SlotPick struct {
	Slot          string             `json:"slot"`
	Method        string             `json:"method"` // "single" or "sequential_double_slot"
	PoolSize      int                `json:"pool_size"`
	Candidates    []SlotCandidate    `json:"candidates,omitempty"`
	WinnerID      int                `json:"winner_id,omitempty"`
	RunnerUpID    int                `json:"runner_up_id,omitempty"`
	GapDPS        float64            `json:"gap_dps,omitempty"`
	GapPct        float64            `json:"gap_pct,omitempty"`
	Indeterminate bool               `json:"indeterminate,omitempty"`
	PrimaryPool   []SlotCandidate    `json:"primary_pool,omitempty"`
	SecondaryPool []SlotCandidate    `json:"secondary_pool,omitempty"`
	PrimaryPick   *SlotCandidate     `json:"primary_pick,omitempty"`
	SecondaryPick *SlotCandidate     `json:"secondary_pick,omitempty"`
}

// SlotCandidate is one item in a candidate pool with its sim score and
// rank within the pool (1-indexed, ties broken by pool order).
type SlotCandidate struct {
	ItemID int     `json:"item_id"`
	Name   string  `json:"name,omitempty"`
	Ilvl   int     `json:"ilvl"`
	Track  string  `json:"track,omitempty"`
	DPS    float64 `json:"dps"`
	Rank   int     `json:"rank"`
}

// CrossProductReport captures the refinement step. Combos lists every
// 2^k assignment that was actually sim'd; FlippedFromGreedy is the set
// of slots whose pick differed from the greedy winner — the agent's
// hook for spotting whether refinement is load-bearing.
type CrossProductReport struct {
	IndeterminateSlots  []IndetSlot     `json:"indeterminate_slots,omitempty"`
	CombosTried         int             `json:"combos_tried"`
	Combos              []CrossCombo    `json:"combos,omitempty"`
	WinnerIndex         int             `json:"winner_index,omitempty"`
	FlippedFromGreedy   []string        `json:"flipped_from_greedy,omitempty"`
	Skipped             bool            `json:"skipped,omitempty"`
	SkipReason          string          `json:"skip_reason,omitempty"`
}

// IndetSlot is a slot that landed inside the indeterminate threshold
// at the end of greedy.
type IndetSlot struct {
	Slot   string  `json:"slot"`
	GapPct float64 `json:"gap_pct"`
}

// CrossCombo is one row of the cross-product table. Picks maps the slot
// name to the item id that was tried.
type CrossCombo struct {
	Index int            `json:"index"`
	Picks map[string]int `json:"picks"`
	DPS   float64        `json:"dps"`
}

// GemPhaseReport captures the gem-optimization pass. MainstatUniqueID
// is the gem ID enforced as unique-equipped (0 if the spec has no
// known mainstat gem in the catalog).
type GemPhaseReport struct {
	PassesRun          int               `json:"passes_run"`
	MainstatUniqueID   int               `json:"mainstat_unique_id,omitempty"`
	MainstatUniqueName string            `json:"mainstat_unique_name,omitempty"`
	Items              []GemItemReport   `json:"items,omitempty"`
}

// GemItemReport is one item's gem decision: candidates with scores,
// winner, and any constraint exclusions (today only the
// mainstat-uniqueness rule).
type GemItemReport struct {
	Slot                 string             `json:"slot"`
	ItemID               int                `json:"item_id"`
	Before               string             `json:"before"`
	BeforeName           string             `json:"before_name,omitempty"`
	After                string             `json:"after"`
	AfterName            string             `json:"after_name,omitempty"`
	Candidates           []GemCandidate     `json:"candidates"`
	ExcludedByConstraint []ExcludedGem      `json:"excluded_by_constraint,omitempty"`
	GapPct               float64            `json:"gap_pct,omitempty"`
}

// GemCandidate is one gem option scored on this item.
type GemCandidate struct {
	ID     string  `json:"id"`
	Name   string  `json:"name,omitempty"`
	DPS    float64 `json:"dps"`
	Rank   int     `json:"rank"`
	Winner bool    `json:"winner,omitempty"`
}

// ExcludedGem records a gem the picker dropped from the candidate set
// because of a constraint, plus enough context for the agent to
// understand which other slot tripped the constraint.
type ExcludedGem struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Reason      string `json:"reason"`
	InUseBySlot string `json:"in_use_by_slot,omitempty"`
}

// EnchantPhaseReport mirrors GemPhaseReport for ring enchants.
type EnchantPhaseReport struct {
	PassesRun int                  `json:"passes_run"`
	Items     []EnchantItemReport  `json:"items,omitempty"`
}

// EnchantItemReport is one ring's enchant decision.
type EnchantItemReport struct {
	Slot       string             `json:"slot"`
	ItemID     int                `json:"item_id"`
	Before     string             `json:"before"`
	BeforeName string             `json:"before_name,omitempty"`
	After      string             `json:"after"`
	AfterName  string             `json:"after_name,omitempty"`
	Candidates []EnchantCandidate `json:"candidates"`
	GapPct     float64            `json:"gap_pct,omitempty"`
}

// EnchantCandidate is one enchant option scored on this ring.
type EnchantCandidate struct {
	ID     string  `json:"id"`
	Name   string  `json:"name,omitempty"`
	DPS    float64 `json:"dps"`
	Rank   int     `json:"rank"`
	Winner bool    `json:"winner,omitempty"`
}

// FinalPassReport is the high-iteration verification sim on the
// fully-optimized loadout.
type FinalPassReport struct {
	Iterations      int     `json:"iterations"`
	TargetError     float64 `json:"target_error"`
	DPS             float64 `json:"dps"`
	DurationSeconds float64 `json:"duration_seconds"`
}

// LoadoutReport is the winning gear set, item-resolved. ProfileB64 is
// the full simc body (post-rescale, with chosen gems/enchants).
type LoadoutReport struct {
	Slots      map[string][]LoadoutItem `json:"slots"`
	ProfileB64 string                   `json:"profile_b64"`
}

// LoadoutItem is one item in the winning set.
type LoadoutItem struct {
	ItemID    int    `json:"item_id"`
	Name      string `json:"name,omitempty"`
	Ilvl      int    `json:"ilvl"`
	Track     string `json:"track,omitempty"`
	GemIDs    string `json:"gem_ids,omitempty"`
	EnchantID string `json:"enchant_id,omitempty"`
}

// DiffReport mirrors the Discord embed's change lists, but resolved
// (item names, gem names) for offline consumption.
type DiffReport struct {
	SlotChanges    []SlotChange    `json:"slot_changes,omitempty"`
	GemChanges     []GemChange     `json:"gem_changes,omitempty"`
	EnchantChanges []EnchantChange `json:"enchant_changes,omitempty"`
}

// Totals aggregates run-wide cost data so the agent can recommend
// budget reallocation between phases.
type Totals struct {
	SimsRun                  int                `json:"sims_run"`
	SimsPerPhase             map[string]int     `json:"sims_per_phase,omitempty"`
	WallclockPerPhaseSeconds map[string]float64 `json:"wallclock_per_phase_seconds,omitempty"`
	QueueConcurrencyObserved float64            `json:"queue_concurrency_observed,omitempty"`
	QueueConcurrencyPeak     int                `json:"queue_concurrency_peak,omitempty"`
}

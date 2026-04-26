package simc

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tnicklin/celestial_orrey/logger"
)

// OrchestratorConfig tunes the sim orchestrator.
type OrchestratorConfig struct {
	RankPassIterations  int           `yaml:"rank_pass_iterations"`
	FinalPassIterations int           `yaml:"final_pass_iterations"`
	RankTargetError     float64       `yaml:"rank_target_error"`
	ProgressEvery       int           `yaml:"progress_every"`
	ProgressMinInterval time.Duration `yaml:"progress_min_interval"`
	HistorySize         int           `yaml:"history_size"`
	ReportDir           string        `yaml:"report_dir"`
}

// Defaults applies default values to the config.
func (c *OrchestratorConfig) Defaults() {
	if c.RankPassIterations <= 0 {
		c.RankPassIterations = 1000
	}
	if c.FinalPassIterations <= 0 {
		c.FinalPassIterations = 10000
	}
	if c.RankTargetError <= 0 {
		c.RankTargetError = 0.5
	}
	if c.ProgressEvery <= 0 {
		c.ProgressEvery = 50
	}
	if c.ProgressMinInterval <= 0 {
		c.ProgressMinInterval = 2 * time.Minute
	}
	if c.HistorySize <= 0 {
		c.HistorySize = 5
	}
}

// RunID identifies a single sim request.
type RunID uint64

// ProgressFunc is invoked when the orchestrator wants to surface progress
// to the user. Implementations should not block.
type ProgressFunc func(RunInfo)

// CompletionFunc is invoked exactly once when a run terminates (success,
// failure, or cancellation).
type CompletionFunc func(RunInfo, *RunResult, error)

// RunInfo is the user-facing snapshot of a single run.
type RunInfo struct {
	ID               RunID                 `json:"id"`
	Requester        string                `json:"requester"`
	CharacterName    string                `json:"character_name,omitempty"`
	ClassName        string                `json:"class_name,omitempty"`
	Spec             string                `json:"spec,omitempty"`
	Phase            string                `json:"phase"`
	SubmittedAt      time.Time             `json:"submitted_at"`
	StartedAt        time.Time             `json:"started_at"`
	FinishedAt       time.Time             `json:"finished_at,omitempty"`
	TotalSims        int                   `json:"total_sims"`
	CompletedSims    int                   `json:"completed_sims"`
	BestDPS          map[FightStyle]float64 `json:"best_dps,omitempty"`
	Status           RunStatus             `json:"status"`
	ErrMsg           string                `json:"err_msg,omitempty"`
	Duration         time.Duration         `json:"duration,omitempty"`
}

// DisplayName returns the character name when known, falling back to
// the Discord requester. Used by the Discord layer in user-facing
// strings.
func (r RunInfo) DisplayName() string {
	if r.CharacterName != "" {
		return r.CharacterName
	}
	return r.Requester
}

// RunStatus is the terminal or in-progress state of a run.
type RunStatus string

const (
	RunStatusQueued    RunStatus = "queued"
	RunStatusRunning   RunStatus = "running"
	RunStatusOK        RunStatus = "ok"
	RunStatusFailed    RunStatus = "failed"
	RunStatusCanceled  RunStatus = "canceled"
)

// RunResult is the final output of a successful run.
type RunResult struct {
	RunID            RunID
	Styles           map[FightStyle]FightStyleResult
	CandidateCount   int
	BaselineProfile  []byte
	Duration         time.Duration
	Stats            CombinationStats
	Report           Report
	ReportPath       string // filesystem path of the JSON report (when written)
}

// FightStyleResult holds the baseline + best DPS plus the slot diff for a
// single fight style. Report is the structured per-phase data used by
// the report writer.
type FightStyleResult struct {
	FightStyle     FightStyle
	BaselineDPS    float64
	BestDPS        float64
	DeltaDPS       float64
	DeltaPct       float64
	BestLoadout    Loadout
	BestProfile    []byte
	SlotChanges    []SlotChange
	GemChanges     []GemChange
	EnchantChanges []EnchantChange
	Report         FightStyleReport
}

// SlotChange describes the per-slot diff between currently equipped and the
// winning loadout.
type SlotChange struct {
	Slot    Slot
	Current []Item // 1 or 2 items (finger/trinket)
	Best    []Item
	Changed bool
}

// OrchestratorSnapshot is exported via Stats() for the discord !simc stats command.
type OrchestratorSnapshot struct {
	Running *RunInfo   `json:"running,omitempty"`
	Pending []RunInfo  `json:"pending"`
	Recent  []RunInfo  `json:"recent"`
}

// Orchestrator orchestrates sim runs.
type Orchestrator interface {
	Submit(profile []byte, requester string, onProgress ProgressFunc, onDone CompletionFunc) (RunID, error)
	Cancel(id RunID) error
	Stats() OrchestratorSnapshot
	Start(ctx context.Context) error
	Stop()
}

// OrchestratorParams holds dependencies.
type OrchestratorParams struct {
	Config OrchestratorConfig
	Queue  Queue
	Logger logger.Logger
}

// DefaultOrchestrator is the concrete orchestrator. One run executes at a
// time; further submissions queue.
type DefaultOrchestrator struct {
	cfg    OrchestratorConfig
	queue  Queue
	logger logger.Logger

	mu       sync.Mutex
	nextID   RunID
	pending  []*runEnvelope
	running  *runEnvelope
	recent   []RunInfo
	wake     chan struct{}
	stop     chan struct{}
	done     chan struct{}
	canceled atomic.Bool
}

var _ Orchestrator = (*DefaultOrchestrator)(nil)

type runEnvelope struct {
	info       RunInfo
	profile    []byte
	onProgress ProgressFunc
	onDone     CompletionFunc
	cancel     context.CancelFunc
}

// NewOrchestrator constructs the orchestrator.
func NewOrchestrator(p OrchestratorParams) *DefaultOrchestrator {
	p.Config.Defaults()
	return &DefaultOrchestrator{
		cfg:    p.Config,
		queue:  p.Queue,
		logger: p.Logger,
		wake:   make(chan struct{}, 1),
	}
}

// Start launches the worker.
func (s *DefaultOrchestrator) Start(_ context.Context) error {
	if s.queue == nil {
		return errors.New("simc: orchestrator requires a queue")
	}
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	go s.workerLoop()
	return nil
}

// Stop signals the worker to exit. Any in-flight run is canceled.
func (s *DefaultOrchestrator) Stop() {
	if s.stop == nil {
		return
	}
	close(s.stop)
	s.mu.Lock()
	if s.running != nil && s.running.cancel != nil {
		s.running.cancel()
	}
	s.mu.Unlock()
	<-s.done
}

// Submit enqueues a new request and returns its assigned ID. The
// onProgress and onDone callbacks may be nil.
func (s *DefaultOrchestrator) Submit(profile []byte, requester string, onProgress ProgressFunc, onDone CompletionFunc) (RunID, error) {
	if len(profile) == 0 {
		return 0, errors.New("simc: empty profile")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	id := s.nextID
	env := &runEnvelope{
		info: RunInfo{
			ID:          id,
			Requester:   requester,
			SubmittedAt: time.Now(),
			Status:      RunStatusQueued,
			Phase:       "queued",
		},
		profile:    profile,
		onProgress: onProgress,
		onDone:     onDone,
	}
	s.pending = append(s.pending, env)
	s.wakeWorker()
	return id, nil
}

// Cancel cancels a pending or in-flight run.
func (s *DefaultOrchestrator) Cancel(id RunID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running != nil && s.running.info.ID == id {
		if s.running.cancel != nil {
			s.running.cancel()
		}
		return nil
	}
	for i, env := range s.pending {
		if env.info.ID != id {
			continue
		}
		s.pending = append(s.pending[:i], s.pending[i+1:]...)
		env.info.Status = RunStatusCanceled
		env.info.FinishedAt = time.Now()
		s.recordRecentLocked(env.info)
		if env.onDone != nil {
			go env.onDone(env.info, nil, errors.New("canceled before start"))
		}
		return nil
	}
	return ErrJobNotFound
}

// Stats returns a snapshot for the bot's status command.
func (s *DefaultOrchestrator) Stats() OrchestratorSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := OrchestratorSnapshot{}
	if s.running != nil {
		ri := s.running.info
		snap.Running = &ri
	}
	for _, e := range s.pending {
		snap.Pending = append(snap.Pending, e.info)
	}
	snap.Recent = append(snap.Recent, s.recent...)
	return snap
}

func (s *DefaultOrchestrator) wakeWorker() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *DefaultOrchestrator) workerLoop() {
	defer close(s.done)
	for {
		select {
		case <-s.stop:
			s.drainPending()
			return
		case <-s.wake:
		}
		for {
			env := s.popNextLocked()
			if env == nil {
				break
			}
			s.executeRun(env)
		}
	}
}

func (s *DefaultOrchestrator) drainPending() {
	s.mu.Lock()
	pending := s.pending
	s.pending = nil
	s.mu.Unlock()
	for _, env := range pending {
		env.info.Status = RunStatusCanceled
		env.info.FinishedAt = time.Now()
		s.mu.Lock()
		s.recordRecentLocked(env.info)
		s.mu.Unlock()
		if env.onDone != nil {
			env.onDone(env.info, nil, errors.New("service stopping"))
		}
	}
}

func (s *DefaultOrchestrator) popNextLocked() *runEnvelope {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running != nil || len(s.pending) == 0 {
		return nil
	}
	env := s.pending[0]
	s.pending = s.pending[1:]
	s.running = env
	env.info.Status = RunStatusRunning
	env.info.StartedAt = time.Now()
	env.info.Phase = "starting"
	return env
}

func (s *DefaultOrchestrator) executeRun(env *runEnvelope) {
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	env.cancel = cancel
	s.mu.Unlock()
	defer cancel()

	res, err := s.runOnce(ctx, env)

	s.mu.Lock()
	env.info.FinishedAt = time.Now()
	env.info.Duration = env.info.FinishedAt.Sub(env.info.StartedAt)
	switch {
	case err == nil:
		env.info.Status = RunStatusOK
		env.info.Phase = "completed"
	case errors.Is(err, context.Canceled):
		env.info.Status = RunStatusCanceled
		env.info.Phase = "canceled"
	default:
		env.info.Status = RunStatusFailed
		env.info.Phase = "failed"
		env.info.ErrMsg = err.Error()
	}
	s.recordRecentLocked(env.info)
	s.running = nil
	hasMore := len(s.pending) > 0
	infoCopy := env.info
	s.mu.Unlock()

	if env.onDone != nil {
		env.onDone(infoCopy, res, err)
	}
	if hasMore {
		s.wakeWorker()
	}
}

func (s *DefaultOrchestrator) recordRecentLocked(info RunInfo) {
	s.recent = append([]RunInfo{info}, s.recent...)
	if len(s.recent) > s.cfg.HistorySize {
		s.recent = s.recent[:s.cfg.HistorySize]
	}
}

// runOnce executes the full pipeline for a single submitted request.
func (s *DefaultOrchestrator) runOnce(ctx context.Context, env *runEnvelope) (*RunResult, error) {
	profile, err := ParseProfile(env.profile)
	if err != nil {
		return nil, fmt.Errorf("parse profile: %w", err)
	}
	cands := profile.CandidatesBySlot()
	stats := AnalyzeCandidates(cands)

	candidateCount := 0
	for _, slot := range slotOrder {
		candidateCount += len(cands[slot])
	}

	// Per fight style: 1 baseline + greedy sims + cross-product refine
	// (worst case) + gem/enchant on the projected post-greedy loadout
	// (estimated using equipped items as a proxy) + 1 final.
	greedyMax := MaxGreedySims(cands)
	crossMax := MaxCrossProductSims(MaxCrossProductSlots)
	gemEnchantMax := MaxGemEnchantSims(estimatedPostGreedyLoadout(profile, cands), profile.ClassName(), profile.Spec())
	perStyle := 2 + greedyMax + crossMax + gemEnchantMax
	totalSims := perStyle * len(FightStyleOrder)

	s.updateInfo(env, func(i *RunInfo) {
		i.TotalSims = totalSims
		i.CharacterName = profile.CharacterName()
		i.ClassName = profile.ClassName()
		i.Spec = profile.Spec()
		i.Phase = fmt.Sprintf("starting (%d candidates × %d styles)", candidateCount, len(FightStyleOrder))
	})

	baseline := BuildEquippedBaseline(profile)

	res := &RunResult{
		RunID:           env.info.ID,
		Styles:          make(map[FightStyle]FightStyleResult, len(FightStyleOrder)),
		CandidateCount:  candidateCount,
		BaselineProfile: baseline,
		Stats:           stats,
	}

	for _, fs := range FightStyleOrder {
		styleResult, err := s.runFightStyle(ctx, env, profile, baseline, cands, fs)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", fs, err)
		}
		res.Styles[fs] = styleResult
		fsCopy := fs
		s.updateInfo(env, func(i *RunInfo) {
			if i.BestDPS == nil {
				i.BestDPS = make(map[FightStyle]float64, len(FightStyleOrder))
			}
			i.BestDPS[fsCopy] = styleResult.BestDPS
		})
	}

	res.Duration = time.Since(env.info.StartedAt)

	// Assemble + persist the structured report so the
	// wow-simc-runner-expert agent can iterate over the run.
	res.Report = s.buildReport(env, profile, res, totalSims)
	if s.cfg.ReportDir != "" {
		path, werr := WriteReport(s.cfg.ReportDir, res.Report)
		if werr != nil {
			s.logger.WarnW("write sim report", "run", env.info.ID, "error", werr)
		} else {
			res.ReportPath = path
		}
	}
	return res, nil
}

// buildReport assembles the run-level Report from RunInfo + per-style
// FightStyleResults. Per-phase sims and wallclock are summed across
// fight styles into Totals.
func (s *DefaultOrchestrator) buildReport(env *runEnvelope, profile *Profile, res *RunResult, totalSimsBudget int) Report {
	sum := sha256.Sum256(env.profile)
	rep := Report{
		SchemaVersion: ReportSchemaVersion,
		Run: RunMeta{
			ID:              uint64(env.info.ID),
			SubmittedAt:     env.info.SubmittedAt,
			StartedAt:       env.info.StartedAt,
			FinishedAt:      env.info.FinishedAt,
			DurationSeconds: res.Duration.Seconds(),
			Requester:       env.info.Requester,
			Character:       profile.CharacterName(),
			Realm:           profile.Realm(),
			Region:          profile.Region(),
			Class:           profile.ClassName(),
			Spec:            profile.Spec(),
			MainStat:        MainStatFor(profile.ClassName(), profile.Spec()),
			StatPriority:    StatPriorityFor(profile.ClassName(), profile.Spec()),
		},
		Config: ConfigSnapshot{
			RankPassIterations:        s.cfg.RankPassIterations,
			FinalPassIterations:       s.cfg.FinalPassIterations,
			RankTargetError:           s.cfg.RankTargetError,
			QueueWorkers:              s.queue.Concurrency(),
			IndeterminateThresholdPct: IndeterminateThreshold * 100,
			MaxCrossProductSlots:      MaxCrossProductSlots,
		},
		Input: InputSummary{
			ProfileBytes:      len(env.profile),
			ProfileSHA256:     hex.EncodeToString(sum[:]),
			ProfileB64:        base64.StdEncoding.EncodeToString(env.profile),
			CandidatesPerSlot: candidatesPerSlot(profile.CandidatesBySlot()),
			Warnings: InputWarnings{
				NoHeroOrMyth: slotNames(res.Stats.Empty),
				FewerThanTwo: slotNames(res.Stats.DoubleEmpty),
			},
		},
		FightStyles: make(map[FightStyle]FightStyleReport, len(res.Styles)),
	}
	for fs, sr := range res.Styles {
		rep.FightStyles[fs] = sr.Report
	}
	rep.Totals = aggregateTotals(rep.FightStyles)
	return rep
}

func candidatesPerSlot(cands map[Slot][]Item) map[string]int {
	out := make(map[string]int, len(cands))
	for slot, items := range cands {
		out[slot.String()] = len(items)
	}
	return out
}

func slotNames(slots []Slot) []string {
	out := make([]string, 0, len(slots))
	for _, s := range slots {
		out = append(out, s.String())
	}
	return out
}

func aggregateTotals(per map[FightStyle]FightStyleReport) Totals {
	t := Totals{
		SimsPerPhase:             map[string]int{},
		WallclockPerPhaseSeconds: map[string]float64{},
	}
	for _, r := range per {
		for k, v := range r.Phases.SimsByPhase {
			t.SimsPerPhase[k] += v
			t.SimsRun += v
		}
		for k, v := range r.Phases.WallclockSecondsByPhase {
			t.WallclockPerPhaseSeconds[k] += v
		}
	}
	return t
}

// runFightStyle runs the full pipeline (baseline → greedy → cross-product
// refine → gem/enchant → final pass) for one fight style and assembles
// the per-style report.
func (s *DefaultOrchestrator) runFightStyle(ctx context.Context, env *runEnvelope, profile *Profile, baseline []byte, cands map[Slot][]Item, fs FightStyle) (FightStyleResult, error) {
	out := FightStyleResult{FightStyle: fs}
	report := FightStyleReport{
		Style: fs,
	}
	timings := make(map[string]float64)
	simsPerPhase := make(map[string]int)

	// Baseline.
	t0 := time.Now()
	s.updateInfo(env, func(i *RunInfo) { i.Phase = fmt.Sprintf("baseline (%s)", fs) })
	baselineRes, err := s.runOne(ctx, env, baseline, fs, s.cfg.FinalPassIterations, 0)
	if err != nil {
		return out, fmt.Errorf("baseline: %w", err)
	}
	out.BaselineDPS = baselineRes.DPS
	report.BaselineDPS = baselineRes.DPS
	report.NoiseFloorDPS = baselineRes.DPS * s.cfg.RankTargetError / 100
	s.bumpCompleted(env)
	timings["baseline"] = time.Since(t0).Seconds()
	simsPerPhase["baseline"] = 1

	// Greedy sweep.
	lastProgress := time.Now()
	progress := func(pass int, slot Slot, slotIdx, slotsTotal int) {
		if time.Since(lastProgress) < s.cfg.ProgressMinInterval {
			return
		}
		lastProgress = time.Now()
		s.updateInfo(env, func(info *RunInfo) {
			info.Phase = fmt.Sprintf("greedy %s pass %d: %s (%d/%d)", fs, pass+1, slot, slotIdx+1, slotsTotal)
		})
		s.notifyProgress(env)
	}

	runner := &orchestratorRunner{orch: s, env: env, targetError: s.cfg.RankTargetError}
	t0 = time.Now()
	bestLoadout, slotResults, greedyTel, err := GreedyOptimize(ctx, profile, cands, fs, s.cfg.RankPassIterations, runner, progress)
	if err != nil {
		return out, fmt.Errorf("greedy: %w", err)
	}
	report.Greedy = buildGreedyReport(greedyTel.PassesRun, slotResults)
	timings["greedy"] = time.Since(t0).Seconds()
	simsPerPhase["greedy"] = greedyTel.SimsRun

	// Cross-product refine.
	t0 = time.Now()
	postTel := &GreedyTelemetry{}
	s.updateInfo(env, func(i *RunInfo) { i.Phase = fmt.Sprintf("refine (%s)", fs) })
	refined, crossReport, err := CrossProductRefine(ctx, profile, bestLoadout, slotResults.Slots, fs, s.cfg.RankPassIterations, runner, postTel)
	if err != nil {
		return out, fmt.Errorf("cross-product refine: %w", err)
	}
	report.CrossProduct = crossReport
	timings["cross_product"] = time.Since(t0).Seconds()
	simsPerPhase["cross_product"] = postTel.SimsRun

	// Gem + enchant.
	t0 = time.Now()
	s.updateInfo(env, func(i *RunInfo) { i.Phase = fmt.Sprintf("gems + enchants (%s)", fs) })
	geTel := &GreedyTelemetry{}
	gemOutcome, err := OptimizeGemsAndEnchants(ctx, profile, refined, fs, s.cfg.RankPassIterations, runner, geTel)
	if err != nil {
		return out, fmt.Errorf("gem/enchant: %w", err)
	}
	report.GemPhase = gemOutcome.GemPhase
	report.EnchantPhase = gemOutcome.EnchantPhase
	timings["gem"] = time.Since(t0).Seconds() * float64(gemOutcome.SimsGem) / float64(maxInt(gemOutcome.SimsGem+gemOutcome.SimsEnchant, 1))
	timings["enchant"] = time.Since(t0).Seconds() - timings["gem"]
	simsPerPhase["gem"] = gemOutcome.SimsGem
	simsPerPhase["enchant"] = gemOutcome.SimsEnchant

	// Final pass.
	t0 = time.Now()
	s.updateInfo(env, func(i *RunInfo) { i.Phase = fmt.Sprintf("final pass (%s)", fs) })
	bestProfile := BuildProfile(profile, gemOutcome.Loadout)
	finalRes, err := s.runOne(ctx, env, bestProfile, fs, s.cfg.FinalPassIterations, 0)
	if err != nil {
		return out, fmt.Errorf("final pass: %w", err)
	}
	s.bumpCompleted(env)
	finalDur := time.Since(t0).Seconds()
	timings["final"] = finalDur
	simsPerPhase["final"] = 1

	report.FinalPass = FinalPassReport{
		Iterations:      s.cfg.FinalPassIterations,
		TargetError:     0,
		DPS:             finalRes.DPS,
		DurationSeconds: finalDur,
	}

	out.BestLoadout = gemOutcome.Loadout
	out.BestDPS = finalRes.DPS
	out.DeltaDPS = finalRes.DPS - out.BaselineDPS
	if out.BaselineDPS > 0 {
		out.DeltaPct = out.DeltaDPS / out.BaselineDPS * 100
	}
	out.BestProfile = bestProfile
	out.SlotChanges = computeSlotChanges(profile, gemOutcome.Loadout)
	out.GemChanges = gemOutcome.GemChanges
	out.EnchantChanges = gemOutcome.EnchantChanges

	report.BestDPS = finalRes.DPS
	report.DeltaDPS = out.DeltaDPS
	report.DeltaPct = out.DeltaPct
	report.WinningLoadout = buildLoadoutReport(gemOutcome.Loadout, bestProfile)
	report.Diff = DiffReport{
		SlotChanges:    out.SlotChanges,
		GemChanges:     out.GemChanges,
		EnchantChanges: out.EnchantChanges,
	}
	out.Report = report
	out.Report.Phases = PhaseStats{
		SimsByPhase:             simsPerPhase,
		WallclockSecondsByPhase: timings,
	}
	return out, nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// buildGreedyReport translates the per-slot scoring data captured by
// GreedyOptimize into a GreedyReport — single slots get a candidates
// list with rank/winner; double slots get split primary/secondary
// pools.
func buildGreedyReport(passesRun int, results GreedyResults) GreedyReport {
	rep := GreedyReport{PassesRun: passesRun}
	for _, slot := range slotOrder {
		if res, ok := results.Slots[slot]; ok && len(res.Pool) > 0 {
			rep.SlotPicks = append(rep.SlotPicks, buildSingleSlotPick(slot, res))
			continue
		}
		if res, ok := results.Doubles[slot]; ok && len(res.PrimaryPool) > 0 {
			rep.SlotPicks = append(rep.SlotPicks, buildDoubleSlotPick(slot, res))
		}
	}
	return rep
}

func buildSingleSlotPick(slot Slot, res GreedySlotResult) SlotPick {
	candidates := make([]SlotCandidate, len(res.Pool))
	rank := rankIndices(res.Scores)
	bestIdx, runnerUpIdx := topTwoIndices(res.Scores)
	for i, it := range res.Pool {
		candidates[i] = SlotCandidate{
			ItemID: it.ItemID,
			Name:   it.Name,
			Ilvl:   it.EffectiveIlvl(),
			Track:  it.Track.String(),
			DPS:    res.Scores[i],
			Rank:   rank[i],
		}
	}
	pick := SlotPick{
		Slot:       slot.String(),
		Method:     "single",
		PoolSize:   len(res.Pool),
		Candidates: candidates,
	}
	if bestIdx >= 0 {
		pick.WinnerID = res.Pool[bestIdx].ItemID
	}
	if runnerUpIdx >= 0 {
		pick.RunnerUpID = res.Pool[runnerUpIdx].ItemID
		gap := res.Scores[bestIdx] - res.Scores[runnerUpIdx]
		pick.GapDPS = gap
		if res.Scores[bestIdx] > 0 {
			pick.GapPct = gap / res.Scores[bestIdx] * 100
			pick.Indeterminate = pick.GapPct < IndeterminateThreshold*100
		}
	}
	return pick
}

func buildDoubleSlotPick(slot Slot, res GreedyDoubleSlotResult) SlotPick {
	pick := SlotPick{
		Slot:     slot.String(),
		Method:   "sequential_double_slot",
		PoolSize: len(res.PrimaryPool) + len(res.SecondaryPool),
	}
	if len(res.PrimaryPool) > 0 {
		pick.PrimaryPool = candidatesFromPool(res.PrimaryPool, res.PrimaryScores)
		if c := findCandidate(pick.PrimaryPool, res.PrimaryWinner.ItemID); c != nil {
			cp := *c
			pick.PrimaryPick = &cp
		}
	}
	if len(res.SecondaryPool) > 0 {
		pick.SecondaryPool = candidatesFromPool(res.SecondaryPool, res.SecondaryScores)
		if c := findCandidate(pick.SecondaryPool, res.SecondaryWinner.ItemID); c != nil {
			cp := *c
			pick.SecondaryPick = &cp
		}
	}
	return pick
}

func candidatesFromPool(pool []Item, scores []float64) []SlotCandidate {
	out := make([]SlotCandidate, len(pool))
	rank := rankIndices(scores)
	for i, it := range pool {
		var dps float64
		if i < len(scores) {
			dps = scores[i]
		}
		out[i] = SlotCandidate{
			ItemID: it.ItemID,
			Name:   it.Name,
			Ilvl:   it.EffectiveIlvl(),
			Track:  it.Track.String(),
			DPS:    dps,
			Rank:   rank[i],
		}
	}
	return out
}

func findCandidate(pool []SlotCandidate, itemID int) *SlotCandidate {
	for i := range pool {
		if pool[i].ItemID == itemID {
			return &pool[i]
		}
	}
	return nil
}

// rankIndices returns rank[i] = 1-based rank of scores[i] in
// descending order. Stable on ties (earlier index wins ties).
func rankIndices(scores []float64) []int {
	type kv struct {
		i int
		s float64
	}
	pairs := make([]kv, len(scores))
	for i, s := range scores {
		pairs[i] = kv{i, s}
	}
	sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].s > pairs[j].s })
	out := make([]int, len(scores))
	for r, p := range pairs {
		out[p.i] = r + 1
	}
	return out
}

func topTwoIndices(scores []float64) (best, runnerUp int) {
	best, runnerUp = -1, -1
	for i, s := range scores {
		if best < 0 || s > scores[best] {
			runnerUp = best
			best = i
			continue
		}
		if runnerUp < 0 || s > scores[runnerUp] {
			runnerUp = i
		}
	}
	return
}

// buildLoadoutReport renders a Loadout into the report's slot-keyed
// shape. profileBytes is the simc body used for the final pass; it
// gets base64'd inline.
func buildLoadoutReport(l Loadout, profileBytes []byte) LoadoutReport {
	out := LoadoutReport{
		Slots:      make(map[string][]LoadoutItem, len(l.Items)),
		ProfileB64: base64.StdEncoding.EncodeToString(profileBytes),
	}
	for _, slot := range slotOrder {
		items, ok := l.Items[slot]
		if !ok {
			continue
		}
		var entries []LoadoutItem
		for _, it := range items {
			entries = append(entries, LoadoutItem{
				ItemID:    it.ItemID,
				Name:      it.Name,
				Ilvl:      it.EffectiveIlvl(),
				Track:     it.Track.String(),
				GemIDs:    it.GemIDs,
				EnchantID: it.EnchantID,
			})
		}
		out.Slots[slot.String()] = entries
	}
	return out
}

// orchestratorRunner adapts the orchestrator's queue.Submit/Subscribe
// dance to the SimRunner interface the greedy optimizer expects. Each
// Run() call also bumps the completed-sims counter so the progress
// fraction stays in sync. The adapter is the layer that decides to
// emit target_error on rank-pass sims so simc can bail early on
// converged candidates.
type orchestratorRunner struct {
	orch        *DefaultOrchestrator
	env         *runEnvelope
	targetError float64
}

func (r *orchestratorRunner) Run(ctx context.Context, body []byte, fs FightStyle, iters int) (SimResult, error) {
	res, err := r.orch.runOne(ctx, r.env, body, fs, iters, r.targetError)
	if err == nil {
		r.orch.bumpCompleted(r.env)
	}
	return res, err
}

// Concurrency reports how many sims the orchestrator's queue can run
// in parallel; greedy uses it to bound per-slot fanout.
func (r *orchestratorRunner) Concurrency() int { return r.orch.queue.Concurrency() }

// runOne submits a single sim through the queue and waits for its outcome.
func (s *DefaultOrchestrator) runOne(ctx context.Context, env *runEnvelope, body []byte, fs FightStyle, iters int, targetError float64) (SimResult, error) {
	id, _, err := s.queue.Submit(SimRequest{
		Profile:     body,
		FightStyle:  fs,
		Iterations:  iters,
		TargetError: targetError,
	}, fmt.Sprintf("sim#%d/%s", env.info.ID, env.info.Requester))
	if err != nil {
		return SimResult{}, err
	}
	ch := s.queue.Subscribe(id)
	select {
	case <-ctx.Done():
		_ = s.queue.Cancel(id)
		<-ch
		return SimResult{}, ctx.Err()
	case outcome, ok := <-ch:
		if !ok {
			return SimResult{}, errors.New("no outcome received")
		}
		switch outcome.Status {
		case JobStatusOK:
			return outcome.Result, nil
		default:
			if outcome.Err != nil {
				return SimResult{}, outcome.Err
			}
			return SimResult{}, fmt.Errorf("sim status %s", outcome.Status)
		}
	}
}

func (s *DefaultOrchestrator) bumpCompleted(env *runEnvelope) {
	s.updateInfo(env, func(i *RunInfo) { i.CompletedSims++ })
}

func (s *DefaultOrchestrator) updateInfo(env *runEnvelope, mut func(*RunInfo)) {
	s.mu.Lock()
	mut(&env.info)
	s.mu.Unlock()
}

func (s *DefaultOrchestrator) notifyProgress(env *runEnvelope) {
	if env.onProgress == nil {
		return
	}
	s.mu.Lock()
	info := env.info
	s.mu.Unlock()
	go env.onProgress(info)
}

func computeSlotChanges(profile *Profile, best Loadout) []SlotChange {
	equipped := profile.EquippedBySlot()
	var changes []SlotChange
	for _, slot := range slotOrder {
		cur := equipped[slot]
		bst := best.Items[slot]
		ch := SlotChange{
			Slot:    slot,
			Current: cur,
			Best:    bst,
			Changed: !sameItems(cur, bst),
		}
		changes = append(changes, ch)
	}
	return changes
}

// estimatedPostGreedyLoadout returns a loadout shape used only for sizing
// the gem/enchant sim budget up-front: every slot gets one (or two)
// candidate items so MaxGemEnchantSims can count gem sockets and ring
// slots. We don't know the actual greedy winner yet, so we just take a
// representative item per slot (preferring the equipped one when it's a
// candidate, otherwise the first candidate).
func estimatedPostGreedyLoadout(p *Profile, cands map[Slot][]Item) Loadout {
	equipped := p.EquippedBySlot()
	out := Loadout{Items: make(map[Slot][]Item, len(cands))}
	for _, slot := range slotOrder {
		items := cands[slot]
		if len(items) == 0 {
			continue
		}
		eq := equipped[slot]
		if slot.IsDoubleSlot() && len(eq) >= 2 {
			out.Items[slot] = []Item{eq[0], eq[1]}
			continue
		}
		if len(eq) >= 1 {
			out.Items[slot] = []Item{eq[0]}
			continue
		}
		if slot.IsDoubleSlot() && len(items) >= 2 {
			out.Items[slot] = []Item{items[0], items[1]}
		} else {
			out.Items[slot] = []Item{items[0]}
		}
	}
	return out
}

func sameItems(a, b []Item) bool {
	if len(a) != len(b) {
		return false
	}
	aFP := make([]string, len(a))
	bFP := make([]string, len(b))
	for i := range a {
		aFP[i] = a[i].fingerprint()
	}
	for i := range b {
		bFP[i] = b[i].fingerprint()
	}
	sort.Strings(aFP)
	sort.Strings(bFP)
	for i := range aFP {
		if aFP[i] != bFP[i] {
			return false
		}
	}
	return true
}

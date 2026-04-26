package simc

import (
	"context"
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
	ProgressEvery       int           `yaml:"progress_every"`
	ProgressMinInterval time.Duration `yaml:"progress_min_interval"`
	HistorySize         int           `yaml:"history_size"`
}

// Defaults applies default values to the config.
func (c *OrchestratorConfig) Defaults() {
	if c.RankPassIterations <= 0 {
		c.RankPassIterations = 1000
	}
	if c.FinalPassIterations <= 0 {
		c.FinalPassIterations = 10000
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
	ID               RunID      `json:"id"`
	Requester        string        `json:"requester"`
	Phase            string        `json:"phase"`
	SubmittedAt      time.Time     `json:"submitted_at"`
	StartedAt        time.Time     `json:"started_at"`
	FinishedAt       time.Time     `json:"finished_at,omitempty"`
	TotalSims        int           `json:"total_sims"`
	CompletedSims    int           `json:"completed_sims"`
	BestPatchwerk    float64       `json:"best_patchwerk_dps"`
	BestDungeonSlice float64       `json:"best_dungeon_slice_dps"`
	Status           RunStatus  `json:"status"`
	ErrMsg           string        `json:"err_msg,omitempty"`
	Duration         time.Duration `json:"duration,omitempty"`
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
	Patchwerk        FightStyleResult
	DungeonSlice     FightStyleResult
	CandidateCount   int
	BaselineProfile  []byte
	Duration         time.Duration
	Stats            CombinationStats
}

// FightStyleResult holds the baseline + best DPS plus the slot diff for a
// single fight style.
type FightStyleResult struct {
	FightStyle  FightStyle
	BaselineDPS float64
	BestDPS     float64
	DeltaDPS    float64
	DeltaPct    float64
	BestLoadout Loadout
	BestProfile []byte
	SlotChanges []SlotChange
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

	// Per fight style: 1 baseline + greedy sims + 1 final.
	perStyle := 2 + MaxGreedySims(cands)
	totalSims := perStyle * 2 // patchwerk + dungeon_slice

	s.updateInfo(env, func(i *RunInfo) {
		i.TotalSims = totalSims
		i.Phase = fmt.Sprintf("starting (%d candidates × 2 styles)", candidateCount)
	})

	baseline := BuildEquippedBaseline(profile)

	res := &RunResult{
		RunID:           env.info.ID,
		CandidateCount:  candidateCount,
		BaselineProfile: baseline,
		Stats:           stats,
	}

	for _, fs := range []FightStyle{FightStylePatchwerk, FightStyleDungeonSlice} {
		styleResult, err := s.runFightStyle(ctx, env, profile, baseline, cands, fs)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", fs, err)
		}
		switch fs {
		case FightStylePatchwerk:
			res.Patchwerk = styleResult
			s.updateInfo(env, func(i *RunInfo) { i.BestPatchwerk = styleResult.BestDPS })
		case FightStyleDungeonSlice:
			res.DungeonSlice = styleResult
			s.updateInfo(env, func(i *RunInfo) { i.BestDungeonSlice = styleResult.BestDPS })
		}
	}

	res.Duration = time.Since(env.info.StartedAt)
	return res, nil
}

// runFightStyle runs the baseline, the greedy sweep, and the final
// high-iteration sim on the assembled winning loadout for one style.
func (s *DefaultOrchestrator) runFightStyle(ctx context.Context, env *runEnvelope, profile *Profile, baseline []byte, cands map[Slot][]Item, fs FightStyle) (FightStyleResult, error) {
	out := FightStyleResult{FightStyle: fs}

	s.updateInfo(env, func(i *RunInfo) { i.Phase = fmt.Sprintf("baseline (%s)", fs) })
	baselineRes, err := s.runOne(ctx, env, baseline, fs, s.cfg.FinalPassIterations)
	if err != nil {
		return out, fmt.Errorf("baseline: %w", err)
	}
	out.BaselineDPS = baselineRes.DPS
	s.bumpCompleted(env)

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

	runner := &orchestratorRunner{orch: s, env: env}
	bestLoadout, _, err := GreedyOptimize(ctx, profile, cands, fs, s.cfg.RankPassIterations, runner, progress)
	if err != nil {
		return out, fmt.Errorf("greedy: %w", err)
	}

	s.updateInfo(env, func(i *RunInfo) { i.Phase = fmt.Sprintf("final pass (%s)", fs) })
	bestProfile := BuildProfile(profile, bestLoadout)
	finalRes, err := s.runOne(ctx, env, bestProfile, fs, s.cfg.FinalPassIterations)
	if err != nil {
		return out, fmt.Errorf("final pass: %w", err)
	}
	s.bumpCompleted(env)

	out.BestLoadout = bestLoadout
	out.BestDPS = finalRes.DPS
	out.DeltaDPS = finalRes.DPS - out.BaselineDPS
	if out.BaselineDPS > 0 {
		out.DeltaPct = out.DeltaDPS / out.BaselineDPS * 100
	}
	out.BestProfile = bestProfile
	out.SlotChanges = computeSlotChanges(profile, bestLoadout)
	return out, nil
}

// orchestratorRunner adapts the orchestrator's queue.Submit/Subscribe
// dance to the SimRunner interface the greedy optimizer expects. Each
// Run() call also bumps the completed-sims counter so the progress
// fraction stays in sync.
type orchestratorRunner struct {
	orch *DefaultOrchestrator
	env  *runEnvelope
}

func (r *orchestratorRunner) Run(ctx context.Context, body []byte, fs FightStyle, iters int) (SimResult, error) {
	res, err := r.orch.runOne(ctx, r.env, body, fs, iters)
	if err == nil {
		r.orch.bumpCompleted(r.env)
	}
	return res, err
}

// runOne submits a single sim through the queue and waits for its outcome.
func (s *DefaultOrchestrator) runOne(ctx context.Context, env *runEnvelope, body []byte, fs FightStyle, iters int) (SimResult, error) {
	id, _, err := s.queue.Submit(SimRequest{
		Profile:    body,
		FightStyle: fs,
		Iterations: iters,
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

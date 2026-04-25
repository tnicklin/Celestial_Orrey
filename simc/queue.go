package simc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tnicklin/celestial_orrey/logger"
)

// ErrQueueFull is returned by Submit when the queue has reached cfg.MaxQueueDepth.
var ErrQueueFull = errors.New("simc: queue is full")

// ErrJobNotFound is returned by Cancel when no matching job exists.
var ErrJobNotFound = errors.New("simc: job not found")

// Queue accepts simulation requests, runs them one at a time, and exposes
// real-time stats.
type Queue interface {
	Submit(req SimRequest, requester string) (jobID uint64, position int, err error)
	Cancel(jobID uint64) error
	Stats() Snapshot
	Subscribe(jobID uint64) <-chan JobOutcome
	Start(ctx context.Context) error
	Stop()
}

// SimExecutor is the surface DefaultQueue uses to run sims. Production code
// uses *Runner; tests inject a fake.
type SimExecutor interface {
	Run(ctx context.Context, args RunArgs) (SimResult, error)
	PruneOldReports()
}

// QueueParams holds dependencies for constructing a queue.
type QueueParams struct {
	Config Config
	Runner SimExecutor
	Logger logger.Logger
}

type jobEnvelope struct {
	info        JobInfo
	request     SimRequest
	subscribers []chan JobOutcome
	cancel      context.CancelFunc
	canceled    atomic.Bool
}

// DefaultQueue is the in-memory job queue used by the bot.
type DefaultQueue struct {
	cfg    Config
	runner SimExecutor
	logger logger.Logger
	cgroup *CgroupSampler

	mu       sync.Mutex
	nextID   uint64
	pending  []*jobEnvelope
	running  *jobEnvelope
	finished *historyRing

	completed atomic.Uint64
	failed    atomic.Uint64
	canceled  atomic.Uint64

	liveProc atomic.Pointer[ProcStats]

	wake chan struct{}
	stop chan struct{}
	done chan struct{}

	pruneStop chan struct{}
	pruneDone chan struct{}
}

var _ Queue = (*DefaultQueue)(nil)

// NewQueue constructs a queue. Call Start to begin processing.
func NewQueue(p QueueParams) *DefaultQueue {
	p.Config.Defaults()
	return &DefaultQueue{
		cfg:      p.Config,
		runner:   p.Runner,
		logger:   p.Logger,
		cgroup:   NewCgroupSampler(p.Config.CgroupRoot),
		finished: newHistoryRing(p.Config.HistorySize),
		wake:     make(chan struct{}, 1),
	}
}

// Start launches the worker goroutine and the report-pruner.
func (q *DefaultQueue) Start(_ context.Context) error {
	if q.runner == nil {
		return errors.New("simc: queue requires a runner")
	}
	q.stop = make(chan struct{})
	q.done = make(chan struct{})
	q.pruneStop = make(chan struct{})
	q.pruneDone = make(chan struct{})
	go q.workerLoop()
	go q.pruneLoop()
	return nil
}

// Stop signals the worker to exit and waits for it. Any in-flight job is
// canceled via its context.
func (q *DefaultQueue) Stop() {
	if q.stop != nil {
		close(q.stop)
		q.mu.Lock()
		if q.running != nil && q.running.cancel != nil {
			q.running.cancel()
		}
		q.mu.Unlock()
		<-q.done
	}
	if q.pruneStop != nil {
		close(q.pruneStop)
		<-q.pruneDone
	}
}

// Submit enqueues a sim request. Returns the assigned job ID and the
// 1-based queue position (1 = next to run).
func (q *DefaultQueue) Submit(req SimRequest, requester string) (uint64, int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.pending) >= q.cfg.MaxQueueDepth {
		return 0, 0, ErrQueueFull
	}

	q.nextID++
	id := q.nextID
	env := &jobEnvelope{
		info: JobInfo{
			ID:          id,
			Requester:   requester,
			FightStyle:  req.FightStyle,
			Iterations:  req.Iterations,
			SubmittedAt: time.Now(),
		},
		request: req,
	}
	q.pending = append(q.pending, env)
	pos := len(q.pending)
	if q.running != nil {
		pos++
	}
	q.signalWorker()
	return id, pos, nil
}

// Cancel removes a pending job or signals an in-flight one to abort.
func (q *DefaultQueue) Cancel(jobID uint64) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.running != nil && q.running.info.ID == jobID {
		q.running.canceled.Store(true)
		if q.running.cancel != nil {
			q.running.cancel()
		}
		return nil
	}
	for i, env := range q.pending {
		if env.info.ID != jobID {
			continue
		}
		q.pending = append(q.pending[:i], q.pending[i+1:]...)
		env.canceled.Store(true)
		outcome := JobOutcome{JobID: jobID, Status: JobStatusCanceled, Err: errors.New("canceled before start")}
		q.deliverLocked(env, outcome)
		q.canceled.Add(1)
		q.finished.Push(FinishedJob{
			JobInfo:    env.info,
			FinishedAt: time.Now(),
			Status:     JobStatusCanceled,
			ErrMsg:     "canceled before start",
		})
		return nil
	}
	return ErrJobNotFound
}

// Subscribe returns a channel that will receive the terminal outcome of the
// given job. The channel is buffered (1) so the worker never blocks. Returns
// a closed channel with ErrJobNotFound if the job is unknown.
func (q *DefaultQueue) Subscribe(jobID uint64) <-chan JobOutcome {
	q.mu.Lock()
	defer q.mu.Unlock()
	ch := make(chan JobOutcome, 1)
	if q.running != nil && q.running.info.ID == jobID {
		q.running.subscribers = append(q.running.subscribers, ch)
		return ch
	}
	for _, env := range q.pending {
		if env.info.ID == jobID {
			env.subscribers = append(env.subscribers, ch)
			return ch
		}
	}
	ch <- JobOutcome{JobID: jobID, Status: JobStatusFailed, Err: ErrJobNotFound}
	close(ch)
	return ch
}

// Stats returns a copy of the current subsystem state.
func (q *DefaultQueue) Stats() Snapshot {
	q.mu.Lock()
	var running *JobInfo
	if q.running != nil {
		ri := q.running.info
		running = &ri
	}
	queued := make([]JobInfo, 0, len(q.pending))
	for _, env := range q.pending {
		queued = append(queued, env.info)
	}
	q.mu.Unlock()

	snap := Snapshot{
		Running:        running,
		Queued:         queued,
		QueueDepth:     len(queued),
		QueueCap:       q.cfg.MaxQueueDepth,
		TotalCompleted: q.completed.Load(),
		TotalFailed:    q.failed.Load(),
		TotalCanceled:  q.canceled.Load(),
		Recent:         q.finished.Snapshot(),
		Container:      q.cgroup.Sample(),
		GeneratedAt:    time.Now(),
	}
	if p := q.liveProc.Load(); p != nil {
		copy := *p
		snap.Process = &copy
	}
	return snap
}

func (q *DefaultQueue) signalWorker() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *DefaultQueue) workerLoop() {
	defer close(q.done)
	for {
		select {
		case <-q.stop:
			q.drainPending()
			return
		case <-q.wake:
		}
		for {
			env := q.popNextLocked()
			if env == nil {
				break
			}
			q.execute(env)
		}
	}
}

func (q *DefaultQueue) drainPending() {
	q.mu.Lock()
	pending := q.pending
	q.pending = nil
	q.mu.Unlock()
	for _, env := range pending {
		outcome := JobOutcome{JobID: env.info.ID, Status: JobStatusCanceled, Err: errors.New("queue stopped")}
		q.deliverLocked(env, outcome)
		q.canceled.Add(1)
	}
}

func (q *DefaultQueue) popNextLocked() *jobEnvelope {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.running != nil || len(q.pending) == 0 {
		return nil
	}
	env := q.pending[0]
	q.pending = q.pending[1:]
	q.running = env
	env.info.StartedAt = time.Now()
	return env
}

func (q *DefaultQueue) execute(env *jobEnvelope) {
	ctx, cancel := context.WithTimeout(context.Background(), q.cfg.JobTimeout)
	q.mu.Lock()
	env.cancel = cancel
	q.mu.Unlock()
	defer cancel()

	args := RunArgs{
		JobID:   env.info.ID,
		Request: env.request,
		OnSample: func(s ProcStats) {
			cp := s
			q.liveProc.Store(&cp)
		},
	}
	res, err := q.runner.Run(ctx, args)
	q.liveProc.Store(nil)

	finished := FinishedJob{
		JobInfo:    env.info,
		FinishedAt: time.Now(),
	}
	finished.Duration = finished.FinishedAt.Sub(env.info.StartedAt)

	outcome := JobOutcome{JobID: env.info.ID}
	switch {
	case err == nil:
		outcome.Status = JobStatusOK
		outcome.Result = res
		finished.Status = JobStatusOK
		finished.DPS = res.DPS
		finished.PlayerName = res.PlayerName
		q.completed.Add(1)
	case env.canceled.Load():
		outcome.Status = JobStatusCanceled
		outcome.Err = err
		finished.Status = JobStatusCanceled
		finished.ErrMsg = err.Error()
		q.canceled.Add(1)
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		outcome.Status = JobStatusTimeout
		outcome.Err = err
		finished.Status = JobStatusTimeout
		finished.ErrMsg = err.Error()
		q.failed.Add(1)
	default:
		outcome.Status = JobStatusFailed
		outcome.Err = err
		finished.Status = JobStatusFailed
		finished.ErrMsg = err.Error()
		q.failed.Add(1)
	}

	q.finished.Push(finished)

	q.mu.Lock()
	q.running = nil
	q.deliverLocked(env, outcome)
	hasMore := len(q.pending) > 0
	q.mu.Unlock()

	if hasMore {
		q.signalWorker()
	}
}

// deliverLocked sends the outcome to every subscriber and closes the channels.
// Caller must hold q.mu, except in workerLoop after q.running has been
// detached.
func (q *DefaultQueue) deliverLocked(env *jobEnvelope, outcome JobOutcome) {
	for _, ch := range env.subscribers {
		select {
		case ch <- outcome:
		default:
		}
		close(ch)
	}
	env.subscribers = nil
}

func (q *DefaultQueue) pruneLoop() {
	defer close(q.pruneDone)
	if q.cfg.ReportRetention <= 0 {
		return
	}
	interval := q.cfg.ReportRetention / 4
	if interval < time.Minute {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	q.runner.PruneOldReports()
	for {
		select {
		case <-q.pruneStop:
			return
		case <-ticker.C:
			q.runner.PruneOldReports()
		}
	}
}

// String returns a one-line debug summary.
func (s Snapshot) String() string {
	running := "idle"
	if s.Running != nil {
		running = fmt.Sprintf("#%d %s", s.Running.ID, s.Running.FightStyle)
	}
	return fmt.Sprintf("simc[%s, queued=%d/%d, ok=%d, fail=%d]",
		running, s.QueueDepth, s.QueueCap, s.TotalCompleted, s.TotalFailed)
}

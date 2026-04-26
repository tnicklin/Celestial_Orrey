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

// Queue accepts simulation requests, runs them concurrently up to
// cfg.Workers in parallel, and exposes real-time stats.
type Queue interface {
	Submit(req SimRequest, requester string) (jobID uint64, position int, err error)
	Cancel(jobID uint64) error
	Stats() Snapshot
	Subscribe(jobID uint64) <-chan JobOutcome
	Concurrency() int
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

// DefaultQueue is the in-memory job queue used by the bot. Up to
// cfg.Workers jobs run concurrently; submissions beyond that wait in
// the pending list.
type DefaultQueue struct {
	cfg    Config
	runner SimExecutor
	logger logger.Logger
	cgroup *CgroupSampler

	mu       sync.Mutex
	nextID   uint64
	pending  []*jobEnvelope
	running  map[uint64]*jobEnvelope
	finished *historyRing

	completed atomic.Uint64
	failed    atomic.Uint64
	canceled  atomic.Uint64

	// liveProcs holds the most recent ProcStats sample per running job
	// id. Workers store/delete; Stats() snapshots all live values.
	liveProcs sync.Map // map[uint64]ProcStats

	wake        chan struct{}
	stop        chan struct{}
	workersDone sync.WaitGroup

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
		running:  make(map[uint64]*jobEnvelope),
		wake:     make(chan struct{}, 1),
	}
}

// Start launches cfg.Workers worker goroutines and the report-pruner.
func (q *DefaultQueue) Start(_ context.Context) error {
	if q.runner == nil {
		return errors.New("simc: queue requires a runner")
	}
	q.stop = make(chan struct{})
	q.pruneStop = make(chan struct{})
	q.pruneDone = make(chan struct{})
	for i := 0; i < q.cfg.Workers; i++ {
		q.workersDone.Add(1)
		go q.workerLoop()
	}
	go q.pruneLoop()
	return nil
}

// Stop signals all workers to exit and waits for them. Any in-flight
// jobs are canceled via their contexts.
func (q *DefaultQueue) Stop() {
	if q.stop != nil {
		close(q.stop)
		q.mu.Lock()
		for _, env := range q.running {
			if env.cancel != nil {
				env.cancel()
			}
		}
		q.mu.Unlock()
		q.workersDone.Wait()
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
	pos := len(q.pending) + len(q.running)
	q.signalWorker()
	return id, pos, nil
}

// Cancel removes a pending job or signals an in-flight one to abort.
func (q *DefaultQueue) Cancel(jobID uint64) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if env, ok := q.running[jobID]; ok {
		env.canceled.Store(true)
		if env.cancel != nil {
			env.cancel()
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
	if env, ok := q.running[jobID]; ok {
		env.subscribers = append(env.subscribers, ch)
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
	running := make([]JobInfo, 0, len(q.running))
	for _, env := range q.running {
		running = append(running, env.info)
	}
	queued := make([]JobInfo, 0, len(q.pending))
	for _, env := range q.pending {
		queued = append(queued, env.info)
	}
	q.mu.Unlock()

	var procs []ProcStats
	q.liveProcs.Range(func(_, v any) bool {
		if ps, ok := v.(ProcStats); ok {
			procs = append(procs, ps)
		}
		return true
	})

	return Snapshot{
		Running:        running,
		Queued:         queued,
		QueueDepth:     len(queued),
		QueueCap:       q.cfg.MaxQueueDepth,
		TotalCompleted: q.completed.Load(),
		TotalFailed:    q.failed.Load(),
		TotalCanceled:  q.canceled.Load(),
		Recent:         q.finished.Snapshot(),
		Processes:      procs,
		Container:      q.cgroup.Sample(),
		GeneratedAt:    time.Now(),
	}
}

// Concurrency returns the configured worker pool size.
func (q *DefaultQueue) Concurrency() int { return q.cfg.Workers }

func (q *DefaultQueue) signalWorker() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *DefaultQueue) workerLoop() {
	defer q.workersDone.Done()
	for {
		select {
		case <-q.stop:
			return
		case <-q.wake:
		}
		// Each wake might land any number of jobs at once; drain.
		for {
			env := q.popNext()
			if env == nil {
				break
			}
			q.execute(env)
			// Wake another worker in case there's still pending work
			// it could pick up.
			q.signalWorker()
		}
	}
}

// popNext claims the next pending job if there's capacity. Returns
// nil if the queue is empty or already at cfg.Workers in flight.
func (q *DefaultQueue) popNext() *jobEnvelope {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.running) >= q.cfg.Workers || len(q.pending) == 0 {
		return nil
	}
	env := q.pending[0]
	q.pending = q.pending[1:]
	q.running[env.info.ID] = env
	env.info.StartedAt = time.Now()
	return env
}

func (q *DefaultQueue) execute(env *jobEnvelope) {
	ctx, cancel := context.WithTimeout(context.Background(), q.cfg.JobTimeout)
	q.mu.Lock()
	env.cancel = cancel
	q.mu.Unlock()
	defer cancel()

	jobID := env.info.ID
	args := RunArgs{
		JobID:   jobID,
		Request: env.request,
		OnSample: func(s ProcStats) {
			q.liveProcs.Store(jobID, s)
		},
	}
	res, err := q.runner.Run(ctx, args)
	q.liveProcs.Delete(jobID)

	finished := FinishedJob{
		JobInfo:    env.info,
		FinishedAt: time.Now(),
	}
	finished.Duration = finished.FinishedAt.Sub(env.info.StartedAt)

	outcome := JobOutcome{JobID: jobID}
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
	delete(q.running, jobID)
	q.deliverLocked(env, outcome)
	q.mu.Unlock()
}

// deliverLocked sends the outcome to every subscriber and closes the channels.
// Caller must hold q.mu.
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
	if len(s.Running) > 0 {
		first := s.Running[0]
		running = fmt.Sprintf("#%d %s", first.ID, first.FightStyle)
		if extra := len(s.Running) - 1; extra > 0 {
			running += fmt.Sprintf(" +%d more", extra)
		}
	}
	return fmt.Sprintf("simc[%s, queued=%d/%d, ok=%d, fail=%d]",
		running, s.QueueDepth, s.QueueCap, s.TotalCompleted, s.TotalFailed)
}

package simc

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeRunner stands in for the real Runner; its single-sim execution is a
// configurable function so tests can simulate latency, errors, etc.
type fakeRunner struct {
	mu    sync.Mutex
	calls int
	exec  func(args RunArgs) (SimResult, error)
}

func (f *fakeRunner) Run(_ context.Context, args RunArgs) (SimResult, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.exec != nil {
		return f.exec(args)
	}
	return SimResult{JobID: args.JobID, DPS: 1000}, nil
}

func (f *fakeRunner) PruneOldReports() {}

// queueWithFakeRunner builds a Queue wired to the fake.
func queueWithFakeRunner(t *testing.T, fake *fakeRunner) (*DefaultQueue, func()) {
	t.Helper()
	cfg := Config{}
	cfg.Defaults()
	cfg.MaxQueueDepth = 4
	cfg.JobTimeout = 5 * time.Second
	cfg.WorkDir = t.TempDir()
	q := NewQueue(QueueParams{Config: cfg, Runner: fake})
	if err := q.Start(context.Background()); err != nil {
		t.Fatalf("start queue: %v", err)
	}
	return q, q.Stop
}

func TestQueue_SubmitRunsJob(t *testing.T) {
	fake := &fakeRunner{exec: func(args RunArgs) (SimResult, error) {
		return SimResult{JobID: args.JobID, DPS: 12345, PlayerName: "Askr"}, nil
	}}
	q, stop := queueWithFakeRunner(t, fake)
	defer stop()

	id, _, err := q.Submit(SimRequest{Profile: []byte("hunter=\"a\"\nlevel=80\n")}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	ch := q.Subscribe(id)
	select {
	case outcome := <-ch:
		if outcome.Status != JobStatusOK {
			t.Fatalf("status = %s err = %v", outcome.Status, outcome.Err)
		}
		if outcome.Result.DPS != 12345 {
			t.Fatalf("DPS = %v", outcome.Result.DPS)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for outcome")
	}

	if got := q.Stats().TotalCompleted; got != 1 {
		t.Errorf("completed = %d, want 1", got)
	}
}

func TestQueue_SubmitRespectsCap(t *testing.T) {
	fake := &fakeRunner{exec: func(args RunArgs) (SimResult, error) {
		time.Sleep(200 * time.Millisecond)
		return SimResult{JobID: args.JobID}, nil
	}}
	q, stop := queueWithFakeRunner(t, fake)
	defer stop()

	for i := 0; i < q.cfg.MaxQueueDepth+1; i++ {
		_, _, _ = q.Submit(SimRequest{Profile: []byte("level=80\n")}, "tester")
	}
	_, _, err := q.Submit(SimRequest{Profile: []byte("level=80\n")}, "tester")
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("err = %v, want ErrQueueFull", err)
	}
}

func TestQueue_CancelPending(t *testing.T) {
	fake := &fakeRunner{exec: func(args RunArgs) (SimResult, error) {
		time.Sleep(200 * time.Millisecond)
		return SimResult{JobID: args.JobID}, nil
	}}
	q, stop := queueWithFakeRunner(t, fake)
	defer stop()

	first, _, _ := q.Submit(SimRequest{Profile: []byte("level=80\n")}, "tester")
	second, _, _ := q.Submit(SimRequest{Profile: []byte("level=80\n")}, "tester")

	// Subscribe before canceling — matches how the discord layer uses the API.
	secondCh := q.Subscribe(second)
	if err := q.Cancel(second); err != nil {
		t.Fatal(err)
	}

	select {
	case outcome := <-secondCh:
		if outcome.Status != JobStatusCanceled {
			t.Fatalf("status = %s", outcome.Status)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not deliver outcome")
	}

	// First job should still complete.
	firstCh := q.Subscribe(first)
	select {
	case outcome := <-firstCh:
		if outcome.Status != JobStatusOK {
			t.Fatalf("first status = %s", outcome.Status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first job did not complete")
	}
}

func TestHistoryRing_NewestFirst(t *testing.T) {
	r := newHistoryRing(3)
	r.Push(FinishedJob{JobInfo: JobInfo{ID: 1}})
	r.Push(FinishedJob{JobInfo: JobInfo{ID: 2}})
	r.Push(FinishedJob{JobInfo: JobInfo{ID: 3}})
	r.Push(FinishedJob{JobInfo: JobInfo{ID: 4}})

	snap := r.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("len = %d, want 3", len(snap))
	}
	want := []uint64{4, 3, 2}
	for i, w := range want {
		if snap[i].ID != w {
			t.Errorf("snap[%d].ID = %d, want %d", i, snap[i].ID, w)
		}
	}
}

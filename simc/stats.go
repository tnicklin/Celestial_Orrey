package simc

import "sync"

// historyRing is a fixed-size FIFO ring buffer of FinishedJob records.
type historyRing struct {
	mu    sync.Mutex
	items []FinishedJob
	next  int
	cap   int
	count int
}

func newHistoryRing(capacity int) *historyRing {
	if capacity <= 0 {
		capacity = 20
	}
	return &historyRing{
		items: make([]FinishedJob, capacity),
		cap:   capacity,
	}
}

func (r *historyRing) Push(j FinishedJob) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[r.next] = j
	r.next = (r.next + 1) % r.cap
	if r.count < r.cap {
		r.count++
	}
}

// Snapshot returns the recent jobs newest-first.
func (r *historyRing) Snapshot() []FinishedJob {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]FinishedJob, 0, r.count)
	for i := 0; i < r.count; i++ {
		idx := (r.next - 1 - i + r.cap) % r.cap
		out = append(out, r.items[idx])
	}
	return out
}

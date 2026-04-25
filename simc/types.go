package simc

import (
	"strings"
	"time"
)

// FightStyle is a SimulationCraft fight style.
type FightStyle string

const (
	FightStylePatchwerk    FightStyle = "Patchwerk"
	FightStyleDungeonSlice FightStyle = "DungeonSlice"
)

// ParseFightStyle returns the FightStyle for a user-provided string.
// Empty string yields the default (Patchwerk).
func ParseFightStyle(s string) (FightStyle, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "patchwerk", "pw":
		return FightStylePatchwerk, true
	case "dungeon_slice", "dungeonslice", "dungeon-slice", "ds":
		return FightStyleDungeonSlice, true
	}
	return "", false
}

// JobStatus is the terminal state of a job.
type JobStatus string

const (
	JobStatusOK       JobStatus = "ok"
	JobStatusFailed   JobStatus = "failed"
	JobStatusCanceled JobStatus = "canceled"
	JobStatusTimeout  JobStatus = "timeout"
)

// SimRequest is a single simulation request submitted to the queue.
type SimRequest struct {
	Profile    []byte
	FightStyle FightStyle
	Iterations int
}

// SimResult is the parsed outcome of a successful simulation.
type SimResult struct {
	JobID      uint64
	DPS        float64
	DPSError   float64
	DPSMin     float64
	DPSMax     float64
	Iterations int
	Duration   time.Duration
	ReportPath string
	JSONPath   string
	PlayerName string
	SimVersion string
	FightStyle FightStyle
}

// JobInfo describes a queued or running job.
type JobInfo struct {
	ID          uint64
	Requester   string
	FightStyle  FightStyle
	Iterations  int
	SubmittedAt time.Time
	StartedAt   time.Time
}

// FinishedJob is a record kept in the recent-history ring buffer.
type FinishedJob struct {
	JobInfo
	FinishedAt time.Time
	Duration   time.Duration
	DPS        float64
	Status     JobStatus
	ErrMsg     string
	PlayerName string
}

// JobOutcome is delivered to subscribers when a job terminates.
type JobOutcome struct {
	JobID  uint64
	Status JobStatus
	Result SimResult
	Err    error
}

// ProcStats describes the running simc child process.
type ProcStats struct {
	PID         int           `json:"pid"`
	CPUPercent  float64       `json:"cpu_percent"`
	RSSBytes    uint64        `json:"rss_bytes"`
	UserTime    time.Duration `json:"user_time"`
	SystemTime  time.Duration `json:"system_time"`
	ThreadCount int           `json:"thread_count"`
	ElapsedTime time.Duration `json:"elapsed_time"`
}

// ContainerStats describes container-level resource usage and caps.
type ContainerStats struct {
	CPUQuotaCores float64 `json:"cpu_quota_cores"`
	CPUUsageCores float64 `json:"cpu_usage_cores"`
	MemLimitBytes uint64  `json:"mem_limit_bytes"`
	MemUsedBytes  uint64  `json:"mem_used_bytes"`
}

// Snapshot is a point-in-time view of the simc subsystem.
type Snapshot struct {
	Running        *JobInfo       `json:"running,omitempty"`
	Queued         []JobInfo      `json:"queued"`
	QueueDepth     int            `json:"queue_depth"`
	QueueCap       int            `json:"queue_cap"`
	TotalCompleted uint64         `json:"total_completed"`
	TotalFailed    uint64         `json:"total_failed"`
	TotalCanceled  uint64         `json:"total_canceled"`
	Recent         []FinishedJob  `json:"recent"`
	Process        *ProcStats     `json:"process,omitempty"`
	Container      ContainerStats `json:"container"`
	GeneratedAt    time.Time      `json:"generated_at"`
}

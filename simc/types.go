package simc

import (
	"strings"
	"time"
)

// FightStyle is a SimulationCraft fight style. We use an internal label
// (FightStylePatchwerk5T is not a real simc style — it maps to
// fight_style=Patchwerk + desired_targets=5 via FightStyleSpecFor).
type FightStyle string

const (
	FightStylePatchwerk    FightStyle = "Patchwerk"
	FightStylePatchwerk5T  FightStyle = "Patchwerk5T"
	FightStyleDungeonSlice FightStyle = "DungeonSlice"
)

// FightStyleOrder is the canonical iteration order for fight styles —
// used by the orchestrator's per-style loop and by the Discord embed
// so the rendered output stays stable regardless of map ordering.
var FightStyleOrder = []FightStyle{
	FightStylePatchwerk,
	FightStylePatchwerk5T,
	FightStyleDungeonSlice,
}

// FightStyleLabel is a short human-friendly label for embed headers.
func FightStyleLabel(fs FightStyle) string {
	switch fs {
	case FightStylePatchwerk:
		return "Patchwerk"
	case FightStylePatchwerk5T:
		return "Patchwerk 5T"
	case FightStyleDungeonSlice:
		return "Dungeon Slice"
	}
	return string(fs)
}

// FightStyleSpec resolves an internal FightStyle label to the simc-side
// configuration: the actual fight_style= value, the combat duration
// (max_time= in seconds), and the multi-target count
// (desired_targets= when > 1). Centralizing this here means the runner
// stays generic and per-style tuning lives in one table.
type FightStyleSpec struct {
	SimcStyle       string
	MaxTimeSec      int
	DesiredTargets  int
}

// FightStyleSpecFor returns the simc-side knobs for a given internal
// fight style. Defaults (zero MaxTime, single target) for unknown
// labels — those still produce a valid simc input.
func FightStyleSpecFor(fs FightStyle) FightStyleSpec {
	switch fs {
	case FightStylePatchwerk:
		return FightStyleSpec{SimcStyle: "Patchwerk", MaxTimeSec: 300}
	case FightStylePatchwerk5T:
		return FightStyleSpec{SimcStyle: "Patchwerk", MaxTimeSec: 300, DesiredTargets: 5}
	case FightStyleDungeonSlice:
		return FightStyleSpec{SimcStyle: "DungeonSlice", MaxTimeSec: 420}
	}
	return FightStyleSpec{SimcStyle: string(fs)}
}

// ParseFightStyle returns the FightStyle for a user-provided string.
// Empty string yields the default (Patchwerk).
func ParseFightStyle(s string) (FightStyle, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "patchwerk", "pw":
		return FightStylePatchwerk, true
	case "patchwerk5t", "patchwerk_5t", "pw5t", "5t", "5_target", "patchwerk-5t":
		return FightStylePatchwerk5T, true
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
	Profile     []byte
	FightStyle  FightStyle
	Iterations  int
	// TargetError is the convergence threshold passed to simc as
	// `target_error=<X>`. When > 0, simc stops iterating early once DPS
	// variance falls below this percentage. Iterations remains the
	// upper cap so a non-converging sim still terminates.
	TargetError float64
	// MaxTimeSec is the combat duration emitted as `max_time=<N>`.
	// 0 means use simc's default.
	MaxTimeSec int
	// DesiredTargets is emitted as `desired_targets=<N>` when > 1.
	// Used for multi-target Patchwerk variants.
	DesiredTargets int
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
	Running        []JobInfo      `json:"running"`
	Queued         []JobInfo      `json:"queued"`
	QueueDepth     int            `json:"queue_depth"`
	QueueCap       int            `json:"queue_cap"`
	TotalCompleted uint64         `json:"total_completed"`
	TotalFailed    uint64         `json:"total_failed"`
	TotalCanceled  uint64         `json:"total_canceled"`
	Recent         []FinishedJob  `json:"recent"`
	Processes      []ProcStats    `json:"processes"`
	Container      ContainerStats `json:"container"`
	GeneratedAt    time.Time      `json:"generated_at"`
}

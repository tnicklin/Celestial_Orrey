package simc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/tnicklin/celestial_orrey/logger"
)

// Runner executes a single SimulationCraft job. It is safe to call Run from
// one goroutine at a time per Runner; the queue takes care of serialization.
type Runner struct {
	cfg    Config
	logger logger.Logger
}

// RunnerParams holds dependencies for constructing a Runner.
type RunnerParams struct {
	Config Config
	Logger logger.Logger
}

// NewRunner constructs a Runner with the given config.
func NewRunner(p RunnerParams) *Runner {
	p.Config.Defaults()
	return &Runner{cfg: p.Config, logger: p.Logger}
}

// LiveProcStatsFunc is invoked periodically while a sim is running. Pass
// a nil function to disable sampling.
type LiveProcStatsFunc func(ProcStats)

// RunArgs bundles the per-invocation inputs to Run.
type RunArgs struct {
	JobID    uint64
	Request  SimRequest
	OnSample LiveProcStatsFunc
}

// Run executes a single sim. The returned SimResult is populated only when
// err is nil. The context controls per-job timeout and cancellation.
func (r *Runner) Run(ctx context.Context, args RunArgs) (SimResult, error) {
	if r.cfg.BinaryPath == "" {
		return SimResult{}, errors.New("simc: binary path not configured")
	}
	if len(args.Request.Profile) == 0 {
		return SimResult{}, errors.New("simc: empty profile")
	}

	jobDir, err := r.prepareJobDir(args)
	if err != nil {
		return SimResult{}, err
	}

	inputPath := filepath.Join(jobDir, "input.simc")
	jsonPath := filepath.Join(jobDir, "output.json")
	htmlPath := filepath.Join(jobDir, "report.html")
	logPath := filepath.Join(jobDir, "simc.log")

	if err := r.writeInput(inputPath, jsonPath, htmlPath, args.Request); err != nil {
		return SimResult{}, err
	}

	logFile, err := os.Create(logPath)
	if err != nil {
		return SimResult{}, fmt.Errorf("create log: %w", err)
	}
	defer logFile.Close()

	cmd := exec.CommandContext(ctx, r.cfg.BinaryPath, inputPath)
	cmd.Dir = jobDir
	var stderrBuf bytes.Buffer
	cmd.Stdout = logFile
	cmd.Stderr = &stderrBuf

	startedAt := time.Now()
	if err := cmd.Start(); err != nil {
		return SimResult{}, fmt.Errorf("start simc: %w", err)
	}

	pid := cmd.Process.Pid
	if r.logger != nil {
		r.logger.InfoW("simc started", "job", args.JobID, "pid", pid, "dir", jobDir)
	}

	stopSampling := r.startSampling(ctx, pid, args.OnSample)
	waitErr := cmd.Wait()
	stopSampling()

	duration := time.Since(startedAt)

	if waitErr != nil {
		stderrTail := tailString(stderrBuf.String(), 1024)
		if ctx.Err() != nil {
			return SimResult{}, fmt.Errorf("simc canceled after %s: %w", duration.Round(time.Second), ctx.Err())
		}
		return SimResult{}, fmt.Errorf("simc exited after %s: %w (stderr: %s)", duration.Round(time.Second), waitErr, stderrTail)
	}

	res, err := ParseJSONReport(jsonPath)
	if err != nil {
		return SimResult{}, fmt.Errorf("parse report: %w", err)
	}
	res.JobID = args.JobID
	res.Duration = duration
	res.ReportPath = htmlPath
	res.JSONPath = jsonPath
	res.FightStyle = args.Request.FightStyle

	if r.logger != nil {
		r.logger.InfoW("simc finished",
			"job", args.JobID,
			"dps", res.DPS,
			"iterations", res.Iterations,
			"duration", duration.Round(time.Millisecond),
		)
	}
	return res, nil
}

// PruneOldReports deletes job directories older than cfg.ReportRetention.
// Best-effort; errors are logged but not returned.
func (r *Runner) PruneOldReports() {
	if r.cfg.WorkDir == "" || r.cfg.ReportRetention <= 0 {
		return
	}
	entries, err := os.ReadDir(r.cfg.WorkDir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-r.cfg.ReportRetention)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			path := filepath.Join(r.cfg.WorkDir, e.Name())
			if err := os.RemoveAll(path); err != nil && r.logger != nil {
				r.logger.WarnW("prune simc report", "path", path, "error", err)
			}
		}
	}
}

func (r *Runner) prepareJobDir(args RunArgs) (string, error) {
	if err := os.MkdirAll(r.cfg.WorkDir, 0o755); err != nil {
		return "", fmt.Errorf("create work dir: %w", err)
	}
	name := fmt.Sprintf("job-%d-%d", args.JobID, time.Now().Unix())
	dir := filepath.Join(r.cfg.WorkDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create job dir: %w", err)
	}
	return dir, nil
}

func (r *Runner) writeInput(inputPath, jsonPath, htmlPath string, req SimRequest) error {
	iterations := req.Iterations
	if iterations <= 0 {
		iterations = r.cfg.DefaultIterations
	}
	if iterations > r.cfg.MaxIterations {
		iterations = r.cfg.MaxIterations
	}
	style := req.FightStyle
	if style == "" {
		style = FightStylePatchwerk
	}

	var buf bytes.Buffer
	buf.Write(req.Profile)
	if !bytes.HasSuffix(req.Profile, []byte("\n")) {
		buf.WriteByte('\n')
	}
	// Override block goes last so user-supplied values can't shadow our caps.
	fmt.Fprintf(&buf, "\n# --- celestial-orrey overrides ---\n")
	fmt.Fprintf(&buf, "threads=%d\n", r.cfg.Threads)
	fmt.Fprintf(&buf, "iterations=%d\n", iterations)
	fmt.Fprintf(&buf, "fight_style=%s\n", style)
	fmt.Fprintf(&buf, "json2=%s\n", jsonPath)
	fmt.Fprintf(&buf, "html=%s\n", htmlPath)

	if err := os.WriteFile(inputPath, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write input: %w", err)
	}
	return nil
}

// startSampling spawns a goroutine that polls /proc/<pid>/* every
// cfg.SampleInterval and forwards the result to onSample. The returned
// stop function blocks until the sampler goroutine exits.
func (r *Runner) startSampling(ctx context.Context, pid int, onSample LiveProcStatsFunc) func() {
	if onSample == nil {
		return func() {}
	}
	stopped := atomic.Bool{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		sampler := NewProcSampler(pid)
		ticker := time.NewTicker(r.cfg.SampleInterval)
		defer ticker.Stop()
		for {
			if stopped.Load() {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if stopped.Load() {
					return
				}
				stats, err := sampler.Sample()
				if err != nil {
					return
				}
				onSample(stats)
			}
		}
	}()
	return func() {
		stopped.Store(true)
		<-done
	}
}

func tailString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}

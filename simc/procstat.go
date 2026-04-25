package simc

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// clkTck is the kernel CLK_TCK value. 100 is universal on Linux.
const clkTck = 100

// ProcSampler tracks a running simc child and samples its resource usage.
type ProcSampler struct {
	pid       int
	startedAt time.Time
	lastTotal uint64
	lastTS    time.Time
	lastCPU   float64
}

// NewProcSampler returns a sampler for the given pid. Sample() should be
// called periodically; the first call yields zero CPUPercent (no baseline).
func NewProcSampler(pid int) *ProcSampler {
	return &ProcSampler{pid: pid, startedAt: time.Now()}
}

// Sample reads /proc/<pid>/{stat,status} and returns a fresh snapshot.
// Returns an error if the process has exited.
func (s *ProcSampler) Sample() (ProcStats, error) {
	utime, stime, threads, err := readProcStat(s.pid)
	if err != nil {
		return ProcStats{}, err
	}
	rss, err := readProcRSS(s.pid)
	if err != nil {
		return ProcStats{}, err
	}

	total := utime + stime
	now := time.Now()
	cpuPct := s.lastCPU
	if !s.lastTS.IsZero() {
		dt := now.Sub(s.lastTS).Seconds()
		if dt > 0 {
			deltaTicks := float64(total - s.lastTotal)
			cpuPct = (deltaTicks / clkTck) / dt * 100
		}
	}
	s.lastTotal = total
	s.lastTS = now
	s.lastCPU = cpuPct

	return ProcStats{
		PID:         s.pid,
		CPUPercent:  cpuPct,
		RSSBytes:    rss,
		UserTime:    time.Duration(utime) * time.Second / clkTck,
		SystemTime:  time.Duration(stime) * time.Second / clkTck,
		ThreadCount: threads,
		ElapsedTime: now.Sub(s.startedAt),
	}, nil
}

// readProcStat parses fields 14, 15, 20 (utime, stime, num_threads) from
// /proc/<pid>/stat. The comm field (field 2) may contain spaces and is
// wrapped in parentheses, so we split around it.
func readProcStat(pid int) (utime, stime uint64, threads int, err error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, 0, 0, err
	}
	s := string(data)
	// Find the closing ) of the comm field.
	rparen := strings.LastIndexByte(s, ')')
	if rparen < 0 {
		return 0, 0, 0, fmt.Errorf("malformed /proc/%d/stat", pid)
	}
	rest := strings.Fields(s[rparen+1:])
	// Indices are offset: field 3 (state) is rest[0]. utime=field14 -> rest[11];
	// stime=field15 -> rest[12]; num_threads=field20 -> rest[17].
	if len(rest) < 18 {
		return 0, 0, 0, fmt.Errorf("short /proc/%d/stat", pid)
	}
	if utime, err = strconv.ParseUint(rest[11], 10, 64); err != nil {
		return 0, 0, 0, fmt.Errorf("parse utime: %w", err)
	}
	if stime, err = strconv.ParseUint(rest[12], 10, 64); err != nil {
		return 0, 0, 0, fmt.Errorf("parse stime: %w", err)
	}
	t, err := strconv.Atoi(rest[17])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("parse threads: %w", err)
	}
	return utime, stime, t, nil
}

// readProcRSS reads VmRSS from /proc/<pid>/status (in KiB) and returns bytes.
func readProcRSS(pid int) (uint64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, nil
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse VmRSS: %w", err)
		}
		return kb * 1024, nil
	}
	return 0, nil
}

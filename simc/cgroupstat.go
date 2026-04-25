package simc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// CgroupSampler reads container resource caps and usage from cgroups v2.
// All methods return zero values when the relevant files are missing
// (e.g. running outside a container), so the bot still works locally.
type CgroupSampler struct {
	root      string
	lastUsage uint64
	lastTS    time.Time
	lastCores float64

	cachedQuota    float64
	cachedMemLimit uint64
	cachedAt       time.Time
}

// NewCgroupSampler returns a sampler rooted at the given cgroup path
// (typically /sys/fs/cgroup).
func NewCgroupSampler(root string) *CgroupSampler {
	return &CgroupSampler{root: root}
}

// Sample returns the current container CPU and memory usage versus caps.
// Errors from individual reads are swallowed and returned as zero fields.
func (c *CgroupSampler) Sample() ContainerStats {
	cs := ContainerStats{}

	if time.Since(c.cachedAt) > 30*time.Second {
		if q, err := readCPUQuota(c.root); err == nil {
			c.cachedQuota = q
		}
		if m, err := readMemLimit(c.root); err == nil {
			c.cachedMemLimit = m
		}
		c.cachedAt = time.Now()
	}
	cs.CPUQuotaCores = c.cachedQuota
	cs.MemLimitBytes = c.cachedMemLimit

	if used, err := readMemCurrent(c.root); err == nil {
		cs.MemUsedBytes = used
	}

	if usage, err := readCPUUsageUsec(c.root); err == nil {
		now := time.Now()
		cores := c.lastCores
		if !c.lastTS.IsZero() {
			dt := now.Sub(c.lastTS).Seconds()
			if dt > 0 {
				deltaSec := float64(usage-c.lastUsage) / 1e6
				cores = deltaSec / dt
			}
		}
		c.lastUsage = usage
		c.lastTS = now
		c.lastCores = cores
		cs.CPUUsageCores = cores
	}

	return cs
}

// readCPUQuota parses cpu.max ("QUOTA PERIOD"). "max QUOTA" means unlimited.
func readCPUQuota(root string) (float64, error) {
	data, err := os.ReadFile(filepath.Join(root, "cpu.max"))
	if err != nil {
		return 0, err
	}
	parts := strings.Fields(strings.TrimSpace(string(data)))
	if len(parts) != 2 {
		return 0, fmt.Errorf("malformed cpu.max: %q", string(data))
	}
	if parts[0] == "max" {
		return 0, nil
	}
	quota, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, err
	}
	period, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, err
	}
	if period <= 0 {
		return 0, errors.New("zero cpu period")
	}
	return float64(quota) / float64(period), nil
}

func readCPUUsageUsec(root string) (uint64, error) {
	data, err := os.ReadFile(filepath.Join(root, "cpu.stat"))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "usage_usec") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, errors.New("malformed cpu.stat usage_usec")
		}
		return strconv.ParseUint(fields[1], 10, 64)
	}
	return 0, errors.New("usage_usec not found")
}

func readMemLimit(root string) (uint64, error) {
	data, err := os.ReadFile(filepath.Join(root, "memory.max"))
	if err != nil {
		return 0, err
	}
	v := strings.TrimSpace(string(data))
	if v == "max" {
		return 0, nil
	}
	return strconv.ParseUint(v, 10, 64)
}

func readMemCurrent(root string) (uint64, error) {
	data, err := os.ReadFile(filepath.Join(root, "memory.current"))
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
}

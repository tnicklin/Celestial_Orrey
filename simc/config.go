package simc

import "time"

// Config holds SimulationCraft runner, queue, and stats configuration.
type Config struct {
	BinaryPath        string        `yaml:"binary_path"`
	WorkDir           string        `yaml:"work_dir"`
	Threads           int           `yaml:"threads"`
	Workers           int           `yaml:"workers"`
	DefaultIterations int           `yaml:"default_iterations"`
	MaxIterations     int           `yaml:"max_iterations"`
	JobTimeout        time.Duration `yaml:"job_timeout"`
	MaxQueueDepth     int           `yaml:"max_queue_depth"`
	ReportRetention   time.Duration `yaml:"report_retention"`
	HistorySize       int           `yaml:"history_size"`
	StatsHTTPAddr     string        `yaml:"stats_http_addr"`
	MaxProfileBytes   int64         `yaml:"max_profile_bytes"`
	SampleInterval    time.Duration `yaml:"sample_interval"`
	CgroupRoot        string        `yaml:"cgroup_root"`
}

// Defaults applies default values to the config.
func (c *Config) Defaults() {
	if c.BinaryPath == "" {
		c.BinaryPath = "/usr/local/bin/simc"
	}
	if c.WorkDir == "" {
		c.WorkDir = "/app/data/simc"
	}
	if c.Threads <= 0 {
		c.Threads = 4
	}
	if c.Workers <= 0 {
		c.Workers = 1
	}
	if c.DefaultIterations <= 0 {
		c.DefaultIterations = 10000
	}
	if c.MaxIterations <= 0 {
		c.MaxIterations = 50000
	}
	if c.JobTimeout <= 0 {
		c.JobTimeout = 30 * time.Minute
	}
	if c.MaxQueueDepth <= 0 {
		c.MaxQueueDepth = 4
	}
	if c.ReportRetention <= 0 {
		c.ReportRetention = 24 * time.Hour
	}
	if c.HistorySize <= 0 {
		c.HistorySize = 20
	}
	if c.StatsHTTPAddr == "" {
		c.StatsHTTPAddr = "127.0.0.1:9090"
	}
	if c.MaxProfileBytes <= 0 {
		c.MaxProfileBytes = 1 << 20
	}
	if c.SampleInterval <= 0 {
		c.SampleInterval = 2 * time.Second
	}
	if c.CgroupRoot == "" {
		c.CgroupRoot = "/sys/fs/cgroup"
	}
}

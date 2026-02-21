package clock

import (
	"context"
	"sync"
	"time"

	"github.com/beevik/ntp"
)

// Logger is a minimal logging interface satisfied by logger.Logger.
type Logger interface {
	InfoW(msg string, keysAndValues ...any)
	WarnW(msg string, keysAndValues ...any)
}

// NTPClock provides drift-corrected wall-clock time by periodically
// syncing with an NTP server.
type NTPClock struct {
	server  string
	interval time.Duration
	timeout  time.Duration
	logger   Logger

	mu     sync.RWMutex
	offset time.Duration

	cancel context.CancelFunc
	done   chan struct{}
}

// Option configures an NTPClock.
type Option func(*NTPClock)

// WithServer sets the NTP server address.
func WithServer(server string) Option {
	return func(c *NTPClock) { c.server = server }
}

// WithInterval sets the re-sync interval.
func WithInterval(d time.Duration) Option {
	return func(c *NTPClock) { c.interval = d }
}

// WithTimeout sets the NTP query timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *NTPClock) { c.timeout = d }
}

// WithLogger sets the logger.
func WithLogger(l Logger) Option {
	return func(c *NTPClock) { c.logger = l }
}

const (
	defaultServer   = "pool.ntp.org"
	defaultInterval = 30 * time.Minute
	defaultTimeout  = 5 * time.Second
)

// NewNTP creates an NTPClock with the given options.
func NewNTP(opts ...Option) *NTPClock {
	c := &NTPClock{
		server:   defaultServer,
		interval: defaultInterval,
		timeout:  defaultTimeout,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Now returns the current time adjusted by the NTP offset.
func (c *NTPClock) Now() time.Time {
	c.mu.RLock()
	off := c.offset
	c.mu.RUnlock()
	return time.Now().Add(off)
}

// Offset returns the current NTP offset.
func (c *NTPClock) Offset() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.offset
}

// Start performs an initial NTP sync and starts a background goroutine
// that re-syncs on the configured interval.
func (c *NTPClock) Start(ctx context.Context) error {
	c.sync()

	ctx, c.cancel = context.WithCancel(ctx)
	c.done = make(chan struct{})
	go c.run(ctx)
	return nil
}

// Stop shuts down the background sync goroutine.
func (c *NTPClock) Stop() {
	if c.cancel != nil {
		c.cancel()
		<-c.done
	}
}

func (c *NTPClock) run(ctx context.Context) {
	defer close(c.done)

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.sync()
		}
	}
}

func (c *NTPClock) sync() {
	resp, err := ntp.QueryWithOptions(c.server, ntp.QueryOptions{
		Timeout: c.timeout,
	})
	if err != nil {
		if c.logger != nil {
			c.logger.WarnW("ntp sync failed, keeping last offset", "error", err)
		}
		return
	}

	c.mu.Lock()
	c.offset = resp.ClockOffset
	c.mu.Unlock()

	if c.logger != nil {
		c.logger.InfoW("ntp sync", "offset", resp.ClockOffset)
	}
}

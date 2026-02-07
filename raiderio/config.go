package raiderio

import (
	"net/http"
	"time"
)

// Config holds RaiderIO client and poller configuration.
type Config struct {
	BaseURL       string        `yaml:"base_url"`
	UserAgent     string        `yaml:"user_agent"`
	PollInterval  time.Duration `yaml:"poll_interval"`
	MaxConcurrent int           `yaml:"max_concurrent"`
	HTTPClient    *http.Client  `yaml:"-"`
}

// Defaults applies default values to the config.
func (c *Config) Defaults() {
	if c.BaseURL == "" {
		c.BaseURL = "https://raider.io"
	}
	if c.UserAgent == "" {
		c.UserAgent = "celestial-orrey/1.0"
	}
	if c.PollInterval <= 0 {
		c.PollInterval = 5 * time.Minute
	}
	if c.MaxConcurrent <= 0 {
		c.MaxConcurrent = 4
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
}

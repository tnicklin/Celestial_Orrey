package elvui

import "time"

// Config holds ElvUI poller configuration.
type Config struct {
	PollInterval time.Duration `yaml:"poll_interval"`
	APIURL       string        `yaml:"api_url"`
}

// Defaults applies default values to the config.
func (c *Config) Defaults() {
	if c.PollInterval <= 0 {
		c.PollInterval = 5 * time.Minute
	}
	if c.APIURL == "" {
		c.APIURL = "https://www.tukui.org/api.php?ui=elvui"
	}
}

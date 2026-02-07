package config

import (
	"os"

	"github.com/tnicklin/celestial_orrey/logger"
	"github.com/tnicklin/celestial_orrey/models"
	"github.com/tnicklin/celestial_orrey/raiderio"
	"github.com/tnicklin/celestial_orrey/store"
	"github.com/tnicklin/celestial_orrey/warcraftlogs"
	"go.uber.org/config"
)

// DiscordConfig holds Discord-specific configuration.
type DiscordConfig struct {
	Token          string `yaml:"token"`
	GuildID        string `yaml:"guild_id"`
	CommandChannel string `yaml:"command_channel"`
	ReportChannel  string `yaml:"report_channel"`
}

// AppConfig holds all application configuration.
type AppConfig struct {
	Logger       logger.Config       `yaml:"logger"`
	Discord      DiscordConfig       `yaml:"discord"`
	RaiderIO     raiderio.Config     `yaml:"raiderio"`
	WarcraftLogs warcraftlogs.Config `yaml:"warcraftlogs"`
	Store        store.Config        `yaml:"store"`
	Characters   []models.Character  `yaml:"characters"`
}

// Load reads configuration from the specified YAML files.
// Files are merged in order, with later files overriding earlier ones.
// Missing files are silently ignored.
func Load(files ...string) (*AppConfig, error) {
	opts := make([]config.YAMLOption, 0, len(files))
	for _, f := range files {
		if _, err := os.Stat(f); err == nil {
			opts = append(opts, config.File(f))
		}
	}

	if len(opts) == 0 {
		return nil, os.ErrNotExist
	}

	provider, err := config.NewYAML(opts...)
	if err != nil {
		return nil, err
	}

	var cfg AppConfig
	if err := provider.Get(config.Root).Populate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// LoadWithDefaults loads configuration with sensible defaults.
func LoadWithDefaults(files ...string) (*AppConfig, error) {
	cfg, err := Load(files...)
	if err != nil {
		return nil, err
	}

	// Apply defaults
	if cfg.Logger.Level == "" {
		cfg.Logger.Level = "info"
	}
	if len(cfg.Logger.OutputPaths) == 0 {
		cfg.Logger.OutputPaths = []string{"stdout"}
	}
	if cfg.RaiderIO.BaseURL == "" {
		cfg.RaiderIO.BaseURL = "https://raider.io"
	}
	if cfg.RaiderIO.UserAgent == "" {
		cfg.RaiderIO.UserAgent = "celestial-orrey/1.0"
	}
	if cfg.RaiderIO.PollInterval == 0 {
		cfg.RaiderIO.PollInterval = 5 * 60 * 1e9 // 5 minutes in nanoseconds
	}
	if cfg.RaiderIO.MaxConcurrent == 0 {
		cfg.RaiderIO.MaxConcurrent = 4
	}
	if cfg.Store.Path == "" {
		cfg.Store.Path = "data/celestial_orrey.db"
	}

	// WarcraftLogs defaults
	if cfg.WarcraftLogs.GraphQLURL == "" {
		cfg.WarcraftLogs.GraphQLURL = "https://www.warcraftlogs.com/api/v2/client"
	}
	if cfg.WarcraftLogs.TokenURL == "" {
		cfg.WarcraftLogs.TokenURL = "https://www.warcraftlogs.com/oauth/token"
	}
	if cfg.WarcraftLogs.UserAgent == "" {
		cfg.WarcraftLogs.UserAgent = "celestial-orrey/1.0"
	}
	if cfg.WarcraftLogs.Limit == 0 {
		cfg.WarcraftLogs.Limit = 50
	}

	return cfg, nil
}

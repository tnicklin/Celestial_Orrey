package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tnicklin/celestial_orrey/clock"
	"github.com/tnicklin/celestial_orrey/discord"
	"github.com/tnicklin/celestial_orrey/elvui"
	"github.com/tnicklin/celestial_orrey/logger"
	"github.com/tnicklin/celestial_orrey/raiderio"
	rioClient "github.com/tnicklin/celestial_orrey/raiderio/client"
	"github.com/tnicklin/celestial_orrey/simc"
	"github.com/tnicklin/celestial_orrey/store"
	"github.com/tnicklin/celestial_orrey/warcraftlogs"
	"go.uber.org/config"
	"go.uber.org/fx"
)

const _configDir = "config"

func main() { fx.New(_app).Run() }

var _app = fx.Options(
	fx.Provide(build),
	fx.Invoke(run),
)

type appConfig struct {
	Logger       logger.Config       `yaml:"logger"`
	Discord      discord.Config      `yaml:"discord"`
	RaiderIO     raiderio.Config     `yaml:"raiderio"`
	WarcraftLogs warcraftlogs.Config `yaml:"warcraftlogs"`
	Store        store.Config        `yaml:"store"`
	ElvUI        elvui.Config        `yaml:"elvui"`
	Simc         simc.Config         `yaml:"simc"`
	SimcOrch      simc.OrchestratorConfig      `yaml:"simc_run"`
}

type result struct {
	fx.Out

	Config        appConfig
	Logger        logger.Logger
	Store         *store.SQLiteStore
	NTPClock      *clock.NTPClock
	RaiderIO      rioClient.Client
	RIOPoller     raiderio.Poller
	WarcraftLogs  warcraftlogs.WCL
	WCLPoller     warcraftlogs.Poller
	ElvUIPoller   elvui.Poller
	SimcRunner    *simc.Runner
	SimcQueue     *simc.DefaultQueue
	SimcOrch       *simc.DefaultOrchestrator
	SimcStats     *simc.StatsServer
	DiscordClient discord.Discord
}

func build() (result, error) {
	cfg, err := loadConfig()
	if err != nil {
		return result{}, fmt.Errorf("load config: %w", err)
	}

	appLogger, err := logger.New(cfg.Logger)
	if err != nil {
		return result{}, fmt.Errorf("initialize logger: %w", err)
	}

	ntpClock := clock.NewNTP(clock.WithLogger(appLogger))

	st := store.NewSQLiteStore(store.Params{
		Path:      cfg.Store.Path,
		BackupDir: cfg.Store.BackupDir,
		Logger:    appLogger,
	})

	wclClient := warcraftlogs.New(warcraftlogs.Params{
		ClientID:     cfg.WarcraftLogs.ClientID,
		ClientSecret: cfg.WarcraftLogs.ClientSecret,
		HTTPClient:   &http.Client{Timeout: 30 * time.Second},
	})

	wclLinker := warcraftlogs.NewLinker(warcraftlogs.LinkerParams{
		Store:  st,
		Client: wclClient,
	})

	rio := rioClient.New(rioClient.Params{
		BaseURL:    cfg.RaiderIO.BaseURL,
		UserAgent:  cfg.RaiderIO.UserAgent,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	})

	rioPoller := raiderio.New(raiderio.Params{
		Config:    cfg.RaiderIO,
		Client:    rio,
		Store:     st,
		WCLLinker: wclLinker,
		Clock:     ntpClock,
	})

	wclPoller := warcraftlogs.NewPoller(warcraftlogs.PollerParams{
		Store:    st,
		Client:   wclClient,
		Clock:    ntpClock,
		Interval: 5 * time.Minute,
	})

	simcRunner := simc.NewRunner(simc.RunnerParams{
		Config: cfg.Simc,
		Logger: appLogger,
	})
	simcQueue := simc.NewQueue(simc.QueueParams{
		Config: cfg.Simc,
		Runner: simcRunner,
		Logger: appLogger,
	})
	simcOrch := simc.NewOrchestrator(simc.OrchestratorParams{
		Config: cfg.SimcOrch,
		Queue:  simcQueue,
		Logger: appLogger,
	})
	simcStats := simc.NewStatsServer(simc.StatsServerParams{
		Addr:   cfg.Simc.StatsHTTPAddr,
		Queue:  simcQueue,
		Logger: appLogger,
	})
	simcNames := simc.NewWowheadResolver(st, appLogger)

	discordClient, err := discord.New(discord.Params{
		Config:       cfg.Discord,
		Store:        st,
		RaiderIO:     rio,
		WarcraftLogs: wclClient,
		SimcQueue:    simcQueue,
		SimcOrch:      simcOrch,
		SimcConfig:   cfg.Simc,
		SimcNames:    simcNames,
		Logger:       appLogger,
		Clock:        ntpClock,
	})
	if err != nil {
		return result{}, fmt.Errorf("discord client: %w", err)
	}

	elvuiPoller := elvui.New(elvui.Params{
		Config:     cfg.ElvUI,
		Store:      st,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		OnNewVersion: func(v elvui.VersionInfo) {
			msg := fmt.Sprintf("**ElvUI %s** is now available!\n[Download](%s) | [Changelog](%s)",
				v.Version, v.URL, v.ChangelogURL)
			if err := discordClient.WriteMessage(cfg.Discord.ListenChannel, msg); err != nil {
				appLogger.ErrorW("elvui notification", "error", err)
			}
		},
	})

	return result{
		Config:        cfg,
		Logger:        appLogger,
		DiscordClient: discordClient,
		Store:         st,
		NTPClock:      ntpClock,
		RaiderIO:      rio,
		RIOPoller:     rioPoller,
		WarcraftLogs:  wclClient,
		WCLPoller:     wclPoller,
		ElvUIPoller:   elvuiPoller,
		SimcRunner:    simcRunner,
		SimcQueue:     simcQueue,
		SimcOrch:       simcOrch,
		SimcStats:     simcStats,
	}, nil
}

type runParams struct {
	fx.In

	Lifecycle     fx.Lifecycle
	Config        appConfig
	Store         *store.SQLiteStore
	NTPClock      *clock.NTPClock
	RaiderIO      rioClient.Client
	RIOPoller     raiderio.Poller
	WarcraftLogs  warcraftlogs.WCL
	WCLPoller     warcraftlogs.Poller
	ElvUIPoller   elvui.Poller
	SimcQueue     *simc.DefaultQueue
	SimcOrch       *simc.DefaultOrchestrator
	SimcStats     *simc.StatsServer
	DiscordClient discord.Discord
	Logger        logger.Logger
}

func run(p runParams) error {
	p.Lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			return p.NTPClock.Start(ctx)
		},
		OnStop: func(_ context.Context) error {
			p.NTPClock.Stop()
			return nil
		},
	})

	p.Lifecycle.Append(fx.Hook{
		OnStart: func(_ context.Context) error {
			ctx := context.Background()
			if err := p.Store.Open(ctx); err != nil {
				return fmt.Errorf("open keydb store: %w", err)
			}
			if err := p.Store.RestoreFromDisk(ctx, p.Config.Store.Path); err != nil {
				p.Logger.WarnW("restore from disk", "error", err)
			}
			return nil
		},
		OnStop: func(ctx context.Context) error {
			return p.Store.Shutdown(ctx)
		},
	})

	p.Lifecycle.Append(fx.Hook{
		OnStart: func(_ context.Context) error {
			if err := p.DiscordClient.Start(context.Background()); err != nil {
				return fmt.Errorf("start discord client: %w", err)
			}

			return nil
		},
		OnStop: func(_ context.Context) error {
			p.DiscordClient.Stop()
			return nil
		},
	})

	p.Lifecycle.Append(fx.Hook{
		OnStart: func(_ context.Context) error {
			if err := p.WCLPoller.Start(context.Background()); err != nil {
				return fmt.Errorf("start wcl poller: %w", err)
			}

			return nil
		},
		OnStop: func(_ context.Context) error {
			p.WCLPoller.Stop()
			return nil
		},
	})

	p.Lifecycle.Append(fx.Hook{
		OnStart: func(_ context.Context) error {
			if err := p.RIOPoller.Start(context.Background()); err != nil {
				return fmt.Errorf("start rio poller: %w", err)
			}

			return nil
		},
		OnStop: func(_ context.Context) error {
			p.RIOPoller.Stop()
			return nil
		},
	})

	p.Lifecycle.Append(fx.Hook{
		OnStart: func(_ context.Context) error {
			if err := p.ElvUIPoller.Start(context.Background()); err != nil {
				return fmt.Errorf("start elvui poller: %w", err)
			}

			return nil
		},
		OnStop: func(_ context.Context) error {
			p.ElvUIPoller.Stop()
			return nil
		},
	})

	p.Lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if err := p.SimcQueue.Start(ctx); err != nil {
				return fmt.Errorf("start simc queue: %w", err)
			}
			return nil
		},
		OnStop: func(_ context.Context) error {
			p.SimcQueue.Stop()
			return nil
		},
	})

	p.Lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if err := p.SimcOrch.Start(ctx); err != nil {
				return fmt.Errorf("start simc orchestrator: %w", err)
			}
			return nil
		},
		OnStop: func(_ context.Context) error {
			p.SimcOrch.Stop()
			return nil
		},
	})

	p.Lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if err := p.SimcStats.Start(ctx); err != nil {
				return fmt.Errorf("start simc stats server: %w", err)
			}
			return nil
		},
		OnStop: func(_ context.Context) error {
			p.SimcStats.Stop()
			return nil
		},
	})

	return nil
}

func loadConfig() (appConfig, error) {
	files, err := os.ReadDir(_configDir)
	if err != nil {
		return appConfig{}, fmt.Errorf("read config dir: %w", err)
	}

	var opts []config.YAMLOption
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		if !strings.HasSuffix(f.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(_configDir, f.Name())
		opts = append(opts, config.File(path))
	}
	if len(opts) == 0 {
		return appConfig{}, fmt.Errorf("no yaml files found in %q", _configDir)
	}

	provider, err := config.NewYAML(opts...)
	if err != nil {
		return appConfig{}, err
	}

	var cfg appConfig
	if err = provider.Get(config.Root).Populate(&cfg); err != nil {
		return appConfig{}, err
	}

	return cfg, nil
}

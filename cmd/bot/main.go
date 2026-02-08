package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tnicklin/celestial_orrey/discord"
	"github.com/tnicklin/celestial_orrey/logger"
	"github.com/tnicklin/celestial_orrey/models"
	"github.com/tnicklin/celestial_orrey/raiderio"
	rioClient "github.com/tnicklin/celestial_orrey/raiderio/client"
	"github.com/tnicklin/celestial_orrey/store"
	"github.com/tnicklin/celestial_orrey/warcraftlogs"
	"go.uber.org/config"
)

func main() {
	result, err := build()
	if err != nil {
		log.Fatal(err)
	}

	if err = run(result); err != nil {
		log.Fatal(err)
	}
}

type appConfig struct {
	Logger       logger.Config       `yaml:"logger"`
	Discord      discord.Config      `yaml:"discord"`
	RaiderIO     raiderio.Config     `yaml:"raiderio"`
	WarcraftLogs warcraftlogs.Config `yaml:"warcraftlogs"`
	Store        store.Config        `yaml:"store"`
	Characters   []models.Character  `yaml:"characters"`
}

type result struct {
	Config        appConfig
	Logger        logger.Logger
	Store         *store.SQLiteStore
	RaiderIO      rioClient.Client
	RIOPoller     raiderio.Poller
	WarcraftLogs  warcraftlogs.WCL
	WCLPoller     warcraftlogs.Poller
	DiscordClient discord.Discord
}

func build() (result, error) {
	cfg, err := loadConfig("config")
	if err != nil {
		return result{}, fmt.Errorf("load config: %w", err)
	}

	appLogger, err := logger.New(cfg.Logger)
	if err != nil {
		return result{}, fmt.Errorf("initialize logger: %w", err)
	}

	st := store.NewSQLiteStore(store.Params{
		Path:   cfg.Store.Path,
		Logger: appLogger,
	})

	rio := rioClient.New(rioClient.Params{
		BaseURL:    cfg.RaiderIO.BaseURL,
		UserAgent:  cfg.RaiderIO.UserAgent,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Logger:     appLogger,
	})
	rioPoller := raiderio.New(raiderio.Params{
		Config:     cfg.RaiderIO,
		Client:     rio,
		Store:      st,
		Characters: cfg.Characters,
		Logger:     appLogger,
	})

	wclClient := warcraftlogs.New(warcraftlogs.Params{
		ClientID:     cfg.WarcraftLogs.ClientID,
		ClientSecret: cfg.WarcraftLogs.ClientSecret,
		HTTPClient:   &http.Client{Timeout: 30 * time.Second},
	})

	wclPoller := warcraftlogs.NewPoller(warcraftlogs.PollerParams{
		Store:       st,
		Client:      wclClient,
		Logger:      appLogger,
		Interval:    5 * time.Minute,
		MatchWindow: 24 * time.Hour,
	})

	discordClient, err := discord.New(discord.Params{
		Config:       cfg.Discord,
		Store:        st,
		RaiderIO:     rio,
		WarcraftLogs: wclClient,
		Logger:       appLogger,
	})
	if err != nil {
		return result{}, fmt.Errorf("discord client: %w", err)
	}

	return result{
		Config:        cfg,
		Logger:        appLogger,
		DiscordClient: discordClient,
		Store:         st,
		RaiderIO:      rio,
		RIOPoller:     rioPoller,
		WarcraftLogs:  wclClient,
		WCLPoller:     wclPoller,
	}, nil
}

// run starts all components and runs the application until shutdown.
func run(r result) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer r.Logger.Sync()

	if err := r.Store.Open(ctx); err != nil {
		return fmt.Errorf("open keydb store: %w", err)
	}

	if err := r.Store.RestoreFromDisk(ctx, r.Config.Store.Path); err != nil {
		r.Logger.WarnW("restore from disk", "error", err)
	}

	if err := r.DiscordClient.Start(ctx); err != nil {
		return fmt.Errorf("start discord client: %w", err)
	}

	if err := r.RIOPoller.Start(ctx); err != nil {
		return fmt.Errorf("start rio poller: %w", err)
	}

	if err := r.WCLPoller.Start(ctx); err != nil {
		return fmt.Errorf("start wcl poller: %w", err)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	r.WCLPoller.Stop()
	r.DiscordClient.Stop()
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := r.Store.Shutdown(shutdownCtx); err != nil {
		return err
	}

	return nil
}

func loadConfig(dir string) (appConfig, error) {
	files, err := os.ReadDir(dir)
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
		path := filepath.Join(dir, f.Name())
		opts = append(opts, config.File(path))
	}
	if len(opts) == 0 {
		return appConfig{}, fmt.Errorf("no yaml files found in %q", dir)
	}

	provider, err := config.NewYAML(opts...)
	if err != nil {
		return appConfig{}, err
	}

	var cfg appConfig
	if err := provider.Get(config.Root).Populate(&cfg); err != nil {
		return appConfig{}, err
	}

	// Normalize all character names/realms/regions to lowercase
	for i := range cfg.Characters {
		cfg.Characters[i].Name = strings.ToLower(cfg.Characters[i].Name)
		cfg.Characters[i].Realm = strings.ToLower(cfg.Characters[i].Realm)
		cfg.Characters[i].Region = strings.ToLower(cfg.Characters[i].Region)
	}

	return cfg, nil
}

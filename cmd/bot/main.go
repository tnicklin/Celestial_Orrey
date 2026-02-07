package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/tnicklin/celestial_orrey/config"
	"github.com/tnicklin/celestial_orrey/discord"
	"github.com/tnicklin/celestial_orrey/logger"
	rioClient "github.com/tnicklin/celestial_orrey/raiderio/client"
	"github.com/tnicklin/celestial_orrey/store"
	"github.com/tnicklin/celestial_orrey/warcraftlogs"
)

func main() {
	params, err := build()
	if err != nil {
		log.Fatal(err)
	}

	if err = run(params); err != nil {
		log.Fatal(err)
	}
}

func build() (runParams, error) {
	cfg, err := config.LoadWithDefaults("config/config.yaml", "config/secrets.yaml")
	if err != nil {
		return runParams{}, fmt.Errorf("load config: %w", err)
	}

	appLogger, err := logger.New(cfg.Logger)
	if err != nil {
		return runParams{}, fmt.Errorf("initialize logger: %w", err)
	}

	token := strings.TrimSpace(os.Getenv("DISCORD_TOKEN"))
	if token == "" {
		token = cfg.Discord.Token
	}
	if token == "" {
		return runParams{}, fmt.Errorf("DISCORD_TOKEN environment variable or discord.token config required")
	}

	guildID := strings.TrimSpace(os.Getenv("DISCORD_GUILD_ID"))
	if guildID == "" {
		guildID = cfg.Discord.GuildID
	}
	if guildID == "" {
		return runParams{}, fmt.Errorf("DISCORD_GUILD_ID environment variable or discord.guild_id config required")
	}

	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return runParams{}, fmt.Errorf("create discord session: %w", err)
	}
	session.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages | discordgo.IntentsMessageContent

	st := store.NewSQLiteStore(cfg.Store.Path)

	cfg.RaiderIO.Defaults()
	rio := rioClient.New(rioClient.Params{
		BaseURL:    cfg.RaiderIO.BaseURL,
		UserAgent:  cfg.RaiderIO.UserAgent,
		HTTPClient: cfg.RaiderIO.HTTPClient,
	})

	var wcl warcraftlogs.WCL
	if cfg.WarcraftLogs.ClientID != "" && cfg.WarcraftLogs.ClientSecret != "" {
		wclClient := warcraftlogs.New(warcraftlogs.Params{
			ClientID:     cfg.WarcraftLogs.ClientID,
			ClientSecret: cfg.WarcraftLogs.ClientSecret,
			HTTPClient:   &http.Client{Timeout: 30 * time.Second},
		})
		wcl = wclClient
	}

	discordClient := discord.New(discord.Params{
		Session:        session,
		GuildID:        guildID,
		CommandChannel: cfg.Discord.CommandChannel,
		ReportChannel:  cfg.Discord.ReportChannel,
		Store:          st,
		RaiderIO:       rio,
		WarcraftLogs:   wcl,
		Logger:         appLogger,
	})

	return runParams{
		Config:        cfg,
		Logger:        appLogger,
		Session:       session,
		Store:         st,
		RaiderIO:      rio,
		WarcraftLogs:  wcl,
		DiscordClient: discordClient,
		GuildID:       guildID,
	}, nil
}

type runParams struct {
	Config        *config.AppConfig
	Logger        logger.Logger
	Session       *discordgo.Session
	Store         *store.SQLiteStore
	RaiderIO      rioClient.Client
	WarcraftLogs  warcraftlogs.WCL
	DiscordClient discord.Discord
	GuildID       string
}

// run starts all components and runs the application until shutdown.
func run(p runParams) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer p.Logger.Sync()

	if err := p.Session.Open(); err != nil {
		return fmt.Errorf("open discord connection: %w", err)
	}
	defer p.Session.Close()

	if err := p.Store.Open(ctx); err != nil {
		return fmt.Errorf("open keydb store: %w", err)
	}

	if err := p.Store.RestoreFromDisk(ctx, p.Config.Store.Path); err != nil {
		p.Logger.WarnW("restore from disk", "error", err)
	}

	if err := p.DiscordClient.Start(ctx); err != nil {
		return fmt.Errorf("start discord client: %w", err)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	if err := p.DiscordClient.Stop(); err != nil {
		p.Logger.ErrorW("stop discord client", "error", err)
	}
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := p.Store.Shutdown(shutdownCtx); err != nil {
		return err
	}

	return nil
}

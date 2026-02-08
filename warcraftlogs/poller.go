package warcraftlogs

import (
	"context"
	"errors"
	"time"

	"github.com/tnicklin/celestial_orrey/logger"
	"github.com/tnicklin/celestial_orrey/store"
	"github.com/tnicklin/celestial_orrey/timeutil"
)

var _ Poller = (*DefaultPoller)(nil)

// Poller is the interface for the background WCL linking poller.
type Poller interface {
	Start(ctx context.Context) error
	Stop()
}

// DefaultPoller polls for unlinked keys and attempts to link them to WarcraftLogs.
type DefaultPoller struct {
	store       store.Store
	client      WCL
	logger      logger.Logger
	interval    time.Duration
	matchWindow time.Duration
	stop        chan struct{}
	done        chan struct{}
}

// PollerParams holds configuration for creating a new WCL Poller.
type PollerParams struct {
	Store       store.Store
	Client      WCL
	Logger      logger.Logger
	Interval    time.Duration
	MatchWindow time.Duration
}

// NewPoller creates a new WCL background linker poller.
func NewPoller(p PollerParams) *DefaultPoller {
	log := p.Logger
	if log == nil {
		log = logger.NewNop()
	}

	interval := p.Interval
	if interval <= 0 {
		interval = 5 * time.Minute
	}

	matchWindow := p.MatchWindow
	if matchWindow <= 0 {
		matchWindow = 24 * time.Hour
	}

	return &DefaultPoller{
		store:       p.Store,
		client:      p.Client,
		logger:      log,
		interval:    interval,
		matchWindow: matchWindow,
	}
}

// Start begins the background polling loop.
func (p *DefaultPoller) Start(ctx context.Context) error {
	if p.store == nil {
		return errors.New("warcraftlogs poller: store is required")
	}
	if p.client == nil {
		return errors.New("warcraftlogs poller: client is required")
	}

	p.stop = make(chan struct{})
	p.done = make(chan struct{})

	p.logger.InfoW("WCL poller starting",
		"interval", p.interval.String(),
		"match_window", p.matchWindow.String(),
	)

	go p.run(ctx)
	return nil
}

// Stop stops the background polling loop.
func (p *DefaultPoller) Stop() {
	if p.stop != nil {
		close(p.stop)
		<-p.done
	}
}

func (p *DefaultPoller) run(ctx context.Context) {
	defer close(p.done)

	// Run immediately on start
	p.pollOnce(ctx)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stop:
			p.logger.InfoW("WCL poller stopped")
			return
		case <-ctx.Done():
			p.logger.InfoW("WCL poller context cancelled")
			return
		case <-ticker.C:
			p.pollOnce(ctx)
		}
	}
}

func (p *DefaultPoller) pollOnce(ctx context.Context) {
	cutoff := timeutil.WeeklyReset()

	p.logger.DebugW("WCL poller: checking for unlinked keys",
		"cutoff", cutoff.Format(time.RFC3339),
	)

	keys, err := p.store.ListUnlinkedKeysSince(ctx, cutoff)
	if err != nil {
		p.logger.ErrorW("WCL poller: failed to list unlinked keys", "error", err)
		return
	}

	if len(keys) == 0 {
		p.logger.DebugW("WCL poller: no unlinked keys found")
		return
	}

	p.logger.InfoW("WCL poller: found unlinked keys",
		"count", len(keys),
	)

	linker := NewLinker(p.store, p.client, ReportFilter{}, p.logger)
	linker.MatchWindow = p.matchWindow

	linkedCount := 0
	for _, key := range keys {
		p.logger.DebugW("WCL poller: attempting to link key",
			"key_id", key.KeyID,
			"character", key.Character,
			"realm", key.Realm,
			"dungeon", key.Dungeon,
			"level", key.KeyLevel,
			"completed_at", key.CompletedAt,
		)

		match, err := linker.MatchKey(ctx, key)
		if err != nil {
			p.logger.WarnW("WCL poller: match error",
				"key_id", key.KeyID,
				"error", err,
			)
			continue
		}
		if match == nil {
			p.logger.DebugW("WCL poller: no match found",
				"key_id", key.KeyID,
				"dungeon", key.Dungeon,
			)
			continue
		}

		url := BuildMythicPlusURL(match.Run)
		fightID := int64(match.Run.FightID)

		link := store.WarcraftLogsLink{
			KeyID:      key.KeyID,
			ReportCode: match.Run.ReportCode,
			FightID:    &fightID,
			URL:        url,
		}
		if err := p.store.UpsertWarcraftLogsLink(ctx, link); err != nil {
			p.logger.WarnW("WCL poller: failed to store link",
				"key_id", key.KeyID,
				"error", err,
			)
			continue
		}

		linkedCount++
		p.logger.InfoW("WCL poller: link created",
			"key_id", key.KeyID,
			"character", key.Character,
			"dungeon", key.Dungeon,
			"report_code", match.Run.ReportCode,
			"fight_id", fightID,
			"confidence", match.Confidence,
			"url", url,
		)
	}

	p.logger.InfoW("WCL poller: poll complete",
		"unlinked_keys", len(keys),
		"newly_linked", linkedCount,
	)
}

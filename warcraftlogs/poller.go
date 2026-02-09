package warcraftlogs

import (
	"context"
	"errors"
	"time"

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
	interval    time.Duration
	matchWindow time.Duration
	stop        chan struct{}
	done        chan struct{}
}

// PollerParams holds configuration for creating a new WCL Poller.
type PollerParams struct {
	Store       store.Store
	Client      WCL
	Interval    time.Duration
	MatchWindow time.Duration
}

// NewPoller creates a new WCL background linker poller.
func NewPoller(p PollerParams) *DefaultPoller {
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
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.pollOnce(ctx)
		}
	}
}

func (p *DefaultPoller) pollOnce(ctx context.Context) {
	cutoff := timeutil.WeeklyReset()
	matchWindow := time.Since(cutoff) + 24*time.Hour

	keys, err := p.store.ListUnlinkedKeysSince(ctx, cutoff)
	if err != nil || len(keys) == 0 {
		return
	}

	linker := NewLinker(LinkerParams{
		Store:  p.store,
		Client: p.client,
	})
	linker.MatchWindow = matchWindow

	for _, key := range keys {
		match, err := linker.MatchKey(ctx, key)
		if err != nil || match == nil {
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
		_ = p.store.UpsertWarcraftLogsLink(ctx, link)
	}
}

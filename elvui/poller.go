package elvui

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/tnicklin/celestial_orrey/store"
)

var _ Poller = (*DefaultPoller)(nil)

// DefaultPoller polls the TukUI API for ElvUI version updates.
type DefaultPoller struct {
	client       *Client
	store        store.Store
	interval     time.Duration
	onNewVersion NotifyFunc
	stop         chan struct{}
	done         chan struct{}
}

// Params holds configuration for creating a new ElvUI Poller.
type Params struct {
	Config       Config
	Store        store.Store
	HTTPClient   *http.Client
	OnNewVersion NotifyFunc
}

// New creates a new ElvUI poller.
func New(p Params) *DefaultPoller {
	p.Config.Defaults()

	httpClient := p.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	return &DefaultPoller{
		client:       NewClient(p.Config.APIURL, httpClient),
		store:        p.Store,
		interval:     p.Config.PollInterval,
		onNewVersion: p.OnNewVersion,
	}
}

// Start begins the polling loop.
func (p *DefaultPoller) Start(ctx context.Context) error {
	if p.store == nil {
		return errors.New("elvui: store is required")
	}

	p.stop = make(chan struct{})
	p.done = make(chan struct{})

	go p.run(ctx)
	return nil
}

// Stop stops the polling loop.
func (p *DefaultPoller) Stop() {
	if p.stop != nil {
		close(p.stop)
		<-p.done
	}
}

func (p *DefaultPoller) run(ctx context.Context) {
	defer close(p.done)

	p.pollOnce(ctx, true)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.pollOnce(ctx, false)
		}
	}
}

func (p *DefaultPoller) pollOnce(ctx context.Context, isInitial bool) {
	info, err := p.client.FetchVersion(ctx)
	if err != nil {
		return
	}

	current, err := p.store.GetElvUIVersion(ctx)
	isNew := err != nil || current.Version != info.Version

	_ = p.store.UpsertElvUIVersion(ctx, store.ElvUIVersion{
		Version:      info.Version,
		DownloadURL:  info.URL,
		ChangelogURL: info.ChangelogURL,
		LastUpdate:   info.LastUpdate,
	})

	if isNew && !isInitial && p.onNewVersion != nil {
		p.onNewVersion(*info)
	}
}

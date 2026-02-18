package raiderio

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"

	"github.com/tnicklin/celestial_orrey/models"
	rioClient "github.com/tnicklin/celestial_orrey/raiderio/client"
	"github.com/tnicklin/celestial_orrey/store"
	"github.com/tnicklin/celestial_orrey/timeutil"
	"github.com/tnicklin/celestial_orrey/warcraftlogs"
)

var _ Poller = (*DefaultPoller)(nil)

// DefaultPoller polls RaiderIO for new M+ keys.
type DefaultPoller struct {
	client        rioClient.Client
	store         store.Store
	wclLinker     *warcraftlogs.Linker
	interval      time.Duration
	maxConcurrent int
	rng           *rand.Rand

	mu    sync.Mutex
	known map[string]map[string]struct{} // character key -> known key IDs
	stop  chan struct{}
	done  chan struct{}
}

// Params holds configuration for creating a new Poller.
type Params struct {
	Config    Config
	Client    rioClient.Client
	Store     store.Store
	WCLLinker *warcraftlogs.Linker
}

// New creates a new DefaultPoller with the given parameters.
func New(p Params) *DefaultPoller {
	p.Config.Defaults()

	client := p.Client
	if client == nil {
		client = rioClient.New(rioClient.Params{
			BaseURL:    p.Config.BaseURL,
			UserAgent:  p.Config.UserAgent,
			HTTPClient: p.Config.HTTPClient,
		})
	}

	return &DefaultPoller{
		client:        client,
		store:         p.Store,
		wclLinker:     p.WCLLinker,
		interval:      p.Config.PollInterval,
		maxConcurrent: p.Config.MaxConcurrent,
		rng:           rand.New(rand.NewSource(time.Now().UnixNano())),
		known:         make(map[string]map[string]struct{}),
	}
}

// Start begins the polling loop.
func (p *DefaultPoller) Start(ctx context.Context) error {
	if p.client == nil {
		return errors.New("raiderio: client is required")
	}
	if p.store == nil {
		return errors.New("raiderio: store is required")
	}

	p.stop = make(chan struct{})
	p.done = make(chan struct{})

	go p.run(ctx)
	return nil
}

func (p *DefaultPoller) run(ctx context.Context) {
	defer close(p.done)

	// Poll immediately on start
	p.pollAllCharacters(ctx)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.pollAllCharacters(ctx)
		}
	}
}

// Stop stops the polling loop.
func (p *DefaultPoller) Stop() {
	if p.stop != nil {
		close(p.stop)
		<-p.done
	}
}

func (p *DefaultPoller) pollAllCharacters(ctx context.Context) {
	characters, err := p.store.ListCharacters(ctx)
	if err != nil || len(characters) == 0 {
		return
	}

	sem := make(chan struct{}, p.maxConcurrent)
	var wg sync.WaitGroup

	for _, char := range characters {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case <-p.stop:
			wg.Wait()
			return
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(c models.Character) {
			defer wg.Done()
			defer func() { <-sem }()
			p.pollCharacter(ctx, c)
		}(char)
	}

	wg.Wait()
}

func (p *DefaultPoller) pollCharacter(ctx context.Context, character models.Character) {
	charKey := character.Key()

	p.mu.Lock()
	known, ok := p.known[charKey]
	if !ok {
		known = make(map[string]struct{})
		p.known[charKey] = known

		cutoff := timeutil.WeeklyReset()
		existingKeys, err := p.store.ListKeysByCharacterSince(ctx, character.Name, cutoff)
		if err == nil {
			for _, key := range existingKeys {
				known[key.KeyIDOrSynthetic()] = struct{}{}
			}
		}
	}
	p.mu.Unlock()

	keys, err := p.client.FetchWeeklyRuns(ctx, character)
	if err != nil {
		return
	}

	cutoff := timeutil.WeeklyReset()
	for _, key := range keys {
		if !afterCutoff(key.CompletedAt, cutoff) {
			continue
		}

		keyID := key.KeyIDOrSynthetic()

		p.mu.Lock()
		_, exists := known[keyID]
		if !exists {
			known[keyID] = struct{}{}
		}
		p.mu.Unlock()

		if exists {
			continue
		}

		if err := p.store.UpsertCompletedKey(ctx, key); err != nil {
			continue
		}

		p.linkToWCL(ctx, key)
	}
}

// linkToWCL attempts to link a newly detected key to WarcraftLogs.
func (p *DefaultPoller) linkToWCL(ctx context.Context, key models.CompletedKey) {
	if p.wclLinker == nil {
		return
	}

	match, err := p.wclLinker.MatchKey(ctx, key)
	if err != nil || match == nil {
		return
	}

	url := warcraftlogs.BuildMythicPlusURL(match.Run)
	fightID := int64(match.Run.FightID)

	link := store.WarcraftLogsLink{
		KeyID:      key.KeyID,
		ReportCode: match.Run.ReportCode,
		FightID:    &fightID,
		URL:        url,
	}
	_ = p.store.UpsertWarcraftLogsLink(ctx, link)
}

func afterCutoff(completedAt string, cutoff time.Time) bool {
	t, err := timeutil.ParseRFC3339(completedAt)
	if err != nil {
		return false
	}
	return t.After(cutoff)
}

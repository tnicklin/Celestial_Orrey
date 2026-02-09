package raiderio

import (
	"context"
	"errors"
	"math/rand"
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
	characters    []models.Character
	interval      time.Duration
	maxConcurrent int
	rng           *rand.Rand
}

// Params holds configuration for creating a new Poller.
type Params struct {
	Config     Config
	Client     rioClient.Client
	Store      store.Store
	WCLLinker  *warcraftlogs.Linker
	Characters []models.Character
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
		characters:    p.Characters,
		interval:      p.Config.PollInterval,
		maxConcurrent: p.Config.MaxConcurrent,
		rng:           rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Start begins the polling loop for all characters.
func (p *DefaultPoller) Start(ctx context.Context) error {
	if p.client == nil {
		return errors.New("raiderio: client is required")
	}
	if p.store == nil {
		return errors.New("raiderio: store is required")
	}
	if len(p.characters) == 0 {
		return nil
	}

	sem := make(chan struct{}, p.maxConcurrent)
	offsetStep := p.interval / time.Duration(len(p.characters))

	for i, character := range p.characters {
		known, err := p.loadKnownKeys(ctx, character)
		if err != nil {
			return err
		}

		initialDelay := time.Duration(i) * offsetStep
		go p.runCharacter(ctx, character, known, sem, initialDelay)
	}
	return nil
}

func (p *DefaultPoller) loadKnownKeys(ctx context.Context, character models.Character) (map[string]struct{}, error) {
	cutoff := timeutil.WeeklyReset()
	keys, err := p.store.ListKeysByCharacterSince(ctx, character.Name, cutoff)
	if err != nil {
		return nil, err
	}

	known := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		keyID := key.KeyIDOrSynthetic()
		known[keyID] = struct{}{}
	}
	return known, nil
}

func (p *DefaultPoller) runCharacter(ctx context.Context, character models.Character, known map[string]struct{}, sem chan struct{}, initialDelay time.Duration) {
	if initialDelay > 0 {
		select {
		case <-time.After(initialDelay):
		case <-ctx.Done():
			return
		}
	}

	for {
		_ = p.pollOnce(ctx, character, known, sem)

		wait := p.interval
		if p.interval > 0 {
			jitterWindow := p.interval / 10
			if jitterWindow > 0 {
				jitter := time.Duration(p.rng.Int63n(int64(jitterWindow)))
				wait = p.interval + jitter
			}
		}

		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return
		}
	}
}

func (p *DefaultPoller) pollOnce(ctx context.Context, character models.Character, known map[string]struct{}, sem chan struct{}) error {
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-sem }()

	keys, err := p.client.FetchWeeklyRuns(ctx, character)
	if err != nil {
		return err
	}

	cutoff := timeutil.WeeklyReset()

	for _, key := range keys {
		if !afterCutoff(key.CompletedAt, cutoff) {
			continue
		}

		keyID := key.KeyIDOrSynthetic()
		if _, ok := known[keyID]; ok {
			continue
		}

		if err = p.store.UpsertCompletedKey(ctx, key); err != nil {
			continue
		}

		// Attempt WCL linking for new key
		p.linkToWCL(ctx, key)

		known[keyID] = struct{}{}
	}

	return nil
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

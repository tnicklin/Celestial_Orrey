package raiderio

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"time"

	"github.com/tnicklin/celestial_orrey/logger"
	"github.com/tnicklin/celestial_orrey/models"
	rioClient "github.com/tnicklin/celestial_orrey/raiderio/client"
	"github.com/tnicklin/celestial_orrey/store"
	"github.com/tnicklin/celestial_orrey/timeutil"
)

var _ Poller = (*DefaultPoller)(nil)

// DefaultPoller polls RaiderIO for new M+ keys.
type DefaultPoller struct {
	client        rioClient.Client
	store         store.Store
	characters    []models.Character
	interval      time.Duration
	maxConcurrent int
	logger        logger.Logger
	onNewKey      func(ctx context.Context, key models.CompletedKey)
	onAnnounce    func(ctx context.Context, message string)
	rng           *rand.Rand
}

// Params holds configuration for creating a new Poller.
type Params struct {
	Config     Config
	Client     rioClient.Client // optional, for testing; if nil, created from Config
	Store      store.Store
	Characters []models.Character
	Logger     logger.Logger
	OnNewKey   func(ctx context.Context, key models.CompletedKey)
	OnAnnounce func(ctx context.Context, message string)
}

// New creates a new DefaultPoller with the given parameters.
func New(p Params) *DefaultPoller {
	p.Config.Defaults()

	log := p.Logger
	if log == nil {
		log = logger.NewNop()
	}

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
		characters:    p.Characters,
		interval:      p.Config.PollInterval,
		maxConcurrent: p.Config.MaxConcurrent,
		logger:        log,
		onNewKey:      p.OnNewKey,
		onAnnounce:    p.OnAnnounce,
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
		known[key.KeyIDOrSynthetic()] = struct{}{}
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
		if err := p.pollOnce(ctx, character, known, sem); err != nil && !errors.Is(err, context.Canceled) {
			p.logger.ErrorW("poll failed", "character", character.Key(), "error", err)
		}

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
			p.logger.ErrorW("store upsert failed", "error", err, "key_id", keyID)
			continue
		}

		p.logger.InfoW("new key completed",
			"character", key.Character,
			"dungeon", key.Dungeon,
			"level", key.KeyLevel,
			"key_id", key.KeyID,
		)

		if p.onNewKey != nil {
			p.onNewKey(ctx, key)
		}

		if p.onAnnounce != nil {
			msg := formatAnnouncement(key)
			p.onAnnounce(ctx, msg)
		}

		known[keyID] = struct{}{}
	}

	return nil
}

func formatAnnouncement(key models.CompletedKey) string {
	base := fmt.Sprintf("%s +%d %s", key.Character, key.KeyLevel, key.Dungeon)
	if key.ParTimeMS <= 0 || key.RunTimeMS <= 0 {
		return base
	}

	diffMS := key.RunTimeMS - key.ParTimeMS
	diffMin := int64(math.Abs(float64(diffMS)) / 60000)
	sign := "+"
	if diffMS < 0 {
		sign = "-"
	}
	return fmt.Sprintf("%s (%s%d min vs par)", base, sign, diffMin)
}

func afterCutoff(completedAt string, cutoff time.Time) bool {
	t, err := timeutil.ParseRFC3339(completedAt)
	if err != nil {
		return false
	}
	return t.After(cutoff)
}

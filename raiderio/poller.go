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
	logger        logger.Logger
	rng           *rand.Rand
}

// Params holds configuration for creating a new Poller.
type Params struct {
	Config     Config
	Client     rioClient.Client
	Store      store.Store
	WCLLinker  *warcraftlogs.Linker
	Characters []models.Character
	Logger     logger.Logger
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
		wclLinker:     p.WCLLinker,
		characters:    p.Characters,
		interval:      p.Config.PollInterval,
		maxConcurrent: p.Config.MaxConcurrent,
		logger:        log,
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
		p.logger.InfoW("poller start: no characters configured")
		return nil
	}

	p.logger.InfoW("poller starting",
		"characters", len(p.characters),
		"interval", p.interval.String(),
		"max_concurrent", p.maxConcurrent,
		"wcl_linker_configured", p.wclLinker != nil,
	)

	sem := make(chan struct{}, p.maxConcurrent)
	offsetStep := p.interval / time.Duration(len(p.characters))

	for i, character := range p.characters {
		known, err := p.loadKnownKeys(ctx, character)
		if err != nil {
			p.logger.ErrorW("failed to load known keys", "character", character.Key(), "error", err)
			return err
		}

		p.logger.DebugW("loaded known keys for character",
			"character", character.Key(),
			"known_count", len(known),
		)

		initialDelay := time.Duration(i) * offsetStep
		go p.runCharacter(ctx, character, known, sem, initialDelay)
	}
	return nil
}

func (p *DefaultPoller) loadKnownKeys(ctx context.Context, character models.Character) (map[string]struct{}, error) {
	cutoff := timeutil.WeeklyReset()
	p.logger.DebugW("loading known keys",
		"character", character.Key(),
		"cutoff", cutoff.Format(time.RFC3339),
	)

	keys, err := p.store.ListKeysByCharacterSince(ctx, character.Name, cutoff)
	if err != nil {
		return nil, err
	}

	known := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		keyID := key.KeyIDOrSynthetic()
		known[keyID] = struct{}{}
		p.logger.DebugW("known key loaded",
			"character", character.Key(),
			"key_id", key.KeyID,
			"synthetic_id", keyID,
			"dungeon", key.Dungeon,
			"level", key.KeyLevel,
		)
	}

	p.logger.DebugW("known keys loaded",
		"character", character.Key(),
		"count", len(known),
	)
	return known, nil
}

func (p *DefaultPoller) runCharacter(ctx context.Context, character models.Character, known map[string]struct{}, sem chan struct{}, initialDelay time.Duration) {
	if initialDelay > 0 {
		p.logger.DebugW("character poll initial delay",
			"character", character.Key(),
			"delay", initialDelay.String(),
		)
		select {
		case <-time.After(initialDelay):
		case <-ctx.Done():
			return
		}
	}

	for {
		p.logger.DebugW("starting poll for character",
			"character", character.Key(),
		)

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

		p.logger.DebugW("poll complete, waiting for next cycle",
			"character", character.Key(),
			"next_poll_in", wait.String(),
		)

		select {
		case <-time.After(wait):
		case <-ctx.Done():
			p.logger.DebugW("character poll stopped",
				"character", character.Key(),
			)
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

	p.logger.DebugW("poll cycle starting",
		"character", character.Key(),
		"known_keys", len(known),
	)

	keys, err := p.client.FetchWeeklyRuns(ctx, character)
	if err != nil {
		p.logger.ErrorW("fetch weekly runs failed",
			"character", character.Key(),
			"error", err,
		)
		return err
	}

	p.logger.DebugW("fetched keys from raiderio",
		"character", character.Key(),
		"fetched_count", len(keys),
	)

	cutoff := timeutil.WeeklyReset()
	p.logger.DebugW("using weekly reset cutoff",
		"cutoff", cutoff.Format(time.RFC3339),
	)

	newKeysCount := 0
	skippedCutoff := 0
	skippedKnown := 0
	upsertErrors := 0

	for _, key := range keys {
		p.logger.DebugW("processing key",
			"character", key.Character,
			"realm", key.Realm,
			"region", key.Region,
			"dungeon", key.Dungeon,
			"level", key.KeyLevel,
			"key_id", key.KeyID,
			"completed_at", key.CompletedAt,
		)

		if !afterCutoff(key.CompletedAt, cutoff) {
			p.logger.DebugW("key skipped: before cutoff",
				"key_id", key.KeyID,
				"completed_at", key.CompletedAt,
				"cutoff", cutoff.Format(time.RFC3339),
			)
			skippedCutoff++
			continue
		}

		keyID := key.KeyIDOrSynthetic()
		if _, ok := known[keyID]; ok {
			p.logger.DebugW("key skipped: already known",
				"key_id", key.KeyID,
				"synthetic_id", keyID,
			)
			skippedKnown++
			continue
		}

		p.logger.DebugW("upserting new key",
			"key_id", key.KeyID,
			"synthetic_id", keyID,
			"dungeon", key.Dungeon,
			"level", key.KeyLevel,
		)

		if err = p.store.UpsertCompletedKey(ctx, key); err != nil {
			p.logger.ErrorW("store upsert failed",
				"error", err,
				"key_id", keyID,
				"character", key.Character,
				"dungeon", key.Dungeon,
			)
			upsertErrors++
			continue
		}

		p.logger.InfoW("new key completed",
			"character", key.Character,
			"realm", key.Realm,
			"region", key.Region,
			"dungeon", key.Dungeon,
			"level", key.KeyLevel,
			"key_id", key.KeyID,
		)

		// Attempt WCL linking for new key
		p.linkToWCL(ctx, key)

		known[keyID] = struct{}{}
		newKeysCount++
	}

	p.logger.DebugW("poll cycle complete",
		"character", character.Key(),
		"fetched", len(keys),
		"new_keys", newKeysCount,
		"skipped_cutoff", skippedCutoff,
		"skipped_known", skippedKnown,
		"upsert_errors", upsertErrors,
	)

	return nil
}

// linkToWCL attempts to link a newly detected key to WarcraftLogs.
func (p *DefaultPoller) linkToWCL(ctx context.Context, key models.CompletedKey) {
	if p.wclLinker == nil {
		return
	}

	p.logger.InfoW("attempting WCL link for new key",
		"character", key.Character,
		"dungeon", key.Dungeon,
		"level", key.KeyLevel,
		"key_id", key.KeyID,
	)

	match, err := p.wclLinker.MatchKey(ctx, key)
	if err != nil {
		p.logger.WarnW("WCL match error",
			"key_id", key.KeyID,
			"error", err,
		)
		return
	}
	if match == nil {
		p.logger.DebugW("no WCL match found for new key",
			"key_id", key.KeyID,
			"dungeon", key.Dungeon,
		)
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
	if err := p.store.UpsertWarcraftLogsLink(ctx, link); err != nil {
		p.logger.WarnW("failed to store WCL link",
			"key_id", key.KeyID,
			"error", err,
		)
		return
	}

	p.logger.InfoW("WCL link created for new key",
		"key_id", key.KeyID,
		"report_code", match.Run.ReportCode,
		"url", url,
		"confidence", match.Confidence,
	)
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

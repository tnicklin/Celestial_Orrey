package warcraftlogs

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"
	"unicode"

	"github.com/tnicklin/celestial_orrey/models"
	"github.com/tnicklin/celestial_orrey/store"
	"github.com/tnicklin/celestial_orrey/timeutil"
)

// Linker matches RaiderIO completed keys to WarcraftLogs reports.
type Linker struct {
	Store        store.Store
	Client       WCL
	Filter       ReportFilter
	MatchWindow  time.Duration
	PreBuffer    time.Duration
	PostBuffer   time.Duration
	DungeonMatch func(dungeon, zone string) bool
}

// LinkerParams holds configuration for creating a new Linker.
type LinkerParams struct {
	Store  store.Store
	Client WCL
	Filter ReportFilter
}

// NewLinker creates a new Linker with the given parameters.
func NewLinker(p LinkerParams) *Linker {
	return &Linker{
		Store:       p.Store,
		Client:      p.Client,
		Filter:      p.Filter,
		MatchWindow: time.Since(timeutil.WeeklyReset()) + 24*time.Hour,
		PreBuffer:   15 * time.Minute,
		PostBuffer:  30 * time.Minute,
		DungeonMatch: func(dungeon, zone string) bool {
			return defaultDungeonMatch(dungeon, zone)
		},
	}
}

// RunOnce attempts to link RaiderIO keys completed since the given time to WarcraftLogs reports.
func (l *Linker) RunOnce(ctx context.Context, since time.Time) (int, error) {
	if l.Store == nil || l.Client == nil {
		return 0, errors.New("warcraftlogs: store and client are required")
	}

	keys, err := l.Store.ListKeysSince(ctx, since)
	if err != nil {
		return 0, err
	}
	if len(keys) == 0 {
		return 0, nil
	}

	minTime, maxTime := minMaxCompletedAt(keys)
	if minTime.IsZero() || maxTime.IsZero() {
		return 0, nil
	}

	filter := l.Filter
	filter.StartTime = minTime.Add(-l.MatchWindow)
	filter.EndTime = maxTime.Add(l.MatchWindow)

	reports, err := l.Client.FetchReports(ctx, filter)
	if err != nil {
		return 0, err
	}

	linked := 0
	for _, key := range keys {
		if key.KeyID <= 0 {
			continue
		}
		existing, err := l.Store.ListWarcraftLogsLinksForKey(ctx, key.KeyID)
		if err == nil && len(existing) > 0 {
			continue
		}
		match := bestReportMatch(key, reports, l.DungeonMatch, l.PreBuffer, l.PostBuffer, l.MatchWindow)
		if match == nil {
			continue
		}

		link := store.WarcraftLogsLink{
			KeyID:      key.KeyID,
			ReportCode: match.Code,
			URL:        BuildReportURL(match.Code, nil, nil),
		}
		if err := l.Store.UpsertWarcraftLogsLink(ctx, link); err != nil {
			continue
		}
		linked++
	}

	return linked, nil
}

func minMaxCompletedAt(keys []models.CompletedKey) (time.Time, time.Time) {
	var min time.Time
	var max time.Time
	for _, key := range keys {
		t, err := timeutil.ParseRFC3339(key.CompletedAt)
		if err != nil {
			continue
		}
		if min.IsZero() || t.Before(min) {
			min = t
		}
		if max.IsZero() || t.After(max) {
			max = t
		}
	}
	return min, max
}

func bestReportMatch(key models.CompletedKey, reports []ReportSummary, matchFn func(string, string) bool, preBuffer, postBuffer, window time.Duration) *ReportSummary {
	keyTime, err := timeutil.ParseRFC3339(key.CompletedAt)
	if err != nil {
		return nil
	}
	if matchFn == nil {
		matchFn = defaultDungeonMatch
	}

	var best *ReportSummary
	bestScore := math.MaxFloat64
	for i := range reports {
		report := &reports[i]
		if report.Code == "" || report.Start.IsZero() {
			continue
		}
		if !matchFn(key.Dungeon, report.ZoneName) {
			continue
		}

		start := report.Start.Add(-preBuffer)
		end := report.End.Add(postBuffer)
		if report.End.IsZero() {
			end = report.Start.Add(window)
		}

		if keyTime.Before(start) || keyTime.After(end) {
			continue
		}

		score := math.Abs(float64(keyTime.Sub(report.Start)))
		if score < bestScore {
			best = report
			bestScore = score
		}
	}
	return best
}

func defaultDungeonMatch(dungeon, zone string) bool {
	a := normalizeName(dungeon)
	b := normalizeName(zone)
	if a == "" || b == "" {
		return false
	}
	return a == b || strings.Contains(a, b) || strings.Contains(b, a)
}

func normalizeName(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// MatchKey attempts to find a WarcraftLogs M+ run that matches a RaiderIO completed key.
func (l *Linker) MatchKey(ctx context.Context, key models.CompletedKey) (*MatchResult, error) {
	char := models.Character{
		Name:   key.Character,
		Realm:  key.Realm,
		Region: key.Region,
	}

	runs, err := l.Client.FetchCharacterMythicPlus(ctx, char, 10)
	if err != nil {
		return nil, err
	}

	keyTime, err := timeutil.ParseRFC3339(key.CompletedAt)
	if err != nil {
		return nil, err
	}

	return matchKeyToRuns(key, keyTime, runs, l.DungeonMatch, l.MatchWindow), nil
}

// MatchKeys attempts to match multiple RaiderIO keys to WarcraftLogs runs for a character.
func (l *Linker) MatchKeys(ctx context.Context, keys []models.CompletedKey) ([]MatchResult, error) {
	if len(keys) == 0 {
		return nil, nil
	}

	byChar := make(map[string][]models.CompletedKey)
	for _, key := range keys {
		charKey := models.Character{
			Name:   key.Character,
			Realm:  key.Realm,
			Region: key.Region,
		}.Key()
		byChar[charKey] = append(byChar[charKey], key)
	}

	var results []MatchResult
	for _, charKeys := range byChar {
		if len(charKeys) == 0 {
			continue
		}

		char := models.Character{
			Name:   charKeys[0].Character,
			Realm:  charKeys[0].Realm,
			Region: charKeys[0].Region,
		}

		runs, err := l.Client.FetchCharacterMythicPlus(ctx, char, 10)
		if err != nil {
			continue
		}

		for _, key := range charKeys {
			keyTime, err := timeutil.ParseRFC3339(key.CompletedAt)
			if err != nil {
				continue
			}

			match := matchKeyToRuns(key, keyTime, runs, l.DungeonMatch, l.MatchWindow)
			if match == nil {
				continue
			}
			results = append(results, *match)
		}
	}

	return results, nil
}

// matchKeyToRuns finds the best matching WCL run for a RaiderIO key.
func matchKeyToRuns(key models.CompletedKey, keyTime time.Time, runs []MythicPlusRun, matchFn func(string, string) bool, window time.Duration) *MatchResult {
	if matchFn == nil {
		matchFn = defaultDungeonMatch
	}

	var best *MythicPlusRun
	bestConfidence := 0.0

	for i := range runs {
		run := &runs[i]

		if run.KeystoneLevel == 0 {
			continue
		}

		if !run.Kill && run.KeystoneTime == 0 {
			continue
		}

		if run.KeystoneLevel != key.KeyLevel {
			continue
		}

		if !matchFn(key.Dungeon, run.Dungeon) {
			continue
		}

		timeDiff := keyTime.Sub(run.CompletedAt)
		if timeDiff < 0 {
			timeDiff = -timeDiff
		}
		if timeDiff > window {
			continue
		}

		confidence := calculateMatchConfidence(key, run, timeDiff, window)

		if confidence > bestConfidence {
			best = run
			bestConfidence = confidence
		}
	}

	if best == nil {
		return nil
	}

	return &MatchResult{
		Key:        key,
		Run:        *best,
		Confidence: bestConfidence,
	}
}

// calculateMatchConfidence scores how well a key matches a WCL run.
func calculateMatchConfidence(key models.CompletedKey, run *MythicPlusRun, timeDiff, window time.Duration) float64 {
	confidence := 0.0

	timeScore := 1.0 - (float64(timeDiff) / float64(window))
	confidence += timeScore * 0.4

	if run.KeystoneTime > 0 && key.RunTimeMS > 0 {
		runTimeDiff := math.Abs(float64(run.KeystoneTime - key.RunTimeMS))
		if runTimeDiff < 5000 {
			runTimeScore := 1.0 - (runTimeDiff / 5000)
			confidence += runTimeScore * 0.4
		}
	}

	confidence += 0.2

	return confidence
}

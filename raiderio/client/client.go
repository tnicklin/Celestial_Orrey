package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/tnicklin/celestial_orrey/logger"
	"github.com/tnicklin/celestial_orrey/models"
)

var _ Client = (*DefaultClient)(nil)

// DefaultClient is the RaiderIO API client.
type DefaultClient struct {
	baseURL   string
	userAgent string
	http      *http.Client
	logger    logger.Logger
}

type Params struct {
	BaseURL    string
	UserAgent  string
	HTTPClient *http.Client
	Logger     logger.Logger
}

// New creates a new RaiderIO client from the given config.
func New(p Params) *DefaultClient {
	return &DefaultClient{
		baseURL:   p.BaseURL,
		userAgent: p.UserAgent,
		http:      p.HTTPClient,
		logger:    p.Logger,
	}
}

func (c *DefaultClient) log() logger.Logger {
	if c.logger == nil {
		return nopLogger{}
	}
	return c.logger
}

// nopLogger is a no-op logger for when no logger is configured.
type nopLogger struct{}

func (nopLogger) DebugW(_ string, _ ...any) {}
func (nopLogger) InfoW(_ string, _ ...any)  {}
func (nopLogger) WarnW(_ string, _ ...any)  {}
func (nopLogger) ErrorW(_ string, _ ...any) {}
func (nopLogger) Sync() error               { return nil }

func (c *DefaultClient) FetchWeeklyRuns(ctx context.Context, character models.Character) ([]models.CompletedKey, error) {
	endpoint, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, err
	}
	endpoint.Path = "/api/v1/characters/profile"

	query := endpoint.Query()
	query.Set("region", character.Region)
	query.Set("realm", character.Realm)
	query.Set("name", character.Name)
	query.Set("fields", "mythic_plus_weekly_highest_level_runs")
	endpoint.RawQuery = query.Encode()

	c.log().DebugW("fetching weekly runs from raiderio",
		"url", endpoint.String(),
		"character", character.Name,
		"realm", character.Realm,
		"region", character.Region,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		c.log().ErrorW("raiderio request failed",
			"error", err,
			"character", character.Name,
		)
		return nil, err
	}
	defer resp.Body.Close()

	c.log().DebugW("raiderio response received",
		"status", resp.StatusCode,
		"character", character.Name,
	)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		c.log().ErrorW("raiderio non-2xx response",
			"status", resp.StatusCode,
			"body", string(body),
			"character", character.Name,
		)
		return nil, fmt.Errorf("raiderio: status %d: %s", resp.StatusCode, string(body))
	}

	var payload profileResponse
	if err = json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		c.log().ErrorW("failed to decode raiderio response",
			"error", err,
			"character", character.Name,
		)
		return nil, err
	}

	c.log().DebugW("raiderio response decoded",
		"character", character.Name,
		"weekly_runs_count", len(payload.WeeklyRuns),
	)

	out := make([]models.CompletedKey, 0, len(payload.WeeklyRuns))
	for _, run := range payload.WeeklyRuns {
		key := models.CompletedKey{
			KeyID:       run.MythicPlusRunID,
			Character:   character.Name,
			Region:      character.Region,
			Realm:       character.Realm,
			Dungeon:     run.Dungeon,
			KeyLevel:    run.KeystoneLevel,
			RunTimeMS:   run.ClearTimeMS,
			ParTimeMS:   run.ParTimeMS,
			CompletedAt: run.CompletedAt,
			Source:      "raiderio",
		}
		out = append(out, key)

		c.log().DebugW("parsed weekly run",
			"character", character.Name,
			"key_id", run.MythicPlusRunID,
			"dungeon", run.Dungeon,
			"level", run.KeystoneLevel,
			"completed_at", run.CompletedAt,
		)
	}

	c.log().InfoW("fetched weekly runs",
		"character", character.Name,
		"realm", character.Realm,
		"count", len(out),
	)

	return out, nil
}

type profileResponse struct {
	WeeklyRuns []weeklyRun `json:"mythic_plus_weekly_highest_level_runs"`
}

type weeklyRun struct {
	MythicPlusRunID int64  `json:"keystone_run_id"`
	Dungeon         string `json:"dungeon"`
	KeystoneLevel   int    `json:"mythic_level"`
	ClearTimeMS     int64  `json:"clear_time_ms"`
	ParTimeMS       int64  `json:"par_time_ms"`
	CompletedAt     string `json:"completed_at"`
}

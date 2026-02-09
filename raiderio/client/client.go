package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/tnicklin/celestial_orrey/models"
)

var _ Client = (*DefaultClient)(nil)

// DefaultClient is the RaiderIO API client.
type DefaultClient struct {
	baseURL   string
	userAgent string
	http      *http.Client
}

type Params struct {
	BaseURL    string
	UserAgent  string
	HTTPClient *http.Client
}

// New creates a new RaiderIO client from the given config.
func New(p Params) *DefaultClient {
	return &DefaultClient{
		baseURL:   p.BaseURL,
		userAgent: p.UserAgent,
		http:      p.HTTPClient,
	}
}

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
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return nil, fmt.Errorf("raiderio: status %d: %s", resp.StatusCode, string(body))
	}

	var payload profileResponse
	if err = json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	out := make([]models.CompletedKey, 0, len(payload.WeeklyRuns))
	for _, run := range payload.WeeklyRuns {
		key := models.CompletedKey{
			KeyID:       run.MythicPlusRunID,
			Character:   strings.ToLower(character.Name),
			Region:      strings.ToLower(character.Region),
			Realm:       strings.ToLower(character.Realm),
			Dungeon:     run.Dungeon,
			KeyLevel:    run.KeystoneLevel,
			RunTimeMS:   run.ClearTimeMS,
			ParTimeMS:   run.ParTimeMS,
			CompletedAt: run.CompletedAt,
			Source:      "raiderio",
		}
		out = append(out, key)
	}

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

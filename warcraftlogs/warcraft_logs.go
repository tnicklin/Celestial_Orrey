package warcraftlogs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/tnicklin/celestial_orrey/models"
)

var _ WCL = (*DefaultWCL)(nil)

const (
	defaultGraphQLURL = "https://www.warcraftlogs.com/api/v2/client"
	defaultTokenURL   = "https://www.warcraftlogs.com/oauth/token"
)

type DefaultWCL struct {
	graphQLURL string
	tokenURL   string
	userAgent  string
	clientID   string
	secret     string
	http       *http.Client

	mu          sync.Mutex
	token       string
	tokenExpiry time.Time
}

type Params struct {
	ClientID     string
	ClientSecret string
	GraphQLURL   string
	TokenURL     string
	UserAgent    string
	HTTPClient   *http.Client
}

func New(p Params) *DefaultWCL {
	graphQLURL := p.GraphQLURL
	if graphQLURL == "" {
		graphQLURL = defaultGraphQLURL
	}
	tokenURL := p.TokenURL
	if tokenURL == "" {
		tokenURL = defaultTokenURL
	}
	httpClient := p.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}

	return &DefaultWCL{
		graphQLURL: graphQLURL,
		tokenURL:   tokenURL,
		userAgent:  p.UserAgent,
		clientID:   p.ClientID,
		secret:     p.ClientSecret,
		http:       httpClient,
	}
}

func (c *DefaultWCL) Query(ctx context.Context, query string, variables map[string]any) (json.RawMessage, error) {
	if query == "" {
		return nil, errors.New("warcraftlogs: query is empty")
	}

	token, err := c.getToken(ctx)
	if err != nil {
		return nil, err
	}

	payload := map[string]any{
		"query":     query,
		"variables": variables,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.graphQLURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
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
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return nil, fmt.Errorf("warcraftlogs: status %d: %s", resp.StatusCode, string(data))
	}

	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors any             `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, err
	}
	if envelope.Errors != nil {
		return nil, fmt.Errorf("warcraftlogs: graphql errors: %v", envelope.Errors)
	}
	return envelope.Data, nil
}

func (c *DefaultWCL) getToken(ctx context.Context) (string, error) {
	if c.clientID == "" || c.secret == "" {
		return "", errors.New("warcraftlogs: missing client credentials")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.token != "" && time.Now().Before(c.tokenExpiry.Add(-30*time.Second)) {
		return c.token, nil
	}

	form := url.Values{}
	form.Set("grant_type", "client_credentials")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	credentials := base64.StdEncoding.EncodeToString([]byte(c.clientID + ":" + c.secret))
	req.Header.Set("Authorization", "Basic "+credentials)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return "", fmt.Errorf("warcraftlogs: token status %d: %s", resp.StatusCode, string(data))
	}

	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.AccessToken == "" {
		return "", errors.New("warcraftlogs: empty access token")
	}

	c.token = payload.AccessToken
	if payload.ExpiresIn > 0 {
		c.tokenExpiry = time.Now().Add(time.Duration(payload.ExpiresIn) * time.Second)
	} else {
		c.tokenExpiry = time.Now().Add(5 * time.Minute)
	}

	return c.token, nil
}

// FetchCharacterMythicPlus fetches recent M+ runs for a character.
func (c *DefaultWCL) FetchCharacterMythicPlus(ctx context.Context, char models.Character, limit int) ([]MythicPlusRun, error) {
	if limit <= 0 {
		limit = 10
	}

	// Convert realm to server slug (lowercase, remove apostrophes/hyphens/spaces)
	serverSlug := strings.ToLower(char.Realm)
	serverSlug = strings.ReplaceAll(serverSlug, "'", "")
	serverSlug = strings.ReplaceAll(serverSlug, "-", "")
	serverSlug = strings.ReplaceAll(serverSlug, " ", "")

	query := `
	query($name: String!, $serverSlug: String!, $serverRegion: String!, $limit: Int!) {
		characterData {
			character(name: $name, serverSlug: $serverSlug, serverRegion: $serverRegion) {
				id
				name
				recentReports(limit: $limit) {
					data {
						code
						title
						startTime
						fights {
							id
							name
							encounterID
							difficulty
							keystoneLevel
							keystoneTime
							keystoneBonus
							rating
							endTime
							kill
						}
					}
				}
			}
		}
	}
	`

	variables := map[string]any{
		"name":         char.Name,
		"serverSlug":   serverSlug,
		"serverRegion": strings.ToLower(char.Region),
		"limit":        limit,
	}

	data, err := c.Query(ctx, query, variables)
	if err != nil {
		return nil, err
	}

	var result struct {
		CharacterData struct {
			Character *struct {
				ID            int    `json:"id"`
				Name          string `json:"name"`
				RecentReports struct {
					Data []struct {
						Code      string `json:"code"`
						Title     string `json:"title"`
						StartTime int64  `json:"startTime"`
						Fights    []struct {
							ID            int      `json:"id"`
							Name          string   `json:"name"`
							EncounterID   int      `json:"encounterID"`
							Difficulty    *int     `json:"difficulty"`
							KeystoneLevel *int     `json:"keystoneLevel"`
							KeystoneTime  *int64   `json:"keystoneTime"`
							KeystoneBonus *int     `json:"keystoneBonus"`
							Rating        *float64 `json:"rating"`
							EndTime       int64    `json:"endTime"`
							Kill          *bool    `json:"kill"`
						} `json:"fights"`
					} `json:"data"`
				} `json:"recentReports"`
			} `json:"character"`
		} `json:"characterData"`
	}

	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("warcraftlogs: failed to parse response: %w", err)
	}

	if result.CharacterData.Character == nil {
		return nil, fmt.Errorf("warcraftlogs: character not found: %s-%s", char.Name, char.Realm)
	}

	var runs []MythicPlusRun
	for _, report := range result.CharacterData.Character.RecentReports.Data {
		reportStartTime := time.UnixMilli(report.StartTime)

		for _, fight := range report.Fights {
			// Skip non-M+ fights (keystoneLevel is nil for non-M+)
			if fight.KeystoneLevel == nil || *fight.KeystoneLevel == 0 {
				continue
			}

			run := MythicPlusRun{
				ReportCode:    report.Code,
				FightID:       fight.ID,
				Dungeon:       fight.Name,
				EncounterID:   fight.EncounterID,
				KeystoneLevel: *fight.KeystoneLevel,
				CompletedAt:   reportStartTime.Add(time.Duration(fight.EndTime) * time.Millisecond),
			}

			if fight.KeystoneTime != nil {
				run.KeystoneTime = *fight.KeystoneTime
			}
			if fight.KeystoneBonus != nil {
				run.KeystoneBonus = *fight.KeystoneBonus
			}
			if fight.Rating != nil {
				run.Rating = *fight.Rating
			}
			if fight.Kill != nil {
				run.Kill = *fight.Kill
			}

			runs = append(runs, run)
		}
	}

	return runs, nil
}

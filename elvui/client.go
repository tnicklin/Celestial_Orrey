package elvui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Client fetches ElvUI version information from the TukUI API.
type Client struct {
	apiURL string
	http   *http.Client
}

// NewClient creates a new ElvUI API client.
func NewClient(apiURL string, httpClient *http.Client) *Client {
	return &Client{
		apiURL: apiURL,
		http:   httpClient,
	}
}

// FetchVersion fetches the current ElvUI version from the TukUI API.
func (c *Client) FetchVersion(ctx context.Context) (*VersionInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return nil, fmt.Errorf("elvui: status %d: %s", resp.StatusCode, string(body))
	}

	var info VersionInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}

	return &info, nil
}

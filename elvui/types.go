package elvui

import "context"

// Poller defines the interface for polling ElvUI version updates.
type Poller interface {
	Start(ctx context.Context) error
	Stop()
}

// VersionInfo holds the ElvUI version information from the TukUI API.
type VersionInfo struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	URL          string `json:"url"`
	LastUpdate   string `json:"last_update"`
	ChangelogURL string `json:"changelog_url"`
	WebURL       string `json:"web_url"`
}

// NotifyFunc is called when a new ElvUI version is detected.
type NotifyFunc func(VersionInfo)

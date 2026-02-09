package warcraftlogs

// Config holds WarcraftLogs client configuration.
type Config struct {
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
}

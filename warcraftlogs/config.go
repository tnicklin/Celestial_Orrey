package warcraftlogs

// Config holds WarcraftLogs client configuration.
type Config struct {
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
	GraphQLURL   string `yaml:"graphql_url"`
	TokenURL     string `yaml:"token_url"`
	UserAgent    string `yaml:"user_agent"`
	GuildName    string `yaml:"guild_name"`
	ServerSlug   string `yaml:"server_slug"`
	ServerRegion string `yaml:"server_region"`
	Limit        int    `yaml:"limit"`
}

package discord

// Config holds Discord-specific configuration.
type Config struct {
	Token          string `yaml:"token"`
	GuildID        string `yaml:"guild_id"`
	CommandChannel string `yaml:"command_channel"`
	ReportChannel  string `yaml:"report_channel"`
}

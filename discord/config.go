package discord

// Config holds Discord-specific configuration.
type Config struct {
	Token         string `yaml:"token"`
	GuildID       string `yaml:"guild_id"`
	ListenChannel string `yaml:"listen_channel"`
}

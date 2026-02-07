package logger

// Config holds logger configuration.
type Config struct {
	Level       string   `yaml:"level"`
	OutputPaths []string `yaml:"output_paths"`
}

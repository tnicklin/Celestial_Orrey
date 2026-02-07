package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "test_config.yaml")

	tests := []struct {
		name    string
		content string
		wantErr bool
	}{
		{
			name: "valid config",
			content: `
logger:
  level: debug
  output_paths:
    - stdout
discord:
  token: "test-token"
  guild_id: "123456"
  command_channel: "commands"
  report_channel: "reports"
store:
  path: "test.db"
`,
			wantErr: false,
		},
		{
			name:    "empty config",
			content: "",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(configPath, []byte(tt.content), 0644); err != nil {
				t.Fatalf("Failed to write config file: %v", err)
			}

			cfg, err := Load(configPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("Load() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && cfg == nil {
				t.Error("Load() returned nil config without error")
			}
		})
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load("nonexistent.yaml")
	if err == nil {
		t.Error("Load() should return error for missing file")
	}
}

func TestLoadWithDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "test_config.yaml")

	tests := []struct {
		name           string
		content        string
		wantLogLevel   string
		wantRIOBaseURL string
		wantStorePath  string
	}{
		{
			name:           "applies defaults when values missing",
			content:        "logger:\n  level: \"\"\n",
			wantLogLevel:   "info",
			wantRIOBaseURL: "https://raider.io",
			wantStorePath:  "data/celestial_orrey.db",
		},
		{
			name:           "respects provided values",
			content:        "logger:\n  level: debug\nstore:\n  path: custom.db\n",
			wantLogLevel:   "debug",
			wantRIOBaseURL: "https://raider.io",
			wantStorePath:  "custom.db",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(configPath, []byte(tt.content), 0644); err != nil {
				t.Fatalf("Failed to write config file: %v", err)
			}

			cfg, err := LoadWithDefaults(configPath)
			if err != nil {
				t.Fatalf("LoadWithDefaults() error = %v", err)
			}

			if cfg.Logger.Level != tt.wantLogLevel {
				t.Errorf("Logger.Level = %q, want %q", cfg.Logger.Level, tt.wantLogLevel)
			}
			if cfg.RaiderIO.BaseURL != tt.wantRIOBaseURL {
				t.Errorf("RaiderIO.BaseURL = %q, want %q", cfg.RaiderIO.BaseURL, tt.wantRIOBaseURL)
			}
			if cfg.Store.Path != tt.wantStorePath {
				t.Errorf("Store.Path = %q, want %q", cfg.Store.Path, tt.wantStorePath)
			}
		})
	}
}

func TestLoadWithDefaults_WarcraftLogsDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "test_config.yaml")

	content := "logger:\n  level: info\n"
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	cfg, err := LoadWithDefaults(configPath)
	if err != nil {
		t.Fatalf("LoadWithDefaults() error = %v", err)
	}

	expectedGraphQLURL := "https://www.warcraftlogs.com/api/v2/client"
	if cfg.WarcraftLogs.GraphQLURL != expectedGraphQLURL {
		t.Errorf("WarcraftLogs.GraphQLURL = %q, want %q", cfg.WarcraftLogs.GraphQLURL, expectedGraphQLURL)
	}

	expectedTokenURL := "https://www.warcraftlogs.com/oauth/token"
	if cfg.WarcraftLogs.TokenURL != expectedTokenURL {
		t.Errorf("WarcraftLogs.TokenURL = %q, want %q", cfg.WarcraftLogs.TokenURL, expectedTokenURL)
	}

	expectedLimit := 50
	if cfg.WarcraftLogs.Limit != expectedLimit {
		t.Errorf("WarcraftLogs.Limit = %d, want %d", cfg.WarcraftLogs.Limit, expectedLimit)
	}
}

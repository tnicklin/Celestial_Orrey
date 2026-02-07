package logger

import (
	"testing"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{
			name: "default config",
			config: Config{
				Level:       "info",
				OutputPaths: []string{"stdout"},
			},
			wantErr: false,
		},
		{
			name: "debug level",
			config: Config{
				Level:       "debug",
				OutputPaths: []string{"stdout"},
			},
			wantErr: false,
		},
		{
			name: "warn level",
			config: Config{
				Level:       "warn",
				OutputPaths: []string{"stdout"},
			},
			wantErr: false,
		},
		{
			name: "error level",
			config: Config{
				Level:       "error",
				OutputPaths: []string{"stdout"},
			},
			wantErr: false,
		},
		{
			name: "invalid level falls back to info",
			config: Config{
				Level:       "invalid",
				OutputPaths: []string{"stdout"},
			},
			wantErr: false,
		},
		{
			name: "empty output paths",
			config: Config{
				Level:       "info",
				OutputPaths: []string{},
			},
			wantErr: false,
		},
		{
			name: "multiple output paths",
			config: Config{
				Level:       "info",
				OutputPaths: []string{"stdout", "stderr"},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, err := New(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && logger == nil {
				t.Error("New() returned nil logger without error")
			}
		})
	}
}

func TestNewNop(t *testing.T) {
	logger := NewNop()
	if logger == nil {
		t.Error("NewNop() returned nil")
		return
	}

	// Verify it doesn't panic when called
	logger.InfoW("test message", "key", "value")
	logger.WarnW("test warning", "key", "value")
	logger.ErrorW("test error", "key", "value")

	if err := logger.Sync(); err != nil {
		t.Errorf("Sync() should not error on nop logger: %v", err)
	}
}

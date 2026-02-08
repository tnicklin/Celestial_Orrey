package warcraftlogs

import (
	"testing"
	"time"

	"github.com/tnicklin/celestial_orrey/models"
)

func TestNormalizeName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "lowercase letters only",
			input: "hello",
			want:  "hello",
		},
		{
			name:  "mixed case",
			input: "HelloWorld",
			want:  "helloworld",
		},
		{
			name:  "with spaces",
			input: "Hello World",
			want:  "helloworld",
		},
		{
			name:  "with numbers",
			input: "Test123",
			want:  "test123",
		},
		{
			name:  "with special characters",
			input: "Mists of Tirna Scithe",
			want:  "mistsoftirnascithe",
		},
		{
			name:  "with apostrophes",
			input: "Ara-Kara, City of Echoes",
			want:  "arakaracityofechoes",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "only special characters",
			input: "!@#$%",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeName(tt.input)
			if got != tt.want {
				t.Errorf("normalizeName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDefaultDungeonMatch(t *testing.T) {
	tests := []struct {
		name    string
		dungeon string
		zone    string
		want    bool
	}{
		{
			name:    "exact match",
			dungeon: "Mists of Tirna Scithe",
			zone:    "Mists of Tirna Scithe",
			want:    true,
		},
		{
			name:    "case insensitive",
			dungeon: "MISTS OF TIRNA SCITHE",
			zone:    "mists of tirna scithe",
			want:    true,
		},
		{
			name:    "partial match - zone contains dungeon",
			dungeon: "Mists",
			zone:    "Mists of Tirna Scithe",
			want:    true,
		},
		{
			name:    "partial match - dungeon contains zone",
			dungeon: "The Dawnbreaker",
			zone:    "Dawnbreaker",
			want:    true,
		},
		{
			name:    "no match",
			dungeon: "Mists of Tirna Scithe",
			zone:    "The Necrotic Wake",
			want:    false,
		},
		{
			name:    "empty dungeon",
			dungeon: "",
			zone:    "Mists",
			want:    false,
		},
		{
			name:    "empty zone",
			dungeon: "Mists",
			zone:    "",
			want:    false,
		},
		{
			name:    "both empty",
			dungeon: "",
			zone:    "",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := defaultDungeonMatch(tt.dungeon, tt.zone)
			if got != tt.want {
				t.Errorf("defaultDungeonMatch(%q, %q) = %v, want %v", tt.dungeon, tt.zone, got, tt.want)
			}
		})
	}
}

func TestCalculateMatchConfidence(t *testing.T) {
	tests := []struct {
		name     string
		key      models.CompletedKey
		run      *MythicPlusRun
		timeDiff time.Duration
		window   time.Duration
		wantMin  float64
		wantMax  float64
	}{
		{
			name: "perfect match - same time and run time",
			key: models.CompletedKey{
				KeyLevel:  10,
				RunTimeMS: 1000000,
			},
			run: &MythicPlusRun{
				KeystoneLevel: 10,
				KeystoneTime:  1000000,
			},
			timeDiff: 0,
			window:   6 * time.Hour,
			wantMin:  0.95,
			wantMax:  1.0,
		},
		{
			name: "good match - close time",
			key: models.CompletedKey{
				KeyLevel:  10,
				RunTimeMS: 1000000,
			},
			run: &MythicPlusRun{
				KeystoneLevel: 10,
				KeystoneTime:  1000000,
			},
			timeDiff: 1 * time.Hour,
			window:   6 * time.Hour,
			wantMin:  0.7,
			wantMax:  1.0,
		},
		{
			name: "match with different run times",
			key: models.CompletedKey{
				KeyLevel:  10,
				RunTimeMS: 1000000,
			},
			run: &MythicPlusRun{
				KeystoneLevel: 10,
				KeystoneTime:  1010000, // 10 seconds difference
			},
			timeDiff: 0,
			window:   6 * time.Hour,
			wantMin:  0.5,
			wantMax:  0.7,
		},
		{
			name: "far time difference",
			key: models.CompletedKey{
				KeyLevel:  10,
				RunTimeMS: 1000000,
			},
			run: &MythicPlusRun{
				KeystoneLevel: 10,
				KeystoneTime:  1000000,
			},
			timeDiff: 5 * time.Hour,
			window:   6 * time.Hour,
			wantMin:  0.5,
			wantMax:  0.8,
		},
		{
			name: "no run time data",
			key: models.CompletedKey{
				KeyLevel:  10,
				RunTimeMS: 0,
			},
			run: &MythicPlusRun{
				KeystoneLevel: 10,
				KeystoneTime:  0,
			},
			timeDiff: 0,
			window:   6 * time.Hour,
			wantMin:  0.55,
			wantMax:  0.65,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateMatchConfidence(tt.key, tt.run, tt.timeDiff, tt.window)
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("calculateMatchConfidence() = %v, want between %v and %v", got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestMinMaxCompletedAt(t *testing.T) {
	tests := []struct {
		name    string
		keys    []models.CompletedKey
		wantMin time.Time
		wantMax time.Time
	}{
		{
			name: "single key",
			keys: []models.CompletedKey{
				{CompletedAt: "2026-02-01T12:00:00Z"},
			},
			wantMin: time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC),
			wantMax: time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC),
		},
		{
			name: "multiple keys",
			keys: []models.CompletedKey{
				{CompletedAt: "2026-02-01T12:00:00Z"},
				{CompletedAt: "2026-02-01T10:00:00Z"},
				{CompletedAt: "2026-02-01T14:00:00Z"},
			},
			wantMin: time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC),
			wantMax: time.Date(2026, 2, 1, 14, 0, 0, 0, time.UTC),
		},
		{
			name:    "empty keys",
			keys:    []models.CompletedKey{},
			wantMin: time.Time{},
			wantMax: time.Time{},
		},
		{
			name: "keys with invalid timestamps",
			keys: []models.CompletedKey{
				{CompletedAt: "invalid"},
				{CompletedAt: "2026-02-01T12:00:00Z"},
			},
			wantMin: time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC),
			wantMax: time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMin, gotMax := minMaxCompletedAt(tt.keys)
			if !gotMin.Equal(tt.wantMin) {
				t.Errorf("minMaxCompletedAt() min = %v, want %v", gotMin, tt.wantMin)
			}
			if !gotMax.Equal(tt.wantMax) {
				t.Errorf("minMaxCompletedAt() max = %v, want %v", gotMax, tt.wantMax)
			}
		})
	}
}

func TestNewLinker(t *testing.T) {
	linker := NewLinker(LinkerParams{})

	// MatchWindow is now dynamic based on weekly reset, just verify it's reasonable
	if linker.MatchWindow < 24*time.Hour {
		t.Errorf("MatchWindow = %v, expected at least 24 hours", linker.MatchWindow)
	}
	if linker.PreBuffer != 15*time.Minute {
		t.Errorf("PreBuffer = %v, want %v", linker.PreBuffer, 15*time.Minute)
	}
	if linker.PostBuffer != 30*time.Minute {
		t.Errorf("PostBuffer = %v, want %v", linker.PostBuffer, 30*time.Minute)
	}
}

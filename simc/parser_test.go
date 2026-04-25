package simc

import (
	"strings"
	"testing"
)

func TestValidateProfile(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "empty",
			input:   "",
			wantErr: true,
		},
		{
			name:    "garbage",
			input:   "this is not a simc profile, just some words",
			wantErr: true,
		},
		{
			name:    "class declaration",
			input:   `mage="Askr"` + "\nlevel=80\n",
			wantErr: false,
		},
		{
			name:    "level only",
			input:   "level=80\n",
			wantErr: false,
		},
		{
			name:    "comments then class",
			input:   "# Askr - Frost - 2026\n\nmage=\"Askr\"\nlevel=80\n",
			wantErr: false,
		},
		{
			name:    "spec only",
			input:   "spec=marksmanship\n",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateProfile([]byte(tt.input))
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestParseFightStyle(t *testing.T) {
	tests := []struct {
		input string
		want  FightStyle
		ok    bool
	}{
		{"", FightStylePatchwerk, true},
		{"patchwerk", FightStylePatchwerk, true},
		{"PW", FightStylePatchwerk, true},
		{"dungeon_slice", FightStyleDungeonSlice, true},
		{"DungeonSlice", FightStyleDungeonSlice, true},
		{"ds", FightStyleDungeonSlice, true},
		{"unknown", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, ok := ParseFightStyle(tt.input)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseJSONReport(t *testing.T) {
	const sample = `{
  "version": "1100",
  "build_level": "abc123",
  "sim": {
    "options": { "iterations": 12345, "fight_style": "Patchwerk" },
    "players": [
      {
        "name": "Askr",
        "collected_data": {
          "dps": {
            "mean": 1840329.5,
            "min": 1700000,
            "max": 1900000,
            "std_dev": 50000,
            "mean_std_dev": 1500
          }
        }
      }
    ]
  }
}`
	dir := t.TempDir()
	path := dir + "/out.json"
	if err := writeFile(path, sample); err != nil {
		t.Fatal(err)
	}
	res, err := ParseJSONReport(path)
	if err != nil {
		t.Fatal(err)
	}
	if res.PlayerName != "Askr" {
		t.Errorf("player = %q", res.PlayerName)
	}
	if res.DPS != 1840329.5 {
		t.Errorf("dps = %v", res.DPS)
	}
	if res.Iterations != 12345 {
		t.Errorf("iterations = %d", res.Iterations)
	}
	if !strings.Contains(res.SimVersion, "1100") {
		t.Errorf("version = %q", res.SimVersion)
	}
}

func writeFile(path, content string) error {
	return writeBytes(path, []byte(content))
}

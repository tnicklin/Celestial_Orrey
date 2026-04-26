package simc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteInput_PerStyleSpec(t *testing.T) {
	dir := t.TempDir()
	r := &Runner{cfg: Config{Threads: 4, DefaultIterations: 1000, MaxIterations: 50000}}

	cases := []struct {
		name        string
		req         SimRequest
		wantStyle   string
		wantMaxTime string
		wantTargets string
	}{
		{
			name:        "patchwerk",
			req:         SimRequest{Profile: []byte("hunter=\"X\"\n"), FightStyle: FightStylePatchwerk},
			wantStyle:   "fight_style=Patchwerk",
			wantMaxTime: "max_time=300",
		},
		{
			name:        "patchwerk_5t",
			req:         SimRequest{Profile: []byte("hunter=\"X\"\n"), FightStyle: FightStylePatchwerk5T},
			wantStyle:   "fight_style=Patchwerk",
			wantMaxTime: "max_time=300",
			wantTargets: "desired_targets=5",
		},
		{
			name:        "dungeon_slice",
			req:         SimRequest{Profile: []byte("hunter=\"X\"\n"), FightStyle: FightStyleDungeonSlice},
			wantStyle:   "fight_style=DungeonSlice",
			wantMaxTime: "max_time=420",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := filepath.Join(dir, tc.name+".simc")
			jsonPath := filepath.Join(dir, tc.name+".json")
			if err := r.writeInput(input, jsonPath, tc.req); err != nil {
				t.Fatalf("writeInput: %v", err)
			}
			body, err := os.ReadFile(input)
			if err != nil {
				t.Fatal(err)
			}
			s := string(body)
			if !strings.Contains(s, tc.wantStyle) {
				t.Errorf("missing %q in:\n%s", tc.wantStyle, s)
			}
			if !strings.Contains(s, tc.wantMaxTime) {
				t.Errorf("missing %q in:\n%s", tc.wantMaxTime, s)
			}
			if tc.wantTargets != "" && !strings.Contains(s, tc.wantTargets) {
				t.Errorf("missing %q in:\n%s", tc.wantTargets, s)
			}
			if tc.wantTargets == "" && strings.Contains(s, "desired_targets=") {
				t.Errorf("unexpected desired_targets in:\n%s", s)
			}
		})
	}
}

func TestWriteInput_RequestOverridesSpec(t *testing.T) {
	dir := t.TempDir()
	r := &Runner{cfg: Config{Threads: 4, DefaultIterations: 1000, MaxIterations: 50000}}

	req := SimRequest{
		Profile:        []byte("hunter=\"X\"\n"),
		FightStyle:     FightStylePatchwerk, // spec says max_time=300
		MaxTimeSec:     600,                 // override to 10min
		DesiredTargets: 3,                   // ad-hoc 3T
	}
	input := filepath.Join(dir, "x.simc")
	if err := r.writeInput(input, filepath.Join(dir, "x.json"), req); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(input)
	s := string(body)
	if !strings.Contains(s, "max_time=600") {
		t.Errorf("override max_time not honored:\n%s", s)
	}
	if !strings.Contains(s, "desired_targets=3") {
		t.Errorf("override desired_targets not honored:\n%s", s)
	}
}

func TestFightStyleSpecFor_Defaults(t *testing.T) {
	cases := []struct {
		fs            FightStyle
		wantStyle     string
		wantMaxTime   int
		wantTargets   int
	}{
		{FightStylePatchwerk, "Patchwerk", 300, 0},
		{FightStylePatchwerk5T, "Patchwerk", 300, 5},
		{FightStyleDungeonSlice, "DungeonSlice", 420, 0},
	}
	for _, tc := range cases {
		spec := FightStyleSpecFor(tc.fs)
		if spec.SimcStyle != tc.wantStyle {
			t.Errorf("%s SimcStyle = %q, want %q", tc.fs, spec.SimcStyle, tc.wantStyle)
		}
		if spec.MaxTimeSec != tc.wantMaxTime {
			t.Errorf("%s MaxTimeSec = %d, want %d", tc.fs, spec.MaxTimeSec, tc.wantMaxTime)
		}
		if spec.DesiredTargets != tc.wantTargets {
			t.Errorf("%s DesiredTargets = %d, want %d", tc.fs, spec.DesiredTargets, tc.wantTargets)
		}
	}
}

func TestParseFightStyle_5T(t *testing.T) {
	cases := map[string]FightStyle{
		"5t":           FightStylePatchwerk5T,
		"PW5T":         FightStylePatchwerk5T,
		"patchwerk5t":  FightStylePatchwerk5T,
		"5_target":     FightStylePatchwerk5T,
		"patchwerk":    FightStylePatchwerk,
		"ds":           FightStyleDungeonSlice,
	}
	for in, want := range cases {
		got, ok := ParseFightStyle(in)
		if !ok || got != want {
			t.Errorf("ParseFightStyle(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

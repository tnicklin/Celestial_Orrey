package simc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// classMarkers are the canonical class declaration prefixes that begin a
// SimulationCraft profile (e.g. `mage="Askr"`).
var classMarkers = []string{
	"deathknight", "demonhunter", "druid", "evoker", "hunter", "mage",
	"monk", "paladin", "priest", "rogue", "shaman", "warlock", "warrior",
}

// auxMarkers are non-class settings commonly emitted by the addon. A profile
// that has none of these is almost certainly not a simc dump.
var auxMarkers = []string{
	"level=", "race=", "spec=", "class=", "talents=", "professions=", "role=",
}

// ValidateProfile returns nil when the bytes look like a SimulationCraft
// profile. It is intentionally lenient — a single class or aux marker in the
// first ~50 lines is enough.
func ValidateProfile(b []byte) error {
	if len(b) == 0 {
		return errors.New("profile is empty")
	}
	scanner := bufio.NewScanner(bytes.NewReader(b))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	scanned := 0
	for scanner.Scan() && scanned < 80 {
		scanned++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lower := strings.ToLower(line)
		for _, m := range classMarkers {
			if strings.HasPrefix(lower, m+"=") {
				return nil
			}
		}
		for _, m := range auxMarkers {
			if strings.HasPrefix(lower, m) {
				return nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan profile: %w", err)
	}
	return errors.New("profile does not look like a SimulationCraft dump (no class/level/spec markers found)")
}

// jsonOutput is a partial schema covering the json2 output we care about.
type jsonOutput struct {
	Version    string `json:"version"`
	BuildLevel string `json:"build_level"`
	Sim        struct {
		Options struct {
			Iterations int    `json:"iterations"`
			FightStyle string `json:"fight_style"`
		} `json:"options"`
		Players []struct {
			Name          string `json:"name"`
			CollectedData struct {
				DPS struct {
					Mean        float64 `json:"mean"`
					Min         float64 `json:"min"`
					Max         float64 `json:"max"`
					StdDev      float64 `json:"std_dev"`
					MeanStdDev  float64 `json:"mean_std_dev"`
				} `json:"dps"`
			} `json:"collected_data"`
		} `json:"players"`
	} `json:"sim"`
}

// ParseJSONReport extracts the headline metrics from a simc json2 report.
func ParseJSONReport(path string) (SimResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SimResult{}, fmt.Errorf("read report: %w", err)
	}
	var out jsonOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return SimResult{}, fmt.Errorf("decode report: %w", err)
	}
	if len(out.Sim.Players) == 0 {
		return SimResult{}, errors.New("simc report contains no players")
	}
	p := out.Sim.Players[0]
	return SimResult{
		DPS:        p.CollectedData.DPS.Mean,
		DPSError:   p.CollectedData.DPS.MeanStdDev,
		DPSMin:     p.CollectedData.DPS.Min,
		DPSMax:     p.CollectedData.DPS.Max,
		Iterations: out.Sim.Options.Iterations,
		PlayerName: p.Name,
		SimVersion: strings.TrimSpace(out.Version + " " + out.BuildLevel),
		JSONPath:   path,
	}, nil
}

package simc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WriteReport persists a Report as both a structured JSON artifact and
// a short markdown summary alongside it. Returns the path of the JSON
// file. The dir is created if missing.
//
// Layout:
//   <dir>/sim-<id>.json   — full structured payload (inline base64 profiles)
//   <dir>/sim-<id>.md     — terse human-readable summary the agent can skim
//
// Both files are written atomically (write-then-rename) so a partial
// write never corrupts a previous report at the same path.
func WriteReport(dir string, r Report) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %q: %w", dir, err)
	}

	jsonPath := filepath.Join(dir, fmt.Sprintf("sim-%d.json", r.Run.ID))
	mdPath := filepath.Join(dir, fmt.Sprintf("sim-%d.md", r.Run.ID))

	jsonBytes, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal report: %w", err)
	}
	if err := writeFileAtomic(jsonPath, jsonBytes, 0o644); err != nil {
		return "", err
	}
	if err := writeFileAtomic(mdPath, []byte(renderReportMarkdown(r)), 0o644); err != nil {
		return jsonPath, fmt.Errorf("write markdown: %w", err)
	}
	return jsonPath, nil
}

// writeFileAtomic writes data to path via a sibling tempfile + rename.
// Avoids leaving half-written reports if the process dies mid-write.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*-"+filepath.Base(path))
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName) // no-op if rename succeeded
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// renderReportMarkdown produces a one-page summary the agent can read
// without parsing the JSON. Numbers match the Discord embed where
// possible so a human reading both can cross-check.
func renderReportMarkdown(r Report) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Sim #%d — %s\n\n", r.Run.ID, displayCharacter(r))
	fmt.Fprintf(&sb, "- Class/Spec: %s %s\n", r.Run.Spec, r.Run.Class)
	if len(r.Run.StatPriority) > 0 {
		fmt.Fprintf(&sb, "- Stat priority: %s\n", strings.Join(r.Run.StatPriority, " > "))
	}
	fmt.Fprintf(&sb, "- Duration: %.0fs · sims: %d\n\n", r.Run.DurationSeconds, r.Totals.SimsRun)

	for _, fs := range []FightStyle{FightStylePatchwerk, FightStyleDungeonSlice} {
		fr, ok := r.FightStyles[fs]
		if !ok {
			continue
		}
		fmt.Fprintf(&sb, "## %s\n\n", fs)
		fmt.Fprintf(&sb, "- Baseline: %.0f DPS · Best: %.0f DPS · Δ: %+0.0f (%+.2f%%)\n",
			fr.BaselineDPS, fr.BestDPS, fr.DeltaDPS, fr.DeltaPct)
		fmt.Fprintf(&sb, "- Noise floor (target_error %.2f%%): %.0f DPS\n", r.Config.RankTargetError, fr.NoiseFloorDPS)
		if len(fr.CrossProduct.FlippedFromGreedy) > 0 {
			fmt.Fprintf(&sb, "- Cross-product flipped: %s\n", strings.Join(fr.CrossProduct.FlippedFromGreedy, ", "))
		}
		if indet := indeterminateSlotsBelowNoise(fr); len(indet) > 0 {
			fmt.Fprintf(&sb, "- Slots below noise after greedy: %s\n", strings.Join(indet, ", "))
		}
		if msg := constraintsSummary(fr); msg != "" {
			fmt.Fprintf(&sb, "- Constraints applied: %s\n", msg)
		}
		fmt.Fprintf(&sb, "\n")
	}
	return sb.String()
}

func displayCharacter(r Report) string {
	if r.Run.Character != "" {
		if r.Run.Realm != "" {
			return r.Run.Character + "-" + r.Run.Realm
		}
		return r.Run.Character
	}
	return r.Run.Requester
}

// indeterminateSlotsBelowNoise returns slot names whose top-1/top-2
// gap is below the indeterminate threshold — the agent uses these to
// recommend bumping iterations.
func indeterminateSlotsBelowNoise(fr FightStyleReport) []string {
	var out []string
	for _, p := range fr.Greedy.SlotPicks {
		if p.Indeterminate {
			out = append(out, fmt.Sprintf("%s (%.3f%%)", p.Slot, p.GapPct))
		}
	}
	return out
}

// constraintsSummary describes which optimizer constraints actually
// fired — currently just the mainstat-uniqueness rule.
func constraintsSummary(fr FightStyleReport) string {
	count := 0
	for _, item := range fr.GemPhase.Items {
		count += len(item.ExcludedByConstraint)
	}
	if count == 0 {
		return ""
	}
	return fmt.Sprintf("mainstat uniqueness excluded %d gem(s)", count)
}

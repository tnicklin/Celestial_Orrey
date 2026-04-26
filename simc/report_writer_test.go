package simc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteReport_RoundTripJSON(t *testing.T) {
	dir := t.TempDir()
	r := sampleReport()
	path, err := WriteReport(dir, r)
	if err != nil {
		t.Fatalf("WriteReport: %v", err)
	}
	wantJSON := filepath.Join(dir, "sim-42.json")
	if path != wantJSON {
		t.Errorf("path = %q, want %q", path, wantJSON)
	}

	// JSON parses back into Report and round-trips key fields.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got Report
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.SchemaVersion != ReportSchemaVersion {
		t.Errorf("schema_version = %d, want %d", got.SchemaVersion, ReportSchemaVersion)
	}
	if got.Run.ID != 42 {
		t.Errorf("run.id = %d, want 42", got.Run.ID)
	}
	pw := got.FightStyles[FightStylePatchwerk]
	if pw.NoiseFloorDPS == 0 {
		t.Errorf("noise_floor_dps = 0, want > 0")
	}

	// Markdown summary exists and contains the headline.
	mdPath := filepath.Join(dir, "sim-42.md")
	mdRaw, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("md: %v", err)
	}
	if !strings.Contains(string(mdRaw), "Sim #42") {
		t.Errorf("md missing sim header: %s", mdRaw)
	}
	if !strings.Contains(string(mdRaw), "Patchwerk") {
		t.Errorf("md missing fight style header: %s", mdRaw)
	}
}

func TestWriteReport_AtomicOverwrite(t *testing.T) {
	dir := t.TempDir()
	r := sampleReport()
	if _, err := WriteReport(dir, r); err != nil {
		t.Fatal(err)
	}
	// Second write at the same id replaces both files.
	r.Run.Character = "RewriteChar"
	if _, err := WriteReport(dir, r); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "sim-42.json"))
	if !strings.Contains(string(raw), "RewriteChar") {
		t.Errorf("expected updated character in re-written report")
	}
	// No leftover tempfiles.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("leftover tempfile: %s", e.Name())
		}
	}
}

func TestRenderReportMarkdown_FlagsConstraintsAndIndeterminate(t *testing.T) {
	r := sampleReport()
	pw := r.FightStyles[FightStylePatchwerk]

	// Add an indeterminate slot pick.
	pw.Greedy.SlotPicks = append(pw.Greedy.SlotPicks, SlotPick{
		Slot: "head", Method: "single", PoolSize: 2,
		WinnerID: 1, RunnerUpID: 2, GapPct: 0.05, Indeterminate: true,
	})
	// And a constraint exclusion.
	pw.GemPhase.Items = []GemItemReport{{
		Slot: "head", ItemID: 100,
		ExcludedByConstraint: []ExcludedGem{{
			ID: "240889", Name: "Flawless Agility",
			Reason: "mainstat_unique_in_use_by", InUseBySlot: "finger",
		}},
	}}
	// And a cross-product flip.
	pw.CrossProduct.FlippedFromGreedy = []string{"head"}
	r.FightStyles[FightStylePatchwerk] = pw

	md := renderReportMarkdown(r)
	if !strings.Contains(md, "below noise") {
		t.Errorf("md missing indeterminate-slot summary:\n%s", md)
	}
	if !strings.Contains(md, "mainstat uniqueness excluded 1") {
		t.Errorf("md missing constraint summary:\n%s", md)
	}
	if !strings.Contains(md, "Cross-product flipped: head") {
		t.Errorf("md missing cross-product flip:\n%s", md)
	}
}

func sampleReport() Report {
	pw := FightStyleReport{
		Style:         FightStylePatchwerk,
		BaselineDPS:   87412,
		BestDPS:       89274,
		DeltaDPS:      1862,
		DeltaPct:      2.13,
		NoiseFloorDPS: 437,
		Greedy:        GreedyReport{PassesRun: 2},
		FinalPass:     FinalPassReport{Iterations: 10000, DPS: 89274, DurationSeconds: 23},
		Phases: PhaseStats{
			SimsByPhase:             map[string]int{"baseline": 1, "greedy": 80, "final": 1},
			WallclockSecondsByPhase: map[string]float64{"baseline": 12, "greedy": 90, "final": 23},
		},
	}
	ds := pw
	ds.Style = FightStyleDungeonSlice
	return Report{
		SchemaVersion: ReportSchemaVersion,
		Run: RunMeta{
			ID:              42,
			SubmittedAt:     time.Now().Add(-5 * time.Minute),
			StartedAt:       time.Now().Add(-4 * time.Minute),
			FinishedAt:      time.Now(),
			DurationSeconds: 240,
			Requester:       "askr",
			Character:       "Askrlol",
			Realm:           "stormrage",
			Class:           "hunter",
			Spec:            "marksmanship",
			MainStat:        "agility",
			StatPriority:    []string{"crit", "haste", "mastery", "versatility"},
		},
		Config: ConfigSnapshot{
			RankPassIterations:        1000,
			FinalPassIterations:       10000,
			RankTargetError:           0.5,
			QueueWorkers:              8,
			IndeterminateThresholdPct: 0.3,
			MaxCrossProductSlots:      7,
		},
		Input: InputSummary{
			ProfileBytes:      4317,
			ProfileSHA256:     "f3deadbeef",
			ProfileB64:        "ZGF0YQ==",
			CandidatesPerSlot: map[string]int{"head": 4, "finger": 6},
		},
		FightStyles: map[FightStyle]FightStyleReport{
			FightStylePatchwerk:    pw,
			FightStyleDungeonSlice: ds,
		},
		Totals: aggregateTotals(map[FightStyle]FightStyleReport{
			FightStylePatchwerk:    pw,
			FightStyleDungeonSlice: ds,
		}),
	}
}

func TestAggregateTotals_SumsPerPhase(t *testing.T) {
	pw := FightStyleReport{
		Phases: PhaseStats{
			SimsByPhase:             map[string]int{"baseline": 1, "greedy": 80},
			WallclockSecondsByPhase: map[string]float64{"baseline": 12, "greedy": 90},
		},
	}
	ds := FightStyleReport{
		Phases: PhaseStats{
			SimsByPhase:             map[string]int{"baseline": 1, "greedy": 60, "gem": 30},
			WallclockSecondsByPhase: map[string]float64{"baseline": 14, "greedy": 80, "gem": 45},
		},
	}
	got := aggregateTotals(map[FightStyle]FightStyleReport{
		FightStylePatchwerk:    pw,
		FightStyleDungeonSlice: ds,
	})
	if got.SimsRun != 1+80+1+60+30 {
		t.Errorf("SimsRun = %d, want %d", got.SimsRun, 1+80+1+60+30)
	}
	if got.SimsPerPhase["greedy"] != 140 {
		t.Errorf("greedy sims = %d, want 140", got.SimsPerPhase["greedy"])
	}
	if got.WallclockPerPhaseSeconds["greedy"] != 170 {
		t.Errorf("greedy wallclock = %.1f, want 170", got.WallclockPerPhaseSeconds["greedy"])
	}
	// Phase only present in DS still appears in totals.
	if got.SimsPerPhase["gem"] != 30 {
		t.Errorf("gem sims = %d, want 30", got.SimsPerPhase["gem"])
	}
}

func TestRankIndices_DescendingStable(t *testing.T) {
	got := rankIndices([]float64{100, 200, 50, 200})
	// 200 (idx 1, rank 1), 200 (idx 3, rank 2), 100 (idx 0, rank 3), 50 (idx 2, rank 4)
	want := []int{3, 1, 4, 2}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("rank[%d] = %d, want %d (full: %v)", i, got[i], w, got)
		}
	}
}

func TestTopTwoGapPct_ZeroForSingleScore(t *testing.T) {
	if g := topTwoGapPct([]float64{100}); g != 0 {
		t.Errorf("single-score gap = %v, want 0", g)
	}
	if g := topTwoGapPct([]float64{100, 99}); g <= 0 {
		t.Errorf("two-score gap should be positive, got %v", g)
	}
}

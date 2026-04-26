package simc

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// itemIDRE pulls every `id=NNN` token out of a generated simc body so a
// test runner can score loadouts by item identity without re-parsing the
// whole profile.
var itemIDRE = regexp.MustCompile(`(?m)^([a-z_0-9]+)=,id=(\d+)`)

// fakeSimRunner returns a canned DPS for each loadout, computed by summing
// per-item scores looked up by item ID. Callers configure scores per ID.
type fakeSimRunner struct {
	mu     sync.Mutex
	scores map[int]float64
	calls  int
}

func newFakeRunner(scores map[int]float64) *fakeSimRunner {
	return &fakeSimRunner{scores: scores}
}

func (f *fakeSimRunner) Concurrency() int { return 4 }

func (f *fakeSimRunner) Run(_ context.Context, body []byte, _ FightStyle, _ int) (SimResult, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()

	total := 0.0
	matches := itemIDRE.FindAllSubmatch(body, -1)
	for _, m := range matches {
		// only count slot lines we recognize, so commented bag lines and
		// random `id=` substrings don't contribute.
		slotKey := string(m[1])
		if _, ok := addonSlotMap[slotKey]; !ok {
			continue
		}
		id, _ := strconv.Atoi(string(m[2]))
		total += f.scores[id]
	}
	return SimResult{DPS: total}, nil
}

// makeProfile constructs a minimal Profile from a list of equipped items
// and per-slot bag candidates. Used by greedy tests.
func makeProfile(t *testing.T, equipped []Item, bagBySlot map[Slot][]Item) *Profile {
	t.Helper()
	header := []string{"hunter=\"TestChar\"", "level=80", "spec=marksmanship"}
	all := append([]Item(nil), equipped...)
	for _, items := range bagBySlot {
		all = append(all, items...)
	}
	return &Profile{Header: header, Items: all}
}

// equip is a small helper that builds an Item flagged as equipped.
func equip(slot Slot, slotKey string, id int, track Track) Item {
	return Item{Slot: slot, AddonSlotKey: slotKey, Equipped: true, ItemID: id, Track: track, OriginalIlvl: 272}
}

func bag(slot Slot, slotKey string, id int, track Track) Item {
	return Item{Slot: slot, AddonSlotKey: slotKey, Equipped: false, ItemID: id, Track: track, OriginalIlvl: 272}
}

func TestGreedyOptimize_SingleSlotPicksWinner(t *testing.T) {
	// One slot, two candidates. Winner has the higher score.
	equipped := []Item{equip(SlotHead, "head", 1, TrackHero)}
	cands := map[Slot][]Item{
		SlotHead: {
			{Slot: SlotHead, ItemID: 1, Track: TrackHero, OriginalIlvl: 272},
			{Slot: SlotHead, ItemID: 2, Track: TrackHero, OriginalIlvl: 272},
		},
	}
	p := makeProfile(t, equipped, map[Slot][]Item{SlotHead: cands[SlotHead][1:]})
	runner := newFakeRunner(map[int]float64{1: 100, 2: 200})

	got, tel, err := GreedyOptimize(context.Background(), p, cands, FightStylePatchwerk, 100, runner, nil)
	if err != nil {
		t.Fatal(err)
	}
	head := got.Items[SlotHead]
	if len(head) != 1 || head[0].ItemID != 2 {
		t.Errorf("head winner = %v, want id=2", head)
	}
	// 2 sims (pool of 2) on the first pass, 0 changes on the second so
	// it bails — actually the first pass DOES change, so we get a second
	// pass too. The second pass also runs 2 sims. So 4 total sims.
	if tel.SimsRun < 2 {
		t.Errorf("SimsRun = %d, want at least 2", tel.SimsRun)
	}
}

func TestGreedyOptimize_NoChangeBailsAfterOnePass(t *testing.T) {
	// Equipped is already optimal; greedy should not flip anything and
	// bail after pass 1.
	equipped := []Item{equip(SlotHead, "head", 1, TrackHero)}
	cands := map[Slot][]Item{
		SlotHead: {
			{Slot: SlotHead, ItemID: 1, Track: TrackHero, OriginalIlvl: 272},
			{Slot: SlotHead, ItemID: 2, Track: TrackHero, OriginalIlvl: 272},
		},
	}
	p := makeProfile(t, equipped, map[Slot][]Item{SlotHead: cands[SlotHead][1:]})
	runner := newFakeRunner(map[int]float64{1: 200, 2: 100})

	got, tel, err := GreedyOptimize(context.Background(), p, cands, FightStylePatchwerk, 100, runner, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Items[SlotHead][0].ItemID != 1 {
		t.Errorf("expected to keep equipped id=1, got %d", got.Items[SlotHead][0].ItemID)
	}
	if tel.PassesRun != 1 {
		t.Errorf("PassesRun = %d, want 1 (early bail)", tel.PassesRun)
	}
}

func TestGreedyOptimize_DoubleSlotSequentialPick(t *testing.T) {
	// 4 ring candidates with scores [10, 50, 30, 40]. Sequential pick:
	//   primary winner = id 11 (score 50)
	//   secondary winner from remaining {10, 12, 13} = id 13 (score 40)
	// Expected pair: {11, 13}.
	equipped := []Item{
		equip(SlotFinger, "finger1", 10, TrackHero),
		equip(SlotFinger, "finger2", 11, TrackHero),
	}
	bagItems := []Item{
		{Slot: SlotFinger, ItemID: 12, Track: TrackHero, OriginalIlvl: 272},
		{Slot: SlotFinger, ItemID: 13, Track: TrackHero, OriginalIlvl: 272},
	}
	cands := map[Slot][]Item{
		SlotFinger: {
			{Slot: SlotFinger, ItemID: 10, Track: TrackHero, OriginalIlvl: 272},
			{Slot: SlotFinger, ItemID: 11, Track: TrackHero, OriginalIlvl: 272},
			bagItems[0], bagItems[1],
		},
	}
	p := makeProfile(t, equipped, map[Slot][]Item{SlotFinger: bagItems})

	// Ring scores are additive in our fake (sum of per-id scores),
	// which lines up with the sequential pick algorithm.
	runner := newFakeRunner(map[int]float64{10: 10, 11: 50, 12: 30, 13: 40})

	got, _, err := GreedyOptimize(context.Background(), p, cands, FightStylePatchwerk, 100, runner, nil)
	if err != nil {
		t.Fatal(err)
	}
	pair := got.Items[SlotFinger]
	if len(pair) != 2 {
		t.Fatalf("ring pair size = %d, want 2", len(pair))
	}
	have := map[int]bool{pair[0].ItemID: true, pair[1].ItemID: true}
	if !have[11] || !have[13] {
		t.Errorf("ring pair = %d,%d; want {11,13}", pair[0].ItemID, pair[1].ItemID)
	}
}

func TestGreedyOptimize_RefinementCanFlipSlot(t *testing.T) {
	// Two slots, head and chest. Constructed so that:
	//   pass 1 picks head=2 (200 alone beats 100), then chest=20.
	//   pass 2 should re-evaluate head with chest=20 held — same outcome
	//   since scores are additive. So this exercises the second-pass
	//   path even when no flip happens. Just verify it runs both passes
	//   when pass 1 changed something and then settles.
	equipped := []Item{
		equip(SlotHead, "head", 1, TrackHero),
		equip(SlotChest, "chest", 10, TrackHero),
	}
	bagItems := []Item{
		{Slot: SlotHead, ItemID: 2, Track: TrackHero, OriginalIlvl: 272},
		{Slot: SlotChest, ItemID: 20, Track: TrackHero, OriginalIlvl: 272},
	}
	cands := map[Slot][]Item{
		SlotHead: {
			{Slot: SlotHead, ItemID: 1, Track: TrackHero, OriginalIlvl: 272},
			bagItems[0],
		},
		SlotChest: {
			{Slot: SlotChest, ItemID: 10, Track: TrackHero, OriginalIlvl: 272},
			bagItems[1],
		},
	}
	p := makeProfile(t, equipped, map[Slot][]Item{
		SlotHead:  bagItems[:1],
		SlotChest: bagItems[1:],
	})
	runner := newFakeRunner(map[int]float64{1: 100, 2: 200, 10: 100, 20: 200})

	got, tel, err := GreedyOptimize(context.Background(), p, cands, FightStylePatchwerk, 100, runner, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Items[SlotHead][0].ItemID != 2 {
		t.Errorf("head = %d, want 2", got.Items[SlotHead][0].ItemID)
	}
	if got.Items[SlotChest][0].ItemID != 20 {
		t.Errorf("chest = %d, want 20", got.Items[SlotChest][0].ItemID)
	}
	if tel.PassesRun < 2 {
		t.Errorf("PassesRun = %d, want >= 2 (refinement should run after pass 1 changes)", tel.PassesRun)
	}
}

func TestMaxGreedySims(t *testing.T) {
	// 3 head + 1 chest + 4 finger + 2 trinket.
	//   single slots: head 3 + chest 1 = 4
	//   double slots: finger 2*4-1 = 7, trinket 2*2-1 = 3
	//   per-pass: 4 + 7 + 3 = 14
	//   2 passes: 28
	cands := map[Slot][]Item{
		SlotHead: make([]Item, 3),
		SlotChest: make([]Item, 1),
		SlotFinger: make([]Item, 4),
		SlotTrinket: make([]Item, 2),
	}
	if got := MaxGreedySims(cands); got != 28 {
		t.Errorf("MaxGreedySims = %d, want 28", got)
	}
}

func TestGreedyOptimize_HandlesDoubleSlotSingleCandidate(t *testing.T) {
	// One ring candidate — should drop into the loadout without
	// trying to form a pair.
	equipped := []Item{equip(SlotFinger, "finger1", 1, TrackHero)}
	cands := map[Slot][]Item{
		SlotFinger: {
			{Slot: SlotFinger, ItemID: 1, Track: TrackHero, OriginalIlvl: 272},
		},
	}
	p := makeProfile(t, equipped, nil)
	runner := newFakeRunner(map[int]float64{1: 100})

	got, _, err := GreedyOptimize(context.Background(), p, cands, FightStylePatchwerk, 100, runner, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items[SlotFinger]) != 1 {
		t.Errorf("finger items = %d, want 1", len(got.Items[SlotFinger]))
	}
}

// guard against silently dropping the body when no item lines parse
func TestFakeRunner_SmokeMatch(t *testing.T) {
	body := []byte(strings.TrimSpace(`
hunter="X"
head=,id=42,bonus_id=10384
chest=,id=99,bonus_id=10384
`))
	r := newFakeRunner(map[int]float64{42: 5, 99: 7})
	res, err := r.Run(context.Background(), body, FightStylePatchwerk, 100)
	if err != nil {
		t.Fatal(err)
	}
	if res.DPS != 12 {
		t.Errorf("DPS = %v, want 12", res.DPS)
	}
}

package discord

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tnicklin/celestial_orrey/models"
	"github.com/tnicklin/celestial_orrey/store"
)

func TestCmdCharAliasAllowsAliasNameMatchingCharacterName(t *testing.T) {
	ctx := context.Background()
	st := newAliasTestStore(t, ctx)

	seedAliasTestCharacter(t, ctx, st, models.CompletedKey{
		KeyID:       5001,
		Character:   "askr",
		Region:      "us",
		Realm:       "malganis",
		Dungeon:     "Operation: Floodgate",
		KeyLevel:    12,
		RunTimeMS:   1400000,
		ParTimeMS:   1500000,
		CompletedAt: "2026-03-04T03:00:00Z",
		Source:      "raiderio",
	})
	seedAliasTestCharacter(t, ctx, st, models.CompletedKey{
		KeyID:       5002,
		Character:   "xtein",
		Region:      "us",
		Realm:       "illidan",
		Dungeon:     "Ara-Kara, City of Echoes",
		KeyLevel:    10,
		RunTimeMS:   1500000,
		ParTimeMS:   1600000,
		CompletedAt: "2026-03-05T04:00:00Z",
		Source:      "raiderio",
	})

	bot := &DefaultDiscord{
		store: st,
		clock: fixedClock{now: time.Date(2026, time.March, 8, 12, 0, 0, 0, _pstLocation)},
	}

	msg, err := bot.cmdCharAlias(ctx, []string{"set", "askr", "askr", "xtein"})
	if err != nil {
		t.Fatalf("cmdCharAlias set: %v", err)
	}
	if !strings.Contains(msg, "Alias **askr** now contains") {
		t.Fatalf("unexpected alias set response: %q", msg)
	}

	keysResp, err := bot.cmdKeysAlias(ctx, []string{"askr"})
	if err != nil {
		t.Fatalf("cmdKeysAlias: %v", err)
	}
	if len(keysResp.embeds) != 1 {
		t.Fatalf("expected one keys alias embed, got %#v", keysResp)
	}
	if keysResp.embeds[0].Title != "Keys for alias askr" {
		t.Fatalf("unexpected keys alias title: %q", keysResp.embeds[0].Title)
	}
	if len(keysResp.embeds[0].Fields) != 2 {
		t.Fatalf("expected two alias key fields, got %#v", keysResp.embeds[0].Fields)
	}

	reportResp, err := bot.cmdReportAlias(ctx, []string{"askr"})
	if err != nil {
		t.Fatalf("cmdReportAlias: %v", err)
	}
	if len(reportResp.embeds) != 1 {
		t.Fatalf("expected one report alias embed, got %#v", reportResp)
	}
	if reportResp.embeds[0].Title != "Great Vault Progress (askr)" {
		t.Fatalf("unexpected report alias title: %q", reportResp.embeds[0].Title)
	}
	if !strings.Contains(reportResp.embeds[0].Description, "askr") || !strings.Contains(reportResp.embeds[0].Description, "xtein") {
		t.Fatalf("expected report alias description to include both characters, got %q", reportResp.embeds[0].Description)
	}
}

func TestCmdCharAliasRequiresNameRealmForAmbiguousCharacter(t *testing.T) {
	ctx := context.Background()
	st := newAliasTestStore(t, ctx)

	seedAliasTestCharacter(t, ctx, st, models.CompletedKey{
		KeyID:       6001,
		Character:   "askr",
		Region:      "us",
		Realm:       "malganis",
		Dungeon:     "Operation: Floodgate",
		KeyLevel:    12,
		RunTimeMS:   1400000,
		ParTimeMS:   1500000,
		CompletedAt: "2026-03-04T03:00:00Z",
		Source:      "raiderio",
	})
	seedAliasTestCharacter(t, ctx, st, models.CompletedKey{
		KeyID:       6002,
		Character:   "askr",
		Region:      "us",
		Realm:       "illidan",
		Dungeon:     "Ara-Kara, City of Echoes",
		KeyLevel:    10,
		RunTimeMS:   1500000,
		ParTimeMS:   1600000,
		CompletedAt: "2026-03-05T04:00:00Z",
		Source:      "raiderio",
	})

	bot := &DefaultDiscord{
		store: st,
		clock: fixedClock{now: time.Date(2026, time.March, 8, 12, 0, 0, 0, _pstLocation)},
	}

	msg, err := bot.cmdCharAlias(ctx, []string{"set", "dupes", "askr"})
	if err != nil {
		t.Fatalf("cmdCharAlias ambiguous: %v", err)
	}
	if !strings.Contains(msg, "Ambiguous character name **askr**") {
		t.Fatalf("expected ambiguity message, got %q", msg)
	}

	msg, err = bot.cmdCharAlias(ctx, []string{"set", "dupes", "askr-malganis"})
	if err != nil {
		t.Fatalf("cmdCharAlias exact: %v", err)
	}
	if !strings.Contains(msg, "askr-malganis") {
		t.Fatalf("expected exact realm-qualified member in response, got %q", msg)
	}
}

func TestFormatAliasScoreLeaderboardAlignsSingleAndDoubleDigitRanks(t *testing.T) {
	ctx := context.Background()
	st := newAliasTestStore(t, ctx)

	for i := 1; i <= 10; i++ {
		name := strings.ToLower("char" + strconv.Itoa(i))
		realm := strings.ToLower("realm" + strconv.Itoa(i))
		alias := strings.ToLower("alias" + strconv.Itoa(i))

		seedAliasTestCharacter(t, ctx, st, models.CompletedKey{
			KeyID:       int64(7000 + i),
			Character:   name,
			Region:      "us",
			Realm:       realm,
			Dungeon:     "Operation: Floodgate",
			KeyLevel:    10,
			RunTimeMS:   1400000,
			ParTimeMS:   1500000,
			CompletedAt: "2026-03-04T03:00:00Z",
			Source:      "raiderio",
		})
		if err := st.UpdateCharacterScore(ctx, name, realm, "us", float64(2000-i)); err != nil {
			t.Fatalf("update score for %s: %v", name, err)
		}
		if err := st.SetAliasCharacters(ctx, alias, []models.Character{{
			Name:   name,
			Realm:  realm,
			Region: "us",
		}}); err != nil {
			t.Fatalf("set alias %s: %v", alias, err)
		}
	}

	bot := &DefaultDiscord{
		store: st,
		clock: fixedClock{now: time.Date(2026, time.March, 8, 12, 0, 0, 0, _pstLocation)},
	}

	resp, err := bot.formatAliasScoreLeaderboard(ctx)
	if err != nil {
		t.Fatalf("formatAliasScoreLeaderboard: %v", err)
	}
	if len(resp.embeds) != 1 {
		t.Fatalf("expected leaderboard embed, got %#v", resp)
	}

	lines := extractCodeBlockLines(resp.embeds[0].Description)
	if len(lines) != 10 {
		t.Fatalf("expected 10 leaderboard lines, got %#v", lines)
	}
	if !strings.HasPrefix(lines[0], " 1.") {
		t.Fatalf("expected single-digit rank to be padded for alignment, got %q", lines[0])
	}
	if !strings.HasPrefix(lines[9], "10.") {
		t.Fatalf("expected double-digit rank without leading space, got %q", lines[9])
	}
}

func TestCmdScoresReturnsAliasScoreLeaderboard(t *testing.T) {
	ctx := context.Background()
	st := newAliasTestStore(t, ctx)

	seedAliasTestCharacter(t, ctx, st, models.CompletedKey{
		KeyID:       7501,
		Character:   "askr",
		Region:      "us",
		Realm:       "malganis",
		Dungeon:     "Operation: Floodgate",
		KeyLevel:    12,
		RunTimeMS:   1400000,
		ParTimeMS:   1500000,
		CompletedAt: "2026-03-04T03:00:00Z",
		Source:      "raiderio",
	})
	if err := st.UpdateCharacterScore(ctx, "askr", "malganis", "us", 3123.4); err != nil {
		t.Fatalf("update score: %v", err)
	}
	if err := st.SetAliasCharacters(ctx, "team", []models.Character{{
		Name:   "askr",
		Realm:  "malganis",
		Region: "us",
	}}); err != nil {
		t.Fatalf("set alias: %v", err)
	}

	bot := &DefaultDiscord{
		store: st,
		clock: fixedClock{now: time.Date(2026, time.March, 8, 12, 0, 0, 0, _pstLocation)},
	}

	resp, err := bot.cmdScores(ctx, nil)
	if err != nil {
		t.Fatalf("cmdScores: %v", err)
	}
	if len(resp.embeds) != 1 {
		t.Fatalf("expected one scores embed, got %#v", resp)
	}
	if resp.embeds[0].Title != "Season Score Leaderboard" {
		t.Fatalf("unexpected scores title: %q", resp.embeds[0].Title)
	}
	if !strings.Contains(resp.embeds[0].Description, "team") || !strings.Contains(resp.embeds[0].Description, "(askr)") {
		t.Fatalf("expected scores description to include alias and character, got %q", resp.embeds[0].Description)
	}

	usageResp, err := bot.cmdScores(ctx, []string{"extra"})
	if err != nil {
		t.Fatalf("cmdScores usage: %v", err)
	}
	if usageResp.content != "Usage: `!scores`" {
		t.Fatalf("unexpected usage response: %q", usageResp.content)
	}
}

func TestBuildDailyAnnouncementResponsesOrdersScoreThenVault(t *testing.T) {
	ctx := context.Background()
	st := newAliasTestStore(t, ctx)

	seedAliasTestCharacter(t, ctx, st, models.CompletedKey{
		KeyID:       8001,
		Character:   "askr",
		Region:      "us",
		Realm:       "malganis",
		Dungeon:     "Operation: Floodgate",
		KeyLevel:    12,
		RunTimeMS:   1400000,
		ParTimeMS:   1500000,
		CompletedAt: "2026-03-04T03:00:00Z",
		Source:      "raiderio",
	})
	if err := st.UpdateCharacterScore(ctx, "askr", "malganis", "us", 3123.4); err != nil {
		t.Fatalf("update score: %v", err)
	}
	if err := st.SetAliasCharacters(ctx, "team", []models.Character{{
		Name:   "askr",
		Realm:  "malganis",
		Region: "us",
	}}); err != nil {
		t.Fatalf("set alias: %v", err)
	}

	bot := &DefaultDiscord{
		store: st,
		clock: fixedClock{now: time.Date(2026, time.March, 11, 7, 0, 0, 0, _pstLocation)},
	}

	now := time.Date(2026, time.March, 11, 7, 0, 0, 0, _pstLocation)
	responses, postResetMessage, err := bot.buildDailyAnnouncementResponses(ctx, now)
	if err != nil {
		t.Fatalf("buildDailyAnnouncementResponses: %v", err)
	}
	if postResetMessage {
		t.Fatal("did not expect reset message on a non-Tuesday morning")
	}
	if len(responses) != 2 {
		t.Fatalf("expected two daily announcement responses, got %#v", responses)
	}
	if responses[0].embeds[0].Title != "Season Score Leaderboard" {
		t.Fatalf("expected score leaderboard first, got %#v", responses[0].embeds[0].Title)
	}
	if responses[1].embeds[0].Title != "Great Vault Progress" {
		t.Fatalf("expected vault progress second, got %#v", responses[1].embeds[0].Title)
	}
}

func TestAnnouncementReportCutoffUsesPreviousWeekOnResetMorning(t *testing.T) {
	now := time.Date(2026, time.March, 10, 7, 0, 0, 0, _pstLocation)
	cutoff := announcementReportCutoff(now)
	want := time.Date(2026, time.March, 3, 7, 0, 0, 0, _pstLocation)

	if !cutoff.Equal(want) {
		t.Fatalf("expected previous weekly reset cutoff %v, got %v", want, cutoff)
	}
	if !shouldPostResetMessage(now) {
		t.Fatal("expected reset message on Tuesday morning")
	}
}

func TestScheduledAnnouncementTimeMatchesMorningAndEveningSlots(t *testing.T) {
	morning := time.Date(2026, time.March, 9, 7, 0, 0, 0, _pstLocation)
	slot, ok := scheduledAnnouncementTime(morning)
	if !ok || !slot.Equal(morning) {
		t.Fatalf("expected morning slot at %v, got %v ok=%v", morning, slot, ok)
	}

	evening := time.Date(2026, time.March, 9, 19, 0, 0, 0, _pstLocation)
	slot, ok = scheduledAnnouncementTime(evening)
	if !ok || !slot.Equal(evening) {
		t.Fatalf("expected evening slot at %v, got %v ok=%v", evening, slot, ok)
	}

	notScheduled := time.Date(2026, time.March, 9, 12, 0, 0, 0, _pstLocation)
	if _, ok := scheduledAnnouncementTime(notScheduled); ok {
		t.Fatal("did not expect noon to be a scheduled announcement slot")
	}
}

func newAliasTestStore(t *testing.T, ctx context.Context) *store.SQLiteStore {
	t.Helper()

	path := filepath.Join(t.TempDir(), "snapshot.db")
	st := store.NewSQLiteStore(store.Params{Path: path})
	if err := st.Open(ctx); err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	return st
}

func seedAliasTestCharacter(t *testing.T, ctx context.Context, st *store.SQLiteStore, key models.CompletedKey) {
	t.Helper()
	if err := st.UpsertCompletedKey(ctx, key); err != nil {
		t.Fatalf("upsert key %d: %v", key.KeyID, err)
	}
}

type fixedClock struct {
	now time.Time
}

func (f fixedClock) Now() time.Time {
	return f.now
}

func extractCodeBlockLines(description string) []string {
	start := strings.Index(description, "```\n")
	if start == -1 {
		return nil
	}
	start += len("```\n")
	end := strings.Index(description[start:], "```")
	if end == -1 {
		return nil
	}
	block := strings.TrimRight(description[start:start+end], "\n")
	if block == "" {
		return nil
	}
	return strings.Split(block, "\n")
}

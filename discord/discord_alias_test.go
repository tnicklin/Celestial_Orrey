package discord

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tnicklin/celestial_orrey/clock"
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
		clock: clock.System(),
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
		clock: clock.System(),
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

package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/tnicklin/celestial_orrey/models"
)

func TestSQLiteStoreUpsertAndQuery(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.db")

	st := NewSQLiteStore(Params{Path: path})
	if err := st.Open(ctx); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	key := models.CompletedKey{
		KeyID:       1234,
		Character:   "Arthas",
		Region:      "us",
		Realm:       "illidan",
		Dungeon:     "Mists of Tirna Scithe",
		KeyLevel:    10,
		RunTimeMS:   1320000,
		ParTimeMS:   1500000,
		CompletedAt: "2026-02-04T01:23:45Z",
		Source:      "raiderio",
	}

	if err := st.UpsertCompletedKey(ctx, key); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	cutoff := time.Date(2026, 2, 3, 9, 0, 0, 0, time.UTC)
	counts, err := st.CountKeysByCharacterSince(ctx, cutoff)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if len(counts) != 1 || counts[0].KeyCount != 1 {
		t.Fatalf("expected 1 count row, got %#v", counts)
	}

	keys, err := st.ListKeysByCharacterSince(ctx, "Arthas", cutoff)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(keys))
	}
	if keys[0].Dungeon != key.Dungeon {
		t.Fatalf("expected dungeon %s, got %s", key.Dungeon, keys[0].Dungeon)
	}

	link := WarcraftLogsLink{
		KeyID:      key.KeyID,
		ReportCode: "ABC123",
		URL:        "https://www.warcraftlogs.com/reports/ABC123",
	}
	if err := st.UpsertWarcraftLogsLink(ctx, link); err != nil {
		t.Fatalf("upsert wcl link: %v", err)
	}
	links, err := st.ListWarcraftLogsLinksForKey(ctx, key.KeyID)
	if err != nil {
		t.Fatalf("list wcl links: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("expected 1 wcl link, got %d", len(links))
	}
	if links[0].ReportCode != link.ReportCode {
		t.Fatalf("expected report code %s, got %s", link.ReportCode, links[0].ReportCode)
	}
}

func TestSQLiteStoreRestoreFromDisk(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.db")

	st := NewSQLiteStore(Params{Path: path})
	if err := st.Open(ctx); err != nil {
		t.Fatalf("open: %v", err)
	}
	key := models.CompletedKey{
		KeyID:       2222,
		Character:   "Jaina",
		Region:      "us",
		Realm:       "stormrage",
		Dungeon:     "The Necrotic Wake",
		KeyLevel:    12,
		RunTimeMS:   1500000,
		ParTimeMS:   1600000,
		CompletedAt: "2026-02-04T02:00:00Z",
		Source:      "raiderio",
	}
	if err := st.UpsertCompletedKey(ctx, key); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Use Shutdown to ensure data is flushed to disk
	if err := st.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	restored := NewSQLiteStore(Params{Path: path})
	if err := restored.Open(ctx); err != nil {
		t.Fatalf("open restore: %v", err)
	}
	defer restored.Close()
	if err := restored.RestoreFromDisk(ctx, path); err != nil {
		t.Fatalf("restore: %v", err)
	}

	cutoff := time.Date(2026, 2, 3, 9, 0, 0, 0, time.UTC)
	keys, err := restored.ListKeysByCharacterSince(ctx, "Jaina", cutoff)
	if err != nil {
		t.Fatalf("list after restore: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected 1 key after restore, got %d", len(keys))
	}
}

func TestSQLiteStoreAliasCRUD(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.db")

	st := NewSQLiteStore(Params{Path: path})
	if err := st.Open(ctx); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	keys := []models.CompletedKey{
		{
			KeyID:       3001,
			Character:   "Arthas",
			Region:      "us",
			Realm:       "illidan",
			Dungeon:     "Mists of Tirna Scithe",
			KeyLevel:    10,
			RunTimeMS:   1320000,
			ParTimeMS:   1500000,
			CompletedAt: "2026-02-04T01:23:45Z",
			Source:      "raiderio",
		},
		{
			KeyID:       3002,
			Character:   "Jaina",
			Region:      "us",
			Realm:       "stormrage",
			Dungeon:     "The Necrotic Wake",
			KeyLevel:    12,
			RunTimeMS:   1500000,
			ParTimeMS:   1600000,
			CompletedAt: "2026-02-04T02:00:00Z",
			Source:      "raiderio",
		},
	}
	for _, key := range keys {
		if err := st.UpsertCompletedKey(ctx, key); err != nil {
			t.Fatalf("upsert key %d: %v", key.KeyID, err)
		}
	}

	arthas := models.Character{Name: "Arthas", Realm: "illidan", Region: "us"}
	jaina := models.Character{Name: "Jaina", Realm: "stormrage", Region: "us"}

	if err := st.SetAliasCharacters(ctx, "RaidTeam", []models.Character{arthas}); err != nil {
		t.Fatalf("set alias: %v", err)
	}

	aliases, err := st.ListAliases(ctx)
	if err != nil {
		t.Fatalf("list aliases after set: %v", err)
	}
	if len(aliases) != 1 || aliases[0] != "raidteam" {
		t.Fatalf("expected one normalized alias name, got %#v", aliases)
	}

	chars, err := st.ListAliasCharacters(ctx, "raidteam")
	if err != nil {
		t.Fatalf("list alias after set: %v", err)
	}
	if len(chars) != 1 || chars[0].Name != "arthas" {
		t.Fatalf("expected alias to contain arthas, got %#v", chars)
	}

	if err := st.AddAliasCharacters(ctx, "raidteam", []models.Character{jaina, arthas}); err != nil {
		t.Fatalf("add alias chars: %v", err)
	}

	chars, err = st.ListAliasCharacters(ctx, "RAIDTEAM")
	if err != nil {
		t.Fatalf("list alias after add: %v", err)
	}
	if len(chars) != 2 {
		t.Fatalf("expected 2 alias members after add, got %#v", chars)
	}

	if err := st.RemoveAliasCharacters(ctx, "raidteam", []models.Character{arthas}); err != nil {
		t.Fatalf("remove arthas from alias: %v", err)
	}

	chars, err = st.ListAliasCharacters(ctx, "raidteam")
	if err != nil {
		t.Fatalf("list alias after remove: %v", err)
	}
	if len(chars) != 1 || chars[0].Name != "jaina" {
		t.Fatalf("expected alias to contain only jaina, got %#v", chars)
	}

	if err := st.RemoveAliasCharacters(ctx, "raidteam", []models.Character{jaina}); err != nil {
		t.Fatalf("remove jaina from alias: %v", err)
	}

	chars, err = st.ListAliasCharacters(ctx, "raidteam")
	if err != nil {
		t.Fatalf("list alias after deleting last member: %v", err)
	}
	if len(chars) != 0 {
		t.Fatalf("expected alias to be deleted when empty, got %#v", chars)
	}

	aliases, err = st.ListAliases(ctx)
	if err != nil {
		t.Fatalf("list aliases after delete: %v", err)
	}
	if len(aliases) != 0 {
		t.Fatalf("expected no aliases after deleting last member, got %#v", aliases)
	}
}

func TestSQLiteStoreAliasPersistenceAfterRestore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.db")

	st := NewSQLiteStore(Params{Path: path})
	if err := st.Open(ctx); err != nil {
		t.Fatalf("open: %v", err)
	}

	key := models.CompletedKey{
		KeyID:       4001,
		Character:   "Thrall",
		Region:      "us",
		Realm:       "area-52",
		Dungeon:     "Halls of Atonement",
		KeyLevel:    11,
		RunTimeMS:   1400000,
		ParTimeMS:   1500000,
		CompletedAt: "2026-02-04T03:00:00Z",
		Source:      "raiderio",
	}
	if err := st.UpsertCompletedKey(ctx, key); err != nil {
		t.Fatalf("upsert key: %v", err)
	}

	thrall := models.Character{Name: "Thrall", Realm: "area-52", Region: "us"}
	if err := st.SetAliasCharacters(ctx, "Shaman", []models.Character{thrall}); err != nil {
		t.Fatalf("set alias: %v", err)
	}

	if err := st.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	restored := NewSQLiteStore(Params{Path: path})
	if err := restored.Open(ctx); err != nil {
		t.Fatalf("open restore: %v", err)
	}
	defer restored.Close()
	if err := restored.RestoreFromDisk(ctx, path); err != nil {
		t.Fatalf("restore: %v", err)
	}

	chars, err := restored.ListAliasCharacters(ctx, "shaman")
	if err != nil {
		t.Fatalf("list alias after restore: %v", err)
	}
	if len(chars) != 1 || chars[0].Name != "thrall" {
		t.Fatalf("expected restored alias to contain thrall, got %#v", chars)
	}
}

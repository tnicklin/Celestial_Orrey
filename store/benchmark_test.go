package store

import (
	"context"
	"testing"
	"time"

	"github.com/tnicklin/celestial_orrey/models"
)

func BenchmarkUpsertCompletedKey(b *testing.B) {
	ctx := context.Background()
	st := NewSQLiteStore(Params{})
	st.SetFlushDebounce(1 * time.Hour) // Disable auto-flush for benchmark
	if err := st.Open(ctx); err != nil {
		b.Fatalf("open: %v", err)
	}
	defer st.Close()

	key := models.CompletedKey{
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

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key.KeyID = int64(i + 1)
		if err := st.UpsertCompletedKey(ctx, key); err != nil {
			b.Fatalf("upsert: %v", err)
		}
	}
}

func BenchmarkListKeysByCharacterSince(b *testing.B) {
	ctx := context.Background()
	st := NewSQLiteStore(Params{})
	st.SetFlushDebounce(1 * time.Hour)
	if err := st.Open(ctx); err != nil {
		b.Fatalf("open: %v", err)
	}
	defer st.Close()

	// Insert test data
	for i := 0; i < 100; i++ {
		key := models.CompletedKey{
			KeyID:       int64(i + 1),
			Character:   "Arthas",
			Region:      "us",
			Realm:       "illidan",
			Dungeon:     "Mists of Tirna Scithe",
			KeyLevel:    10 + (i % 10),
			RunTimeMS:   1320000,
			ParTimeMS:   1500000,
			CompletedAt: "2026-02-04T01:23:45Z",
			Source:      "raiderio",
		}
		if err := st.UpsertCompletedKey(ctx, key); err != nil {
			b.Fatalf("upsert: %v", err)
		}
	}

	cutoff := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := st.ListKeysByCharacterSince(ctx, "Arthas", cutoff); err != nil {
			b.Fatalf("list: %v", err)
		}
	}
}

func BenchmarkCountKeysByCharacterSince(b *testing.B) {
	ctx := context.Background()
	st := NewSQLiteStore(Params{})
	st.SetFlushDebounce(1 * time.Hour)
	if err := st.Open(ctx); err != nil {
		b.Fatalf("open: %v", err)
	}
	defer st.Close()

	// Insert test data for multiple characters
	chars := []string{"Arthas", "Jaina", "Thrall", "Sylvanas", "Anduin"}
	for i := 0; i < 100; i++ {
		key := models.CompletedKey{
			KeyID:       int64(i + 1),
			Character:   chars[i%len(chars)],
			Region:      "us",
			Realm:       "illidan",
			Dungeon:     "Mists of Tirna Scithe",
			KeyLevel:    10 + (i % 10),
			RunTimeMS:   1320000,
			ParTimeMS:   1500000,
			CompletedAt: "2026-02-04T01:23:45Z",
			Source:      "raiderio",
		}
		if err := st.UpsertCompletedKey(ctx, key); err != nil {
			b.Fatalf("upsert: %v", err)
		}
	}

	cutoff := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := st.CountKeysByCharacterSince(ctx, cutoff); err != nil {
			b.Fatalf("count: %v", err)
		}
	}
}

package raiderio

import (
	"context"
	"testing"
	"time"

	"github.com/tnicklin/celestial_orrey/models"
	"github.com/tnicklin/celestial_orrey/store"
	"github.com/tnicklin/celestial_orrey/timeutil"
)

type fakeClient struct {
	runs []models.CompletedKey
}

func (f *fakeClient) FetchWeeklyRuns(ctx context.Context, c models.Character) ([]models.CompletedKey, error) {
	return f.runs, nil
}

type fakeStore struct {
	seen []models.CompletedKey
}

func (f *fakeStore) Open(ctx context.Context) error { return nil }
func (f *fakeStore) Close() error                   { return nil }
func (f *fakeStore) RestoreFromDisk(ctx context.Context, path string) error {
	return nil
}
func (f *fakeStore) FlushToDisk(ctx context.Context, path string) error { return nil }
func (f *fakeStore) ArchiveWeek(ctx context.Context) error              { return nil }
func (f *fakeStore) UpsertCompletedKey(ctx context.Context, key models.CompletedKey) error {
	f.seen = append(f.seen, key)
	return nil
}
func (f *fakeStore) UpsertWarcraftLogsLink(ctx context.Context, link store.WarcraftLogsLink) error {
	return nil
}
func (f *fakeStore) CountKeysByCharacterSince(ctx context.Context, cutoff time.Time) ([]store.CountRow, error) {
	return nil, nil
}
func (f *fakeStore) ListKeysByCharacterSince(ctx context.Context, character string, cutoff time.Time) ([]models.CompletedKey, error) {
	return nil, nil
}
func (f *fakeStore) ListKeysSince(ctx context.Context, cutoff time.Time) ([]models.CompletedKey, error) {
	return nil, nil
}
func (f *fakeStore) ListWarcraftLogsLinksForKey(ctx context.Context, keyID int64) ([]store.WarcraftLogsLink, error) {
	return nil, nil
}
func (f *fakeStore) ListCharacters(ctx context.Context) ([]models.Character, error) {
	return nil, nil
}
func (f *fakeStore) GetCharacter(ctx context.Context, name, realm, region string) (*models.Character, error) {
	return nil, nil
}
func (f *fakeStore) DeleteCharacter(ctx context.Context, name, realm, region string) error {
	return nil
}
func (f *fakeStore) ListUnlinkedKeysSince(ctx context.Context, cutoff time.Time) ([]models.CompletedKey, error) {
	return nil, nil
}

func TestPollerFiltersAndAnnounces(t *testing.T) {
	// Use a fixed cutoff time for testing
	cutoff := timeutil.WeeklyReset()
	after := cutoff.Add(24 * time.Hour).Format(time.RFC3339)
	before := cutoff.Add(-24 * time.Hour).Format(time.RFC3339)

	client := &fakeClient{runs: []models.CompletedKey{
		{
			KeyID:       1,
			Character:   "Arthas",
			Region:      "us",
			Realm:       "illidan",
			Dungeon:     "Mists",
			KeyLevel:    10,
			RunTimeMS:   100,
			ParTimeMS:   120,
			CompletedAt: after,
			Source:      "raiderio",
		},
		{
			KeyID:       2,
			Character:   "Arthas",
			Region:      "us",
			Realm:       "illidan",
			Dungeon:     "Mists",
			KeyLevel:    9,
			RunTimeMS:   100,
			ParTimeMS:   120,
			CompletedAt: before,
			Source:      "raiderio",
		},
	}}
	st := &fakeStore{}

	poller := New(Params{
		Client:     client,
		Store:      st,
		Characters: []models.Character{{Region: "us", Realm: "illidan", Name: "Arthas"}},
	})

	known := map[string]struct{}{}
	err := poller.pollOnce(context.Background(), models.Character{Region: "us", Realm: "illidan", Name: "Arthas"}, known, make(chan struct{}, 1))
	if err != nil {
		t.Fatalf("poll once error: %v", err)
	}
	if len(st.seen) != 1 {
		t.Fatalf("expected 1 stored key, got %d", len(st.seen))
	}
}

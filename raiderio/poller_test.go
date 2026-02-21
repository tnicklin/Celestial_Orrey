package raiderio

import (
	"context"
	"testing"
	"time"

	"github.com/tnicklin/celestial_orrey/models"
	rioClient "github.com/tnicklin/celestial_orrey/raiderio/client"
	"github.com/tnicklin/celestial_orrey/store"
	"github.com/tnicklin/celestial_orrey/timeutil"
)

func TestPollerFiltersKeys(t *testing.T) {
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

	st := &fakeStore{
		characters: []models.Character{{Region: "us", Realm: "illidan", Name: "Arthas"}},
	}

	poller := New(Params{
		Client: client,
		Store:  st,
	})

	poller.pollCharacter(context.Background(), models.Character{Region: "us", Realm: "illidan", Name: "Arthas"})

	if len(st.seen) != 1 {
		t.Fatalf("expected 1 stored key (after cutoff), got %d", len(st.seen))
	}
	if st.seen[0].KeyID != 1 {
		t.Fatalf("expected key ID 1, got %d", st.seen[0].KeyID)
	}
}

type fakeClient struct {
	runs []models.CompletedKey
}

func (f *fakeClient) FetchWeeklyRuns(ctx context.Context, c models.Character) (rioClient.ProfileResult, error) {
	return rioClient.ProfileResult{Keys: f.runs}, nil
}

type fakeStore struct {
	characters []models.Character
	seen       []models.CompletedKey
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
	return f.characters, nil
}
func (f *fakeStore) GetCharacter(ctx context.Context, name, realm, region string) (*models.Character, error) {
	return nil, nil
}
func (f *fakeStore) DeleteCharacter(ctx context.Context, name, realm, region string) error {
	return nil
}
func (f *fakeStore) UpdateCharacterScore(ctx context.Context, name, realm, region string, score float64) error {
	return nil
}
func (f *fakeStore) ListUnlinkedKeysSince(ctx context.Context, cutoff time.Time) ([]models.CompletedKey, error) {
	return nil, nil
}
func (f *fakeStore) UpsertElvUIVersion(ctx context.Context, v store.ElvUIVersion) error {
	return nil
}
func (f *fakeStore) GetElvUIVersion(ctx context.Context) (*store.ElvUIVersion, error) {
	return nil, nil
}

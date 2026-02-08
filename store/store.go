package store

import (
	"context"
	"time"

	"github.com/tnicklin/celestial_orrey/models"
)

type CountRow struct {
	Region   string
	Realm    string
	Name     string
	KeyCount int64
}

type WarcraftLogsLink struct {
	KeyID      int64
	ReportCode string
	FightID    *int64
	PullID     *int64
	URL        string
	InsertedAt string
}

type Store interface {
	Open(ctx context.Context) error
	Close() error

	RestoreFromDisk(ctx context.Context, path string) error
	FlushToDisk(ctx context.Context, path string) error

	UpsertCompletedKey(ctx context.Context, key models.CompletedKey) error
	UpsertWarcraftLogsLink(ctx context.Context, link WarcraftLogsLink) error
	DeleteCharacter(ctx context.Context, name, realm, region string) error

	ListCharacters(ctx context.Context) ([]models.Character, error)
	GetCharacter(ctx context.Context, name, realm, region string) (*models.Character, error)
	CountKeysByCharacterSince(ctx context.Context, cutoff time.Time) ([]CountRow, error)
	ListKeysByCharacterSince(ctx context.Context, character string, cutoff time.Time) ([]models.CompletedKey, error)
	ListKeysSince(ctx context.Context, cutoff time.Time) ([]models.CompletedKey, error)
	ListUnlinkedKeysSince(ctx context.Context, cutoff time.Time) ([]models.CompletedKey, error)
	ListWarcraftLogsLinksForKey(ctx context.Context, keyID int64) ([]WarcraftLogsLink, error)
}

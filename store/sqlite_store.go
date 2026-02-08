package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-sqlite3"
	"github.com/tnicklin/celestial_orrey/logger"
	"github.com/tnicklin/celestial_orrey/models"
	"github.com/tnicklin/celestial_orrey/store/db"
)

var _ Store = (*SQLiteStore)(nil)

const (
	memoryDSN       = "file:celestial_orrey?mode=memory&cache=shared&_foreign_keys=on&_busy_timeout=5000"
	defaultDebounce = 5 * time.Second
)

type SQLiteStore struct {
	mu           sync.RWMutex
	db           *sql.DB
	snapshotPath string
	logger       logger.Logger

	// Debounced flush
	flushDebounce time.Duration
	flushTimer    *time.Timer
	flushMu       sync.Mutex
	dirty         bool
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
}

type Params struct {
	Path   string
	Logger logger.Logger
}

func NewSQLiteStore(p Params) *SQLiteStore {
	return &SQLiteStore{
		snapshotPath:  p.Path,
		flushDebounce: defaultDebounce,
		logger:        p.Logger,
	}
}

func (s *SQLiteStore) log() logger.Logger {
	if s.logger == nil {
		return nopLogger{}
	}
	return s.logger
}

// nopLogger is a no-op logger for when no logger is configured.
type nopLogger struct{}

func (nopLogger) DebugW(_ string, _ ...any) {}
func (nopLogger) InfoW(_ string, _ ...any)  {}
func (nopLogger) WarnW(_ string, _ ...any)  {}
func (nopLogger) ErrorW(_ string, _ ...any) {}
func (nopLogger) Sync() error               { return nil }

// SetFlushDebounce sets the debounce duration for disk flushes.
// Must be called before Open().
func (s *SQLiteStore) SetFlushDebounce(d time.Duration) {
	s.flushDebounce = d
}

func (s *SQLiteStore) SetSnapshotPath(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshotPath = path
}

func (s *SQLiteStore) Open(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db != nil {
		return nil
	}

	database, err := sql.Open("sqlite3", memoryDSN)
	if err != nil {
		return err
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)

	if err = database.PingContext(ctx); err != nil {
		_ = database.Close()
		return err
	}

	s.db = database
	s.ctx, s.cancel = context.WithCancel(context.Background())
	return s.applyMigrations(ctx)
}

// Close closes the database without flushing. Use Shutdown for graceful shutdown.
func (s *SQLiteStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.stopFlushTimer()
	if s.cancel != nil {
		s.cancel()
	}

	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

// Shutdown performs a final flush to disk and closes the database.
func (s *SQLiteStore) Shutdown(ctx context.Context) error {
	s.flushMu.Lock()
	s.stopFlushTimer()
	s.flushMu.Unlock()

	// Perform final flush if dirty
	if s.dirty && s.snapshotPath != "" {
		if err := s.FlushToDisk(ctx, s.snapshotPath); err != nil {
			// Log error but continue with close
			fmt.Fprintf(os.Stderr, "shutdown flush failed: %v\n", err)
		}
	}

	return s.Close()
}

func (s *SQLiteStore) RestoreFromDisk(ctx context.Context, path string) error {
	if path == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return errors.New("store is not open")
	}

	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	fileDB, err := sql.Open("sqlite3", sqliteFileDSN(path))
	if err != nil {
		return err
	}
	defer fileDB.Close()

	if err := s.backup(ctx, fileDB, s.db); err != nil {
		return err
	}

	return s.applyMigrations(ctx)
}

func (s *SQLiteStore) FlushToDisk(ctx context.Context, path string) error {
	if path == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.flushLocked(ctx, path)
}

func (s *SQLiteStore) scheduleFlush() {
	if s.snapshotPath == "" {
		return
	}

	s.flushMu.Lock()
	defer s.flushMu.Unlock()

	s.dirty = true
	if s.flushTimer != nil {
		s.flushTimer.Stop()
	}

	s.flushTimer = time.AfterFunc(s.flushDebounce, func() {
		s.performScheduledFlush()
	})
}

func (s *SQLiteStore) performScheduledFlush() {
	s.flushMu.Lock()
	if !s.dirty {
		s.flushMu.Unlock()
		return
	}
	s.flushMu.Unlock()

	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()

	if err := s.FlushToDisk(ctx, s.snapshotPath); err != nil {
		fmt.Fprintf(os.Stderr, "scheduled flush failed: %v\n", err)
		return
	}

	s.flushMu.Lock()
	s.dirty = false
	s.flushMu.Unlock()
}

func (s *SQLiteStore) stopFlushTimer() {
	if s.flushTimer != nil {
		s.flushTimer.Stop()
		s.flushTimer = nil
	}
}

func (s *SQLiteStore) UpsertCompletedKey(ctx context.Context, key models.CompletedKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return errors.New("store is not open")
	}

	s.log().DebugW("upserting completed key",
		"character", key.Character,
		"realm", key.Realm,
		"region", key.Region,
		"dungeon", key.Dungeon,
		"level", key.KeyLevel,
		"key_id", key.KeyID,
		"completed_at", key.CompletedAt,
		"source", key.Source,
	)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.log().ErrorW("failed to begin transaction", "error", err)
		return err
	}

	var characterID int64
	queries := db.New(tx)
	characterID, err = queries.UpsertCharacter(ctx, db.UpsertCharacterParams{
		Region: strings.ToLower(key.Region),
		Realm:  strings.ToLower(key.Realm),
		Name:   strings.ToLower(key.Character),
	})
	if err != nil {
		_ = tx.Rollback()
		s.log().ErrorW("failed to upsert character",
			"error", err,
			"character", key.Character,
			"realm", key.Realm,
			"region", key.Region,
		)
		return err
	}

	s.log().DebugW("character upserted",
		"character_id", characterID,
		"character", key.Character,
		"realm", key.Realm,
		"region", key.Region,
	)

	keyID := key.KeyID
	if keyID <= 0 {
		keyID = syntheticKeyID(key)
		s.log().DebugW("using synthetic key ID",
			"original_key_id", key.KeyID,
			"synthetic_key_id", keyID,
		)
	}

	err = queries.InsertCompletedKey(ctx, db.InsertCompletedKeyParams{
		KeyID:       keyID,
		CharacterID: characterID,
		Dungeon:     key.Dungeon,
		KeyLvl:      int64(key.KeyLevel),
		RunTimeMs:   key.RunTimeMS,
		ParTimeMs:   key.ParTimeMS,
		CompletedAt: key.CompletedAt,
		Source:      key.Source,
	})
	if err != nil {
		_ = tx.Rollback()
		s.log().ErrorW("failed to insert completed key",
			"error", err,
			"key_id", keyID,
			"dungeon", key.Dungeon,
		)
		return err
	}

	if err := tx.Commit(); err != nil {
		s.log().ErrorW("failed to commit transaction", "error", err)
		return err
	}

	s.log().DebugW("completed key inserted successfully",
		"key_id", keyID,
		"character", key.Character,
		"dungeon", key.Dungeon,
		"level", key.KeyLevel,
	)

	// Schedule debounced flush instead of immediate flush
	s.scheduleFlush()
	return nil
}

func (s *SQLiteStore) UpsertWarcraftLogsLink(ctx context.Context, link WarcraftLogsLink) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return errors.New("store is not open")
	}

	queries := db.New(s.db)
	var fightID sql.NullInt64
	if link.FightID != nil {
		fightID = sql.NullInt64{Int64: *link.FightID, Valid: true}
	}
	var pullID sql.NullInt64
	if link.PullID != nil {
		pullID = sql.NullInt64{Int64: *link.PullID, Valid: true}
	}
	var url sql.NullString
	if link.URL != "" {
		url = sql.NullString{String: link.URL, Valid: true}
	}

	if err := queries.InsertWarcraftLogsLink(ctx, db.InsertWarcraftLogsLinkParams{
		KeyID:      link.KeyID,
		ReportCode: link.ReportCode,
		FightID:    fightID,
		PullID:     pullID,
		Url:        url,
	}); err != nil {
		return err
	}

	// Schedule debounced flush instead of immediate flush
	s.scheduleFlush()
	return nil
}

func (s *SQLiteStore) CountKeysByCharacterSince(ctx context.Context, cutoff time.Time) ([]CountRow, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, errors.New("store is not open")
	}

	queries := db.New(s.db)
	rows, err := queries.CountKeysByCharacterSince(ctx, cutoff.Format(time.RFC3339))
	if err != nil {
		return nil, err
	}

	out := make([]CountRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, CountRow{
			Region:   row.Region,
			Realm:    row.Realm,
			Name:     row.Name,
			KeyCount: row.KeyCount,
		})
	}
	return out, nil
}

func (s *SQLiteStore) ListKeysByCharacterSince(ctx context.Context, character string, cutoff time.Time) ([]models.CompletedKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, errors.New("store is not open")
	}

	characterLower := strings.ToLower(character)
	s.log().DebugW("listing keys by character since",
		"character", characterLower,
		"cutoff", cutoff.Format(time.RFC3339),
	)

	queries := db.New(s.db)
	rows, err := queries.ListKeysByCharacterSince(ctx, db.ListKeysByCharacterSinceParams{
		LOWER:       characterLower,
		CompletedAt: cutoff.Format(time.RFC3339),
	})
	if err != nil {
		s.log().ErrorW("failed to list keys by character",
			"error", err,
			"character", character,
		)
		return nil, err
	}

	out := make([]models.CompletedKey, 0, len(rows))
	for _, row := range rows {
		key := models.CompletedKey{
			KeyID:       row.KeyID,
			Region:      row.Region,
			Realm:       row.Realm,
			Character:   row.Character,
			Dungeon:     row.Dungeon,
			KeyLevel:    int(row.KeyLvl),
			RunTimeMS:   row.RunTimeMs,
			ParTimeMS:   row.ParTimeMs,
			CompletedAt: row.CompletedAt,
			Source:      row.Source,
		}
		out = append(out, key)
		s.log().DebugW("found key",
			"character", row.Character,
			"realm", row.Realm,
			"region", row.Region,
			"dungeon", row.Dungeon,
			"level", row.KeyLvl,
			"key_id", row.KeyID,
		)
	}

	s.log().DebugW("keys found for character",
		"character", character,
		"count", len(out),
	)

	return out, nil
}

func (s *SQLiteStore) ListKeysSince(ctx context.Context, cutoff time.Time) ([]models.CompletedKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, errors.New("store is not open")
	}

	queries := db.New(s.db)
	rows, err := queries.ListKeysSince(ctx, cutoff.Format(time.RFC3339))
	if err != nil {
		return nil, err
	}

	out := make([]models.CompletedKey, 0, len(rows))
	for _, row := range rows {
		out = append(out, models.CompletedKey{
			KeyID:       row.KeyID,
			Region:      row.Region,
			Realm:       row.Realm,
			Character:   row.Character,
			Dungeon:     row.Dungeon,
			KeyLevel:    int(row.KeyLvl),
			RunTimeMS:   row.RunTimeMs,
			ParTimeMS:   row.ParTimeMs,
			CompletedAt: row.CompletedAt,
			Source:      row.Source,
		})
	}
	return out, nil
}

func (s *SQLiteStore) ListUnlinkedKeysSince(ctx context.Context, cutoff time.Time) ([]models.CompletedKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, errors.New("store is not open")
	}

	queries := db.New(s.db)
	rows, err := queries.ListUnlinkedKeysSince(ctx, cutoff.Format(time.RFC3339))
	if err != nil {
		return nil, err
	}

	out := make([]models.CompletedKey, 0, len(rows))
	for _, row := range rows {
		out = append(out, models.CompletedKey{
			KeyID:       row.KeyID,
			Region:      row.Region,
			Realm:       row.Realm,
			Character:   row.Character,
			Dungeon:     row.Dungeon,
			KeyLevel:    int(row.KeyLvl),
			RunTimeMS:   row.RunTimeMs,
			ParTimeMS:   row.ParTimeMs,
			CompletedAt: row.CompletedAt,
			Source:      row.Source,
		})
	}
	return out, nil
}

func (s *SQLiteStore) ListWarcraftLogsLinksForKey(ctx context.Context, keyID int64) ([]WarcraftLogsLink, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, errors.New("store is not open")
	}

	queries := db.New(s.db)
	rows, err := queries.ListWarcraftLogsLinksForKey(ctx, keyID)
	if err != nil {
		return nil, err
	}

	out := make([]WarcraftLogsLink, 0, len(rows))
	for _, row := range rows {
		var fightID *int64
		if row.FightID.Valid {
			value := row.FightID.Int64
			fightID = &value
		}
		var pullID *int64
		if row.PullID.Valid {
			value := row.PullID.Int64
			pullID = &value
		}
		var url string
		if row.Url.Valid {
			url = row.Url.String
		}
		out = append(out, WarcraftLogsLink{
			KeyID:      row.KeyID,
			ReportCode: row.ReportCode,
			FightID:    fightID,
			PullID:     pullID,
			URL:        url,
			InsertedAt: row.InsertedAt,
		})
	}
	return out, nil
}

func (s *SQLiteStore) ListCharacters(ctx context.Context) ([]models.Character, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, errors.New("store is not open")
	}

	s.log().DebugW("listing all characters")

	queries := db.New(s.db)
	rows, err := queries.ListCharacters(ctx)
	if err != nil {
		s.log().ErrorW("failed to list characters", "error", err)
		return nil, err
	}

	out := make([]models.Character, 0, len(rows))
	for _, row := range rows {
		char := models.Character{
			Region: row.Region,
			Realm:  row.Realm,
			Name:   row.Name,
		}
		out = append(out, char)
		s.log().DebugW("found character",
			"name", row.Name,
			"realm", row.Realm,
			"region", row.Region,
		)
	}

	s.log().DebugW("characters found", "count", len(out))
	return out, nil
}

func (s *SQLiteStore) GetCharacter(ctx context.Context, name, realm, region string) (*models.Character, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, errors.New("store is not open")
	}

	queries := db.New(s.db)
	row, err := queries.GetCharacter(ctx, db.GetCharacterParams{
		LOWER:   name,
		LOWER_2: realm,
		LOWER_3: region,
	})
	if err != nil {
		return nil, err
	}

	return &models.Character{
		Region: row.Region,
		Realm:  row.Realm,
		Name:   row.Name,
	}, nil
}

func (s *SQLiteStore) DeleteCharacter(ctx context.Context, name, realm, region string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return errors.New("store is not open")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	queries := db.New(tx)

	// Get character ID
	charID, err := queries.GetCharacterID(ctx, db.GetCharacterIDParams{
		LOWER:   name,
		LOWER_2: realm,
		LOWER_3: region,
	})
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("character not found: %s-%s (%s)", name, realm, region)
	}

	// Delete WCL links for this character's keys
	if err := queries.DeleteWarcraftLogsLinksByCharacter(ctx, charID); err != nil {
		_ = tx.Rollback()
		return err
	}

	// Delete completed keys
	if err := queries.DeleteCompletedKeysByCharacter(ctx, charID); err != nil {
		_ = tx.Rollback()
		return err
	}

	// Delete character
	if err := queries.DeleteCharacter(ctx, charID); err != nil {
		_ = tx.Rollback()
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	// Schedule debounced flush instead of immediate flush
	s.scheduleFlush()
	return nil
}

func (s *SQLiteStore) flushLocked(ctx context.Context, path string) error {
	if s.db == nil {
		return errors.New("store is not open")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	fileDB, err := sql.Open("sqlite3", sqliteFileDSN(path))
	if err != nil {
		return err
	}
	defer fileDB.Close()

	return s.backup(ctx, s.db, fileDB)
}

func (s *SQLiteStore) backup(ctx context.Context, src *sql.DB, dst *sql.DB) error {
	srcConn, err := src.Conn(ctx)
	if err != nil {
		return err
	}
	defer srcConn.Close()

	dstConn, err := dst.Conn(ctx)
	if err != nil {
		return err
	}
	defer dstConn.Close()

	return dstConn.Raw(func(dstDriver any) error {
		return srcConn.Raw(func(srcDriver any) error {
			dstSQLite, ok := dstDriver.(*sqlite3.SQLiteConn)
			if !ok {
				return fmt.Errorf("unexpected destination driver: %T", dstDriver)
			}
			srcSQLite, ok := srcDriver.(*sqlite3.SQLiteConn)
			if !ok {
				return fmt.Errorf("unexpected source driver: %T", srcDriver)
			}

			backup, err := dstSQLite.Backup("main", srcSQLite, "main")
			if err != nil {
				return err
			}
			defer backup.Finish()

			_, err = backup.Step(-1)
			return err
		})
	})
}

func (s *SQLiteStore) applyMigrations(ctx context.Context) error {
	if s.db == nil {
		return errors.New("store is not open")
	}

	migrationsPath, err := resolveMigrationsPath()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(migrationsPath)
	if err != nil {
		return err
	}

	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".sql") {
			files = append(files, name)
		}
	}
	sort.Strings(files)

	for _, name := range files {
		path := filepath.Join(migrationsPath, name)
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sqlText := strings.TrimSpace(string(content))
		if sqlText == "" {
			continue
		}
		if _, err := s.db.ExecContext(ctx, sqlText); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
	}
	return nil
}

func resolveMigrationsPath() (string, error) {
	paths := []string{
		filepath.Join("schema", "migrations"),
		filepath.Join("store", "schema", "migrations"),
	}
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return path, nil
		}
	}
	return "", fmt.Errorf("migrations directory not found")
}

func sqliteFileDSN(path string) string {
	return fmt.Sprintf("file:%s?_foreign_keys=on&_busy_timeout=5000", path)
}

func syntheticKeyID(key models.CompletedKey) int64 {
	synthetic := key.SyntheticKey()
	raw, err := hex.DecodeString(synthetic)
	if err != nil || len(raw) < 8 {
		sum := sha256.Sum256([]byte(synthetic))
		raw = sum[:]
	}
	value := int64(binary.BigEndian.Uint64(raw[:8]) & 0x7FFFFFFFFFFFFFFF)
	if value == 0 {
		return 1
	}
	return value
}

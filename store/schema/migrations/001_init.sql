PRAGMA journal_mode=WAL;

CREATE TABLE IF NOT EXISTS characters (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  region TEXT NOT NULL,
  realm  TEXT NOT NULL,
  name   TEXT NOT NULL,
  UNIQUE(region, realm, name)
);

CREATE TABLE IF NOT EXISTS completed_keys (
  key_id        INTEGER PRIMARY KEY,
  character_id  INTEGER NOT NULL REFERENCES characters(id),
  dungeon       TEXT NOT NULL,
  key_lvl       INTEGER NOT NULL,
  run_time_ms   INTEGER NOT NULL,
  par_time_ms   INTEGER NOT NULL,
  completed_at  TEXT NOT NULL,
  source        TEXT NOT NULL,
  inserted_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_completed_keys_completed_at
ON completed_keys(completed_at);

CREATE INDEX IF NOT EXISTS idx_completed_keys_character_time
ON completed_keys(character_id, completed_at);

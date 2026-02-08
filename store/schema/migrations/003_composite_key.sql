-- Change primary key from key_id to (key_id, character_id)
-- This allows multiple tracked characters to each own their participation in the same run

-- Create new table with composite primary key
CREATE TABLE IF NOT EXISTS completed_keys_new (
  key_id        INTEGER NOT NULL,
  character_id  INTEGER NOT NULL REFERENCES characters(id),
  dungeon       TEXT NOT NULL,
  key_lvl       INTEGER NOT NULL,
  run_time_ms   INTEGER NOT NULL,
  par_time_ms   INTEGER NOT NULL,
  completed_at  TEXT NOT NULL,
  source        TEXT NOT NULL,
  inserted_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  PRIMARY KEY (key_id, character_id)
);

-- Copy existing data
INSERT OR IGNORE INTO completed_keys_new
SELECT key_id, character_id, dungeon, key_lvl, run_time_ms, par_time_ms, completed_at, source, inserted_at
FROM completed_keys;

-- Drop old table
DROP TABLE IF EXISTS completed_keys;

-- Rename new table
ALTER TABLE completed_keys_new RENAME TO completed_keys;

-- Recreate indexes
CREATE INDEX IF NOT EXISTS idx_completed_keys_completed_at
ON completed_keys(completed_at);

CREATE INDEX IF NOT EXISTS idx_completed_keys_character_time
ON completed_keys(character_id, completed_at);

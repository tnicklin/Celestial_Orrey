CREATE TABLE IF NOT EXISTS warcraftlogs_links (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  key_id INTEGER NOT NULL REFERENCES completed_keys(key_id),
  report_code TEXT NOT NULL,
  fight_id INTEGER,
  pull_id INTEGER,
  url TEXT,
  inserted_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  UNIQUE(key_id, report_code, fight_id, pull_id)
);

CREATE INDEX IF NOT EXISTS idx_warcraftlogs_links_key
ON warcraftlogs_links(key_id);

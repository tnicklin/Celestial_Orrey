CREATE TABLE IF NOT EXISTS item_names (
  item_id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  cached_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

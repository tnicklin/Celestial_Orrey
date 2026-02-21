CREATE TABLE IF NOT EXISTS elvui_versions (
  id INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  version TEXT NOT NULL,
  download_url TEXT NOT NULL,
  changelog_url TEXT NOT NULL,
  last_update TEXT NOT NULL,
  checked_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

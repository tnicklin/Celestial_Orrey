CREATE TABLE IF NOT EXISTS character_aliases (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  alias_name TEXT NOT NULL,
  UNIQUE(alias_name)
);

CREATE TABLE IF NOT EXISTS character_alias_members (
  alias_id INTEGER NOT NULL REFERENCES character_aliases(id) ON DELETE CASCADE,
  character_id INTEGER NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
  PRIMARY KEY(alias_id, character_id)
);

CREATE INDEX IF NOT EXISTS idx_character_alias_members_character_id
ON character_alias_members(character_id);

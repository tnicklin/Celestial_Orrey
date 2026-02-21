-- name: UpsertCharacter :one
INSERT INTO characters(region, realm, name)
VALUES (?, ?, ?)
ON CONFLICT(region, realm, name) DO UPDATE SET name=excluded.name
RETURNING id;

-- name: InsertCompletedKey :exec
INSERT INTO completed_keys(
  key_id, character_id, dungeon, key_lvl, run_time_ms, par_time_ms, completed_at, source
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(key_id, character_id) DO UPDATE SET
  dungeon = excluded.dungeon,
  key_lvl = excluded.key_lvl,
  run_time_ms = excluded.run_time_ms,
  par_time_ms = excluded.par_time_ms,
  completed_at = excluded.completed_at,
  source = excluded.source;

-- name: CountKeysByCharacterSince :many
SELECT c.region, c.realm, c.name, COUNT(*) AS key_count
FROM completed_keys k
JOIN characters c ON c.id = k.character_id
WHERE k.completed_at > ?
GROUP BY c.region, c.realm, c.name
ORDER BY key_count DESC;

-- name: ListKeysByCharacterSince :many
SELECT k.key_id, c.region, c.realm, c.name AS character, k.dungeon, k.key_lvl,
k.run_time_ms, k.par_time_ms, k.completed_at, k.source
FROM completed_keys k
JOIN characters c ON c.id = k.character_id
WHERE LOWER(c.name) = LOWER(?) AND k.completed_at > ?
ORDER BY k.completed_at DESC;

-- name: ListAllKeysWithCharacters :many
SELECT k.key_id, c.id as character_id, c.region, c.realm, c.name AS character,
k.dungeon, k.key_lvl, k.run_time_ms, k.par_time_ms, k.completed_at, k.source
FROM completed_keys k
JOIN characters c ON c.id = k.character_id
ORDER BY k.completed_at DESC;

-- name: ListKeysSince :many
SELECT k.key_id, c.region, c.realm, c.name AS character, k.dungeon, k.key_lvl,
  k.run_time_ms, k.par_time_ms, k.completed_at, k.source
FROM completed_keys k
JOIN characters c ON c.id = k.character_id
WHERE k.completed_at > ?
ORDER BY k.completed_at DESC;

-- name: UpdateCharacterScore :exec
UPDATE characters SET rio_score = ?
WHERE LOWER(name) = LOWER(?) AND LOWER(realm) = LOWER(?) AND LOWER(region) = LOWER(?);

-- name: ListCharacters :many
SELECT region, realm, name, rio_score FROM characters ORDER BY region, realm, name;

-- name: GetCharacter :one
SELECT id, region, realm, name, rio_score FROM characters
WHERE LOWER(name) = LOWER(?) AND LOWER(realm) = LOWER(?) AND LOWER(region) = LOWER(?);

-- name: GetCharacterID :one
SELECT id FROM characters
WHERE LOWER(name) = LOWER(?) AND LOWER(realm) = LOWER(?) AND LOWER(region) = LOWER(?);

-- name: DeleteWarcraftLogsLinksByCharacter :exec
DELETE FROM warcraftlogs_links
WHERE key_id IN (
  SELECT k.key_id FROM completed_keys k
  JOIN characters c ON c.id = k.character_id
  WHERE c.id = ?
);

-- name: DeleteCompletedKeysByCharacter :exec
DELETE FROM completed_keys WHERE character_id = ?;

-- name: DeleteCharacter :exec
DELETE FROM characters WHERE id = ?;

-- name: ListUnlinkedKeysSince :many
SELECT DISTINCT k.key_id, c.region, c.realm, c.name AS character, k.dungeon, k.key_lvl,
  k.run_time_ms, k.par_time_ms, k.completed_at, k.source
FROM completed_keys k
JOIN characters c ON c.id = k.character_id
LEFT JOIN warcraftlogs_links w ON w.key_id = k.key_id
WHERE k.completed_at > ? AND w.id IS NULL
ORDER BY k.completed_at DESC;

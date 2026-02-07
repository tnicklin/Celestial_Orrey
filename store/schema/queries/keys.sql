-- name: UpsertCharacter :one
INSERT INTO characters(region, realm, name)
VALUES (?, ?, ?)
ON CONFLICT(region, realm, name) DO UPDATE SET name=excluded.name
RETURNING id;

-- name: InsertCompletedKey :exec
INSERT OR IGNORE INTO completed_keys(
  key_id, character_id, dungeon, key_lvl, run_time_ms, par_time_ms, completed_at, source
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

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
WHERE c.name = ? AND k.completed_at > ?
ORDER BY k.completed_at DESC;

-- name: ListKeysSince :many
SELECT k.key_id, c.region, c.realm, c.name AS character, k.dungeon, k.key_lvl,
  k.run_time_ms, k.par_time_ms, k.completed_at, k.source
FROM completed_keys k
JOIN characters c ON c.id = k.character_id
WHERE k.completed_at > ?
ORDER BY k.completed_at DESC;

-- name: ListCharacters :many
SELECT region, realm, name FROM characters ORDER BY region, realm, name;

-- name: GetCharacter :one
SELECT id, region, realm, name FROM characters
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

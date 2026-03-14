-- name: UpsertCharacterAlias :one
INSERT INTO character_aliases(alias_name)
VALUES (?)
ON CONFLICT(alias_name) DO UPDATE SET alias_name = excluded.alias_name
RETURNING id;

-- name: GetCharacterAliasID :one
SELECT id FROM character_aliases
WHERE alias_name = ?;

-- name: InsertCharacterAliasMember :exec
INSERT INTO character_alias_members(alias_id, character_id)
VALUES (?, ?)
ON CONFLICT(alias_id, character_id) DO NOTHING;

-- name: DeleteCharacterAliasMembers :exec
DELETE FROM character_alias_members
WHERE alias_id = ?;

-- name: DeleteCharacterAliasMember :exec
DELETE FROM character_alias_members
WHERE alias_id = ? AND character_id = ?;

-- name: CountCharacterAliasMembers :one
SELECT COUNT(*) FROM character_alias_members
WHERE alias_id = ?;

-- name: DeleteCharacterAlias :exec
DELETE FROM character_aliases
WHERE id = ?;

-- name: ListAliasCharacters :many
SELECT c.region, c.realm, c.name, c.rio_score
FROM character_alias_members am
JOIN character_aliases a ON a.id = am.alias_id
JOIN characters c ON c.id = am.character_id
WHERE a.alias_name = ?
ORDER BY c.region, c.realm, c.name;

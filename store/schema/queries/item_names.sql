-- name: GetItemName :one
SELECT name FROM item_names WHERE item_id = ?;

-- name: UpsertItemName :exec
INSERT INTO item_names (item_id, name)
VALUES (?, ?)
ON CONFLICT(item_id) DO UPDATE SET
  name = excluded.name,
  cached_at = strftime('%Y-%m-%dT%H:%M:%fZ','now');

-- name: ListItemNames :many
SELECT item_id, name, cached_at FROM item_names ORDER BY item_id;

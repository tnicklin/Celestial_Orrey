-- name: UpsertElvUIVersion :exec
INSERT INTO elvui_versions (id, version, download_url, changelog_url, last_update)
VALUES (1, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  version = excluded.version,
  download_url = excluded.download_url,
  changelog_url = excluded.changelog_url,
  last_update = excluded.last_update,
  checked_at = strftime('%Y-%m-%dT%H:%M:%fZ','now');

-- name: GetElvUIVersion :one
SELECT version, download_url, changelog_url, last_update, checked_at
FROM elvui_versions
WHERE id = 1;

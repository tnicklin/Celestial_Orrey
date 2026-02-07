-- name: InsertWarcraftLogsLink :exec
INSERT OR IGNORE INTO warcraftlogs_links(
  key_id, report_code, fight_id, pull_id, url
) VALUES (?, ?, ?, ?, ?);

-- name: ListWarcraftLogsLinksForKey :many
SELECT key_id, report_code, fight_id, pull_id, url, inserted_at
FROM warcraftlogs_links
WHERE key_id = ?
ORDER BY inserted_at DESC;

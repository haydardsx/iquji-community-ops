CREATE TABLE IF NOT EXISTS operation_read_versions (
  operation_run_id TEXT PRIMARY KEY,
  status TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

-- 升级之前的终态默认已读，只提醒升级后新产生的完成结果。
INSERT OR IGNORE INTO operation_read_versions (operation_run_id, status, updated_at)
SELECT json_extract(item.value, '$.id'), json_extract(item.value, '$.status'),
       COALESCE(json_extract(item.value, '$.updatedAt'), '')
FROM app_state, json_each(app_state.state, '$.operationRuns') AS item
WHERE app_state.id = 'current'
  AND json_extract(item.value, '$.status') IN ('succeeded', 'failed', 'cancelled');

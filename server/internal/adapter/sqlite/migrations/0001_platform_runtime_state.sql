CREATE TABLE IF NOT EXISTS app_state (
  id TEXT PRIMARY KEY,
  state TEXT NOT NULL,
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS platform_runtime_status (
  component TEXT PRIMARY KEY,
  status TEXT NOT NULL,
  summary TEXT NOT NULL,
  sampled_at TEXT NOT NULL,
  metadata TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS process_runs (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  status TEXT NOT NULL,
  subject TEXT NOT NULL,
  decision TEXT NOT NULL,
  metrics TEXT NOT NULL DEFAULT '{}',
  started_at TEXT,
  completed_at TEXT,
  generated_at TEXT NOT NULL,
  report TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS process_runs_kind_generated_idx
  ON process_runs (kind, generated_at DESC);

CREATE TABLE IF NOT EXISTS process_checks (
  id TEXT PRIMARY KEY,
  process_id TEXT NOT NULL,
  code TEXT NOT NULL,
  category TEXT NOT NULL,
  phase TEXT NOT NULL,
  target TEXT NOT NULL,
  outcome TEXT NOT NULL,
  severity TEXT NOT NULL,
  blocking INTEGER NOT NULL,
  summary TEXT NOT NULL,
  details TEXT NOT NULL DEFAULT '{}',
  observed_at TEXT NOT NULL,
  FOREIGN KEY(process_id) REFERENCES process_runs(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS process_checks_process_idx
  ON process_checks (process_id, observed_at);

CREATE TABLE IF NOT EXISTS process_resources (
  process_id TEXT NOT NULL,
  resource_kind TEXT NOT NULL,
  resource_id TEXT NOT NULL,
  resource_name TEXT,
  resource_scope TEXT,
  labels TEXT NOT NULL DEFAULT '{}',
  PRIMARY KEY (process_id, resource_kind, resource_id),
  FOREIGN KEY(process_id) REFERENCES process_runs(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS process_resources_resource_idx
  ON process_resources (resource_kind, resource_id);

CREATE TABLE IF NOT EXISTS resource_inventory (
  resource_kind TEXT NOT NULL,
  resource_id TEXT NOT NULL,
  resource_name TEXT,
  resource_scope TEXT,
  labels TEXT NOT NULL DEFAULT '{}',
  updated_at TEXT NOT NULL,
  PRIMARY KEY (resource_kind, resource_id)
);

CREATE TABLE IF NOT EXISTS resource_links (
  source_kind TEXT NOT NULL,
  source_id TEXT NOT NULL,
  target_kind TEXT NOT NULL,
  target_id TEXT NOT NULL,
  relation TEXT NOT NULL,
  metadata TEXT NOT NULL DEFAULT '{}',
  updated_at TEXT NOT NULL,
  PRIMARY KEY (source_kind, source_id, target_kind, target_id, relation)
);

CREATE INDEX IF NOT EXISTS resource_links_target_idx
  ON resource_links (target_kind, target_id);

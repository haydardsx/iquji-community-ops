CREATE TABLE IF NOT EXISTS monitoring_agents (
  id TEXT PRIMARY KEY,
  server_id TEXT NOT NULL UNIQUE,
  token_hash TEXT NOT NULL,
  version TEXT NOT NULL,
  protocol_version INTEGER NOT NULL,
  status TEXT NOT NULL,
  capabilities TEXT NOT NULL DEFAULT '[]',
  installed_at TEXT NOT NULL,
  last_heartbeat_at TEXT,
  last_sample_at TEXT,
  last_error TEXT,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS metric_samples (
  id TEXT PRIMARY KEY,
  server_id TEXT NOT NULL,
  target_kind TEXT NOT NULL,
  target_id TEXT NOT NULL,
  project_id TEXT,
  application_id TEXT,
  application_instance_id TEXT,
  swarm_service_id TEXT,
  swarm_task_id TEXT,
  sampled_at TEXT NOT NULL,
  "values" TEXT NOT NULL DEFAULT '{}',
  labels TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS metric_samples_sampled_at_idx ON metric_samples (sampled_at);
CREATE INDEX IF NOT EXISTS metric_samples_server_target_idx
  ON metric_samples (server_id, target_kind, target_id, sampled_at DESC);
CREATE INDEX IF NOT EXISTS metric_samples_application_idx
  ON metric_samples (application_id, sampled_at DESC);

CREATE TABLE IF NOT EXISTS metric_aggregates (
  id TEXT PRIMARY KEY,
  server_id TEXT NOT NULL,
  target_kind TEXT NOT NULL,
  target_id TEXT NOT NULL,
  project_id TEXT,
  application_id TEXT,
  application_instance_id TEXT,
  swarm_service_id TEXT,
  swarm_task_id TEXT,
  "window" TEXT NOT NULL,
  bucket_start TEXT NOT NULL,
  "values" TEXT NOT NULL DEFAULT '{}',
  labels TEXT NOT NULL DEFAULT '{}',
  sample_count INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  UNIQUE (server_id, target_kind, target_id, "window", bucket_start)
);

CREATE INDEX IF NOT EXISTS metric_aggregates_bucket_idx
  ON metric_aggregates (bucket_start DESC);

CREATE TABLE IF NOT EXISTS swarm_snapshots (
  id TEXT PRIMARY KEY,
  server_id TEXT NOT NULL,
  sampled_at TEXT NOT NULL,
  nodes TEXT NOT NULL DEFAULT '[]',
  services TEXT NOT NULL DEFAULT '[]',
  tasks TEXT NOT NULL DEFAULT '[]',
  networks TEXT NOT NULL DEFAULT '[]',
  summary TEXT NOT NULL,
  status TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS swarm_snapshots_server_sampled_idx
  ON swarm_snapshots (server_id, sampled_at DESC);

CREATE TABLE IF NOT EXISTS frp_probe_hits (
  id TEXT PRIMARY KEY,
  tunnel_id TEXT NOT NULL,
  run_id TEXT NOT NULL,
  mapping_name TEXT NOT NULL,
  method TEXT NOT NULL,
  host TEXT NOT NULL DEFAULT '',
  path TEXT NOT NULL,
  raw_query TEXT NOT NULL DEFAULT '',
  remote_addr TEXT NOT NULL,
  user_agent TEXT NOT NULL DEFAULT '',
  received_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS frp_probe_hits_tunnel_received_idx
  ON frp_probe_hits (tunnel_id, received_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS frp_probe_hits_run_received_idx
  ON frp_probe_hits (run_id, received_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS frp_probe_stats (
  tunnel_id TEXT PRIMARY KEY,
  hit_count INTEGER NOT NULL DEFAULT 0,
  last_hit_at TEXT,
  cleared_at TEXT,
  updated_at TEXT NOT NULL
);

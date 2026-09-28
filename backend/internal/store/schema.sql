-- SPDX-License-Identifier: Apache-2.0
-- Snapshot log: one row per time-travel snapshot. Payloads are JSONB;
-- counts denormalized so List stays cheap. Plain PostgreSQL works as-is;
-- when the TimescaleDB extension is present the table becomes a hypertable
-- (composite PK keeps both legal: Timescale requires the time column in
-- unique constraints).
CREATE TABLE IF NOT EXISTS snapshots (
  id         TEXT NOT NULL,
  ts         TIMESTAMPTZ NOT NULL,
  label      TEXT NOT NULL DEFAULT '',
  node_count INT NOT NULL DEFAULT 0,
  edge_count INT NOT NULL DEFAULT 0,
  nodes      JSONB NOT NULL,
  edges      JSONB NOT NULL,
  -- composite PK: Timescale requires the partitioning column in every
  -- unique constraint; id stays leftmost so Get-by-id uses the index.
  PRIMARY KEY (id, ts)
);
CREATE INDEX IF NOT EXISTS snapshots_ts ON snapshots (ts);
DO $$
BEGIN
  PERFORM 1 FROM pg_extension WHERE extname = 'timescaledb';
  IF FOUND THEN
    PERFORM create_hypertable('snapshots', 'ts', if_not_exists => TRUE);
  END IF;
EXCEPTION WHEN OTHERS THEN
  -- no extension, no permissions, already a hypertable: plain table is fine
  NULL;
END
$$;

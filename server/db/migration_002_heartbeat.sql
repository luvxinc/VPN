-- Migration 002: add last_heartbeat_at to sessions for real-time online status
-- Run: psql weiai_vpn < db/migration_002_heartbeat.sql

ALTER TABLE sessions
  ADD COLUMN IF NOT EXISTS last_heartbeat_at TIMESTAMPTZ DEFAULT NOW();

-- Backfill: set existing active sessions' heartbeat to connected_at
-- (they will expire naturally after 3 minutes if the client doesn't ping)
UPDATE sessions SET last_heartbeat_at = connected_at WHERE last_heartbeat_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_sessions_heartbeat ON sessions(last_heartbeat_at) WHERE is_active = true;

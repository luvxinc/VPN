-- Migration 001: add per-user speed limits and traffic quota
-- Run: psql weiai_vpn < db/migration_001_user_limits.sql

ALTER TABLE users
  ADD COLUMN IF NOT EXISTS speed_limit_up_kbps   INT    DEFAULT NULL,
  ADD COLUMN IF NOT EXISTS speed_limit_down_kbps INT    DEFAULT NULL,
  ADD COLUMN IF NOT EXISTS quota_bytes           BIGINT DEFAULT NULL,
  ADD COLUMN IF NOT EXISTS quota_period          TEXT   DEFAULT NULL
    CHECK (quota_period IN ('daily', 'weekly', 'monthly'));

-- WeiAi VPN — PostgreSQL Schema
-- Run: psql weiai_vpn < db/schema.sql

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- VPN users
CREATE TABLE IF NOT EXISTS users (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username             VARCHAR(64) UNIQUE NOT NULL,
    password_hash        VARCHAR(128) NOT NULL,
    is_active            BOOLEAN DEFAULT true,
    created_at           TIMESTAMP DEFAULT NOW(),
    notes                TEXT,
    speed_limit_up_kbps   INT    DEFAULT NULL,
    speed_limit_down_kbps INT    DEFAULT NULL,
    quota_bytes           BIGINT DEFAULT NULL,
    quota_period          TEXT   DEFAULT NULL CHECK (quota_period IN ('daily', 'weekly', 'monthly'))
);

-- Registered devices (max 1 active per user at a time)
CREATE TABLE IF NOT EXISTS devices (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id            UUID REFERENCES users(id) ON DELETE CASCADE,
    device_fingerprint VARCHAR(64) UNIQUE NOT NULL,
    device_name        VARCHAR(128),
    registered_at      TIMESTAMP DEFAULT NOW(),
    last_seen          TIMESTAMP,
    is_active          BOOLEAN DEFAULT true
);

-- Connection sessions (one active per device at a time)
CREATE TABLE IF NOT EXISTS sessions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID REFERENCES users(id),
    device_id       UUID REFERENCES devices(id),
    vless_uuid      VARCHAR(36) NOT NULL,
    login_ip        INET NOT NULL,
    login_country   VARCHAR(64),
    login_city      VARCHAR(64),
    connected_at    TIMESTAMP DEFAULT NOW(),
    disconnected_at TIMESTAMP,
    upload_bytes    BIGINT DEFAULT 0,
    download_bytes  BIGINT DEFAULT 0,
    is_active       BOOLEAN DEFAULT true
);

-- Access log: domain/IP per session, aggregated to 1-hour buckets
CREATE TABLE IF NOT EXISTS access_log (
    id             BIGSERIAL PRIMARY KEY,
    user_id        UUID REFERENCES users(id),
    session_id     UUID REFERENCES sessions(id),
    host           VARCHAR(253) NOT NULL,
    access_hour    TIMESTAMP NOT NULL,
    request_count  INT DEFAULT 1,
    upload_bytes   BIGINT DEFAULT 0,
    download_bytes BIGINT DEFAULT 0,
    UNIQUE (session_id, host, access_hour)
);

-- Daily traffic summary (aggregated from sessions each night)
CREATE TABLE IF NOT EXISTS traffic_daily (
    user_id        UUID REFERENCES users(id),
    date           DATE NOT NULL,
    upload_bytes   BIGINT DEFAULT 0,
    download_bytes BIGINT DEFAULT 0,
    PRIMARY KEY (user_id, date)
);

-- Admin users (completely separate from VPN users)
CREATE TABLE IF NOT EXISTS admin_users (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username      VARCHAR(64) UNIQUE NOT NULL,
    password_hash VARCHAR(128) NOT NULL,
    created_at    TIMESTAMP DEFAULT NOW()
);

-- Indexes for common query patterns
CREATE INDEX IF NOT EXISTS idx_sessions_user_active     ON sessions(user_id, is_active);
CREATE INDEX IF NOT EXISTS idx_sessions_device_active   ON sessions(device_id, is_active);
CREATE INDEX IF NOT EXISTS idx_sessions_connected_at    ON sessions(connected_at DESC);
CREATE INDEX IF NOT EXISTS idx_access_log_user_hour     ON access_log(user_id, access_hour DESC);
CREATE INDEX IF NOT EXISTS idx_access_log_session       ON access_log(session_id);
CREATE INDEX IF NOT EXISTS idx_traffic_daily_user_date  ON traffic_daily(user_id, date DESC);
CREATE INDEX IF NOT EXISTS idx_devices_user             ON devices(user_id);
CREATE INDEX IF NOT EXISTS idx_devices_fingerprint      ON devices(device_fingerprint);

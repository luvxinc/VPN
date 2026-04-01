-- Migration 003: Add stable vless_uuid per device
-- Borrowed from v2ray-core's per-user-entity UUID philosophy.
-- Each device gets a permanent VLESS UUID assigned at registration time.
-- Normal logins reuse this UUID → zero sing-box reloads needed.
-- Kick operations rotate it → immediate tunnel invalidation.

-- Step 1: Add the column (nullable initially to allow back-fill)
ALTER TABLE devices
    ADD COLUMN IF NOT EXISTS vless_uuid VARCHAR(36);

-- Step 2: Back-fill existing rows with a fresh UUID
UPDATE devices
SET vless_uuid = gen_random_uuid()::text
WHERE vless_uuid IS NULL;

-- Step 3: Enforce NOT NULL + unique
ALTER TABLE devices
    ALTER COLUMN vless_uuid SET NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_devices_vless_uuid ON devices(vless_uuid);

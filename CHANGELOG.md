# Changelog

All notable changes to WeiAi VPN are documented here.

Version format: `MAJOR.MINOR.PATCH`
- **MAJOR** — breaking protocol change (both client and server must update together)
- **MINOR** — new feature, backward-compatible
- **PATCH** — bug fix, no API change

---

## [1.1.13] — 2026-04-01

### Server — Architecture Improvement (v2ray-core Inspiration)

**P1: Stable per-device VLESS UUID (0 ms login latency)**
- Borrowed from v2ray-core's `Validator.Add/Del` philosophy: each device gets a
  **permanent** `vless_uuid` (stored in `devices.vless_uuid`). Normal re-logins
  reuse the existing UUID — zero sing-box config update, zero SIGHUP, ~0 ms overhead.
  Previously every login triggered a SIGHUP (~400 ms).
- First-time device registration: generates UUID, stores in DB, calls `SyncUsers` once.
- `KickUser`: rotates UUID for each kicked device via `RotateDeviceUUID`, then calls
  `SyncUsers` to push the full active-user pool to sing-box with one SIGHUP.
  **Security fix**: kicked users' VPN tunnels now terminate immediately (previously
  only Redis keys were deleted; the old UUID remained live in sing-box).
- `singbox.go`: upgraded `UpdateUUID` → `SyncUsers(configPath, []DeviceUser)`.
  Now writes all active device UUIDs into every VLESS inbound atomically.
  `UpdateUUID` remains as a backward-compatible single-user wrapper.
- DB migration `migration_003_device_uuid.sql`: adds `devices.vless_uuid` column,
  back-fills existing rows with `gen_random_uuid()`, enforces UNIQUE constraint.

**P2: ClashPoller unchanged (existing /connections streaming is efficient)**
- Confirmed that the current `/connections` poll-with-delta approach is already
  comparable to v2ray-core's stats channels. No regression; deferred to a future PR.

**P3: Outbound health monitor**
- New `background.OutboundHealthMonitor` (inspired by v2ray-core's `observatory`).
  Probes configurable outbound tags (e.g. `ws-cdn`, `reality-direct`) every 30 s
  via Clash API `/proxies/{tag}/delay`. Tracks consecutive failures; marks outbound
  as `degraded` (≥2) or `down` (≥5).
- New admin API endpoint: `GET /admin/api/health` — returns JSON with per-outbound
  latency, status, and an aggregate `status` field (`ok` / `degraded` / `down`).
- Configure via `sing_box.outbound_tags` in `config.yaml`.

### Tests
- 4 new singbox tests: `TestSyncUsers_*` covering multi-user write, Reality/WS flow
  distinction, backward compatibility, and mixed-inbound scenarios. **29/29 PASS**.

---

## [1.1.12] — 2026-03-31

### Client (macOS)
- **Fix: login timeout (connection refused)** — `waitForProxyReady` timeout extended
  20 s → 40 s. Added a 5 s server-restart buffer after Phase 1 (local Clash API up)
  so Phase 2 delay tests no longer hit the server's sing-box restart window and fail
  with `connection refused`. Root cause: every login triggers `pkill sing-box` +
  re-launch on the server (~3–5 s), but Phase 2 fired immediately after Phase 1.

### Server
- **Fix: sing-box hot-reload on login** — `UpdateUUID` now sends `SIGHUP` to the
  running sing-box process after atomically rewriting the config, triggering a
  config reload in ~100-300 ms instead of a full kill+restart (~3-5 s). This
  eliminates the restart race window that caused Phase 2 delay tests to fail.
  Note: sing-box's Clash API is read-only (unlike v2ray-core's gRPC user manager);
  SIGHUP is the correct hot-reload primitive. Falls back to `pkill` if the PID
  cannot be found via `pgrep`.
- `main.go` wires `cfg.SingBox.ClashAPIURL` → `singbox.SetClashAPIURL` at startup
  (used in PID-resolution fallback path).

---


## [1.1.9] — 2026-03-31

### Client (macOS)
- **Fix: no internet after login (definitive fix)** — `v1.1.8` had two residual bugs:
  1. Helper script version-blind: `isInstalled` only checked file presence, so the
     new `weiai-helper.sh` was never deployed to existing users; old helper returned
     immediately, bypassing the Clash API readiness polling entirely.
     Now compares `UserDefaults["helperAppVersion"]` to `AppVersion.current` and
     force-reinstalls on any version mismatch.
  2. Clash API `/version` ≠ tunnel working: sing-box's control plane comes up in
     ~100 ms, long before the VLESS Reality handshake completes (1–5 s). Added a
     second phase (`waitForProxyReady`) that calls the Clash API **delay-test
     endpoint** (`/proxies/{tag}/delay`) — traffic actually flows through the VLESS
     outbound — before `isConnected` is set to `true`.

---

## [1.1.8] — 2026-03-31

### Client (macOS)
- **Fix: no internet after login** — sing-box was declared "connected" the moment its
  process started, but `strict_route: true` had already captured all traffic before the
  VLESS-Reality handshake completed, leaving the user with no internet access.
  - Added `experimental.clash_api` (port 9091) to the client sing-box config.
  - `weiai-helper.sh` now polls the Clash API after launch and blocks until sing-box is
    fully ready (up to 12 s) before returning exit 0. The app only sets `isConnected =
    true` after the helper confirms readiness.
  - Existing users must reinstall the client (or delete `/usr/local/bin/weiai-helper`)
    so the new helper script takes effect.

---

## [1.0.0] — 2026-03-30

Initial production release. Complete rewrite from single-user prototype.

### Server
- Multi-user PostgreSQL backend (users, devices, sessions, access_log, traffic_daily)
- JWT authentication (15-minute access tokens + 24-hour refresh tokens)
- bcrypt password hashing, Redis-backed rate limiting (5 req / 15 min per IP)
- Device fingerprint registration with admin-issued 8-char verification codes (15-min TTL)
- Admin dashboard (LAN-only): user CRUD, live session monitor, domain logs, traffic stats
- Clash API polling every 10s for structured domain/IP logging
- GeoIP lookup (MaxMind GeoLite2) for login location
- Nightly 3am cleanup: 90-day log retention, traffic_daily aggregation
- sing-box UUID rotated per session; invalidated on disconnect/kick
- Config loaded from `config.yaml` (gitignored) with `config.example.yaml` committed

### Client (macOS)
- Username/password login UI with Keychain storage
- New device flow: admin generates verification code, user enters it once
- Certificate pinning loaded from `config.json` (not hardcoded)
- Kill switch: null routes block all traffic on unexpected VPN drop
  - NSAlert popup with reconnect / quit options
  - macOS notification
  - State persists across app restarts via `/tmp/weiai_ks_active`
- Startup check: detects lingering kill switch from previous crash
- Quit confirmation when kill switch is active

### Testing
- 27 pytest tests (auth, admin, log_manager)
- 17 Swift unit tests (Config, Keychain, KillSwitch)

---

## Versioning Quick Reference

**How to bump the version:**

1. Edit `server/version.py` → `VERSION`
2. Edit `client/Sources/WeiAiApp/Version.swift` → `AppVersion.current`
3. Edit `client/build.sh` → `VERSION`
4. Add entry to this file
5. Rebuild client (`./build.sh`) and redeploy server

**When to bump MAJOR:**
Any change to the `/connect` or `/verify-device` request/response schema that breaks
existing clients. Both client and server must be updated simultaneously.

**When to bump MINOR:**
New admin features, new optional API fields, new client UI features.

**When to bump PATCH:**
Bug fixes, performance improvements, no protocol change.

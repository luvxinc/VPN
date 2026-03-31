# Changelog

All notable changes to WeiAi VPN are documented here.

Version format: `MAJOR.MINOR.PATCH`
- **MAJOR** — breaking protocol change (both client and server must update together)
- **MINOR** — new feature, backward-compatible
- **PATCH** — bug fix, no API change

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

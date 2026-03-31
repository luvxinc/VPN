# 为爱鼓掌 VPN

A self-hosted VPN system for macOS, built on [sing-box](https://github.com/SagerNet/sing-box) with VLESS + XTLS-Reality transport. Designed for small teams or personal use: one server, multiple named users, every device registered before it can connect.

```
[macOS Client]  ──HTTPS/JWT──>  [Go Server :9443]  ──manages──>  [sing-box :443]
   sing-box (TUN)                 PostgreSQL + Redis               VLESS+Reality
   kill switch                    admin dashboard                  Clash API
   auto-update                    per-user limits
```

**Go server performance vs previous Python/FastAPI:**

| Metric | Python | Go |
|--------|--------|----|
| Memory | ~120 MB | ~18 MB |
| Auth p99 latency | ~8 ms | ~0.8 ms |
| Startup time | ~1.8 s | ~45 ms |
| Deployment | runtime + pip deps | single static binary |

---

## Features

- **Named users + device registration** — users need an admin-issued code to register a new Mac
- **JWT authentication** — 15-minute access tokens, 24-hour refresh tokens, stored in macOS Keychain
- **Per-user speed limits** — configurable upload/download limits (Kbps) per user, enforced by sing-box
- **Per-user traffic quotas** — daily/weekly/monthly data caps; auto-disconnects when exceeded, resets at next period
- **Real-time policy push** — admin changes take effect within 30 seconds; client polls `/status` and shows updated limits immediately
- **Admin dashboard** — create/disable users, kick sessions, generate registration codes, set speed limits and quotas, view logs and traffic (HTMX + Tailwind, LAN-only, all actions use HTML modals — no native browser dialogs)
- **Domain access log** — every domain/IP a user visits is recorded via Clash API polling, aggregated by hour, retained 90 days
- **Kill switch** — if VPN drops unexpectedly, all internet traffic is blocked until the user reconnects or explicitly restores
- **In-app auto-update** — when the server rejects an outdated client (HTTP 426), the app downloads the new zip, shows a progress bar, replaces itself, and relaunches — no browser required
- **Certificate pinning** — client rejects any TLS certificate that doesn't match the pinned SHA-256 fingerprint (applied to both auth and update downloads)
- **Client version gate** — server rejects outdated clients with HTTP 426 and serves the upgrade zip at `/download/client`
- **Menu bar stats** — shows upload/download speed, quota usage (e.g. `345G/1024G`), time until next reset, and server latency (TCP ping)
- **i18n** — client UI supports English and Simplified Chinese; follows system locale, falls back to English
- **GeoIP** — login country and city recorded per session (MaxMind GeoLite2)
- **CI/CD** — GitHub Actions builds both the Go server and the client zip on every push to `main`

---

## Prerequisites

### Server (Mac Mini or any macOS/Linux machine)

| Requirement | Version | Notes |
|-------------|---------|-------|
| Go | 1.23+ | `go version` |
| PostgreSQL | 15+ | `brew install postgresql@15` |
| Redis | 7+ | `brew install redis` |
| sing-box | 1.13+ | See below |
| MaxMind GeoLite2-City.mmdb | any | Free download, see below |

### Client build machine (macOS only)

| Requirement | Version |
|-------------|---------|
| Xcode / Swift | 6.2+ (`swift --version`) |
| macOS SDK | 13.0+ target |

---

## Server Setup

### 1. Install sing-box

```bash
brew install sing-box
# or download from https://github.com/SagerNet/sing-box/releases
sing-box version
```

### 2. Generate TLS certificate

The client pins the server certificate by SHA-256 fingerprint. Generate a self-signed cert:

```bash
cd server
bash gen_certs.sh YOUR_SERVER_IP
```

This writes `certs/server.crt` and `certs/server.key`, then prints the SHA-256 fingerprint. **Copy that fingerprint** — you need it when building the client.

To print the fingerprint later:
```bash
openssl x509 -in server/certs/server.crt -fingerprint -sha256 -noout \
  | sed 's/SHA256 Fingerprint=//' | tr -d ':'
```

### 3. Generate VLESS Reality key pair

```bash
sing-box generate reality-keypair
```

Note the `PublicKey` and `PrivateKey`. Pick a short ID (8 hex chars, e.g. `a1b2c3d4`).

### 4. Configure sing-box server

```bash
cp server/sing-box-server.example.json server/sing-box-server.json
```

Edit `server/sing-box-server.json`. The important parts:

```json
{
  "inbounds": [{
    "type": "vless",
    "listen": "0.0.0.0",
    "listen_port": 443,
    "users": [{"uuid": "PLACEHOLDER", "flow": "xtls-rprx-vision"}],
    "tls": {
      "enabled": true,
      "reality": {
        "enabled": true,
        "private_key": "YOUR_PRIVATE_KEY",
        "short_id": ["YOUR_SHORT_ID"],
        "handshake": {"server": "www.apple.com", "server_port": 443}
      }
    }
  }],
  "experimental": {
    "clash_api": {
      "external_controller": "127.0.0.1:9090",
      "secret": ""
    }
  }
}
```

The `users` array is managed automatically by the auth server — the placeholder UUID is replaced when a user connects.

### 5. Create PostgreSQL database

```bash
brew services start postgresql@15
createdb weiai_vpn
psql weiai_vpn < server/db/schema.sql
```

### 6. Start Redis

```bash
brew services start redis
redis-cli ping   # should print PONG
```

### 7. Download GeoIP database

1. Create a free MaxMind account at https://dev.maxmind.com/geoip/geolite2-free-geolocation-data
2. Download **GeoLite2-City.mmdb**
3. Place it at `server/GeoLite2-City.mmdb`

### 8. Create server config

```bash
cp server/config.example.yaml server/config.yaml
```

Edit `server/config.yaml` — fill in your server IP, Reality keys, and generate an admin password hash:

```bash
htpasswd -bnBC 10 "" yourpassword | tr -d ':\n'
```

Paste the output into `admin.password_hash` in config.yaml. Also set `client.client_zip_path` to the absolute path of your client zip (e.g. `/Users/you/Developer/VPN/client/dist/为爱鼓掌.zip`).

**`config.yaml` is gitignored** — it contains your private keys and password hash. Never commit it.

### 9. Build the Go server

```bash
cd server/go
go build -ldflags="-s -w" -o ../authserver .
```

This produces a single static binary at `server/authserver` (~25 MB).

### 10. Start sing-box

```bash
sing-box run -c server/sing-box-server.json
```

### 11. Start the auth server

```bash
WEIAI_CONFIG=/path/to/server/config.yaml server/authserver
```

Verify:
```bash
curl -k https://localhost:9443/health
# {"status":"ok","version":"1.0.0"}
```

### 12. Run as a LaunchAgent (auto-start on login)

```bash
# Edit the plist — replace YOUR_USERNAME and set WEIAI_CONFIG path
cp server/launchagents/com.weiai.authserver.plist ~/Library/LaunchAgents/
launchctl load ~/Library/LaunchAgents/com.weiai.authserver.plist
```

---

## Admin Dashboard

The admin dashboard is available at `https://YOUR_SERVER_IP:9443/admin` — **LAN only**. Requests from outside your local network are rejected with HTTP 403.

### User Management (`/admin/users`)

| Action | What it does |
|--------|-------------|
| **Create user** | Add username + password; active immediately |
| **Disable user** | Blocks login; active session is kicked |
| **Change password** | Takes effect on next login |
| **Generate code** | 8-char code valid 15 minutes; give it to the user |
| **Kick** | Force-disconnects the active session |
| **Limits** | Set per-user upload/download speed cap (Kbps) and data quota (GB/daily/weekly/monthly) |
| **Delete** | Removes user and all their data (confirmation modal) |

Speed and quota badges are shown inline in the user list. Changes push to the client within 30 seconds via the `/status` polling endpoint.

### Dashboard (`/admin/dashboard`)

Live view of active sessions — who is connected, from where, how long, upload/download. Auto-refreshes every 5 seconds via HTMX.

### Logs (`/admin/logs`)

Browse domain/IP access per user per day. Filter by user and date range.

### Stats (`/admin/stats`)

Daily traffic breakdown per user. Filter by user and time period.

---

## Client Build

### 1. Configure the client

```bash
cp client/Resources/config.example.json client/Resources/config.json
```

Edit `client/Resources/config.json`:

```json
{
  "auth_url": "https://YOUR_SERVER_IP:9443",
  "cert_fingerprint": "THE_SHA256_FINGERPRINT_FROM_STEP_2"
}
```

The fingerprint is 64 uppercase hex characters with no colons.

**`config.json` is gitignored** — never commit it.

### 2. Build

```bash
cd client
bash build.sh
```

Compiles the Swift app, bundles sing-box, signs it, copies `.lproj` localization folders, and produces `dist/为爱鼓掌.zip`.

### 3. Distribute

Send `dist/为爱鼓掌.zip` to your users. They drag `为爱鼓掌.app` to `/Applications` and launch it. First time on a new Mac, they will be prompted for a registration code from the admin dashboard.

---

## Kill Switch

If sing-box crashes or exits unexpectedly, all internet traffic is blocked using null routes until the user acts.

- **On unexpected disconnect:** traffic routes to loopback; system notification + alert with **Reconnect** or **Quit and restore network**
- **On quit with kill switch active:** confirmation dialog before restoring the network
- **On app launch with kill switch leftover:** startup prompt to reconnect or restore

---

## Auto-Update

When the server's `MIN_CLIENT_VERSION` is bumped above the client's version:

1. Client receives HTTP 426 with a `download_url` pointing to `/download/client`
2. App switches to an update view with a progress bar
3. Client downloads the zip (with the same cert pinning as auth)
4. Zip is extracted to `/tmp/weiai-update/`, a detached install script replaces the running bundle
5. App quits and the new version launches automatically

To force an update:
1. Bump `MIN_CLIENT_VERSION` in `server/go/version.go`
2. Build and place the new client zip (CI does this automatically on push)
3. Rebuild and restart the auth server

---

## Running Tests

```bash
cd server/go

# Unit tests (no network required)
go test ./tests/config/... ./tests/auth/... ./tests/singbox/... \
        ./tests/geoip/... ./tests/middleware/... ./tests/background/...

# Integration tests (requires PostgreSQL weiai_vpn_test + Redis DB 1)
createdb weiai_vpn_test && psql weiai_vpn_test < ../db/schema.sql
go test -tags=integration -v ./tests/integration/
```

---

## Version Management

Versions follow **MAJOR.MINOR.PATCH**:

| Change | Bump |
|--------|------|
| Breaking protocol change (client must update) | MAJOR |
| New feature, backward-compatible | MINOR |
| Bug fix, no API change | PATCH |

**Server version** is in `server/go/version.go`:
```go
const VERSION = "1.0.0"
const MIN_CLIENT_VERSION = "1.0.0"
```

**Client version** is in `client/Sources/WeiAiApp/Version.swift`.

---

## Security Notes

| Feature | Detail |
|---------|--------|
| **Transport** | VLESS + XTLS-Reality; looks like normal TLS to port 443 |
| **Certificate pinning** | Client verifies server cert SHA-256 fingerprint on both auth and update downloads |
| **JWT** | HS256, 15-minute expiry; refresh token in Redis with 24-hour TTL |
| **Keychain storage** | Tokens, username, and password in macOS Keychain — not on disk |
| **Rate limiting** | 5 auth attempts per 15 minutes per IP (Redis sliding window) |
| **Admin isolation** | Admin endpoints require LAN IP + separate JWT with different secret suffix |
| **Device pinning** | Each device registered once; new Mac requires admin-issued time-limited code |
| **Kill switch** | Null routes block all traffic on unexpected disconnect |

---

## Project Structure

```
├── server/
│   ├── go/                          Go server (auth + admin + background tasks)
│   │   ├── main.go                  Entry point (Fiber v2, port 9443)
│   │   ├── version.go               Server and min-client version constants
│   │   ├── config/config.go         Config YAML loader
│   │   ├── models/models.go         Shared data types (UserPolicy, PolicyStatus)
│   │   ├── i18n/i18n.go             EN/中文 string maps, per-request switching
│   │   ├── store/
│   │   │   ├── db.go                PostgreSQL helpers (pgx/v5, quota + limits queries)
│   │   │   └── redis.go             Redis helpers (policy_changed flag, session store)
│   │   ├── auth/
│   │   │   ├── jwt.go               User + admin JWT (HS256)
│   │   │   ├── bcrypt.go            Password hashing
│   │   │   └── version.go           Client version check → HTTP 426
│   │   ├── handlers/
│   │   │   ├── api.go               /connect /disconnect /refresh /verify-device /status
│   │   │   ├── admin.go             /admin/** including /users/:id/limits
│   │   │   └── health.go            /health /download/client
│   │   ├── middleware/middleware.go  RateLimit, RequireLAN, RequireAdminAuth
│   │   ├── singbox/singbox.go       Atomic sing-box config update
│   │   ├── geoip/geoip.go           MaxMind GeoLite2 lookup
│   │   ├── background/
│   │   │   ├── clash_poller.go      Polls Clash API every 10s → access_log
│   │   │   └── log_manager.go       Nightly cleanup + traffic_daily aggregation
│   │   ├── templates/               Go html/template files (HTMX + Tailwind CDN)
│   │   └── tests/                   Unit + integration tests
│   ├── db/
│   │   ├── schema.sql               PostgreSQL DDL (all tables + columns)
│   │   └── migration_001_user_limits.sql  Adds speed/quota columns to existing DBs
│   ├── gen_certs.sh                 Self-signed TLS cert generator
│   ├── config.example.yaml          Config template — safe to commit
│   ├── sing-box-server.example.json sing-box config template
│   └── launchagents/
│       ├── com.weiai.authserver.plist
│       └── com.sing-box.vpn.plist
│
└── client/
    ├── build.sh                     Build → sign → copy lproj → zip → dist/
    ├── Package.swift
    ├── Sources/WeiAiApp/
    │   ├── WeiAiApp.swift           App delegate, menu bar (SF Symbols, quota + latency)
    │   ├── MenuView.swift           Login UI, device code form, update progress view
    │   ├── VPNManager.swift         sing-box lifecycle, status polling, latency measurement
    │   ├── UpdateService.swift      In-app update: download → extract → replace → relaunch
    │   ├── AuthService.swift        HTTP auth, JWT, Keychain, cert pinning, /status fetch
    │   ├── KillSwitch.swift         Null-route kill switch via osascript
    │   ├── NetworkMonitor.swift     Real-time upload/download speed (menu bar)
    │   ├── KeychainHelper.swift     SecKeychain CRUD
    │   ├── Config.swift             Loads config.json from bundle
    │   ├── L.swift                  Localizable string lookup helper
    │   └── Version.swift            Version, release date, author
    └── Resources/
        ├── en.lproj/Localizable.strings
        ├── zh-Hans.lproj/Localizable.strings
        ├── config.json              Runtime config (gitignored)
        └── config.example.json      Template — safe to commit
```

---

## API Reference

All endpoints are on `https://YOUR_SERVER:9443`.

### Public

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Server status and version |
| POST | `/connect` | Login + get VPN config and user policy |
| POST | `/verify-device` | Register device with code + get VPN config |
| POST | `/disconnect` | End session |
| POST | `/refresh` | Exchange refresh token for new access token |
| GET | `/status?device_id=...` | Poll current policy (quota, speed limits, policy_changed flag) |
| GET | `/download/client` | Download latest client zip |

### Admin (LAN only, cookie auth)

| Method | Path | Description |
|--------|------|-------------|
| GET/POST | `/admin/login` | Admin login |
| GET | `/admin/dashboard` | Online users and live stats |
| GET/POST | `/admin/users` | List users / create user |
| POST | `/admin/users/:id/delete` | Delete user |
| POST | `/admin/users/:id/password` | Change password |
| POST | `/admin/users/:id/toggle` | Enable/disable user |
| POST | `/admin/users/:id/limits` | Set speed limits and quota |
| GET | `/admin/users/:id/verif-code` | Generate registration code |
| POST | `/admin/users/:id/kick` | Force disconnect |
| GET | `/admin/logs` | Access log browser |
| GET | `/admin/stats` | Traffic stats |

---

## Database Schema

```
users            — VPN users (username, password_hash, is_active,
                   speed_limit_up_kbps, speed_limit_down_kbps,
                   quota_bytes, quota_period)
devices          — Registered Macs per user (device_fingerprint, device_name)
sessions         — Connection records (vless_uuid, login_ip, country, upload/download)
access_log       — Domain/IP per session, aggregated to 1-hour buckets
traffic_daily    — Daily upload/download totals per user
```

---

## Changelog

### v1.1.0 — 2026-03-31

- Per-user upload/download speed limits (Kbps), configurable from admin dashboard
- Per-user traffic quotas (daily/weekly/monthly); auto-disconnect when exceeded, reset at next cycle
- Client menu bar: quota usage badge (`345G/1024G`), reset countdown, server latency (TCP ping)
- Real-time policy push: admin changes propagate to client within 30 seconds via Redis flag + `/status` polling
- Smooth in-app auto-update: progress bar download, silent bundle replacement, automatic relaunch
- Client i18n: English and Simplified Chinese, follows system locale
- Admin dashboard: all confirmations use HTML modals (no native browser dialogs)
- SF Symbols throughout macOS client (heart, bolt, chart, speedometer, power)
- CI/CD: GitHub Actions builds client zip on Mac Mini on every push

### v1.0.0 — 2026-03-30

- Initial release
- Go/Fiber server replacing Python/FastAPI (~18 MB memory, ~0.8 ms auth p99)
- macOS client with kill switch and certificate pinning

---

## License

MIT License. Copyright © 2026 Aaron Tong.

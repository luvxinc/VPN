# 为爱鼓掌 VPN

A self-hosted VPN system for macOS, built on [sing-box](https://github.com/SagerNet/sing-box) with VLESS + XTLS-Reality transport. Designed for small teams or personal use: one server, multiple named users, every device registered before it can connect.

```
[macOS Client]  ──HTTPS/JWT──>  [FastAPI Server :9443]  ──manages──>  [sing-box :443]
   sing-box (TUN)                  PostgreSQL + Redis                   VLESS+Reality
   kill switch                     admin dashboard                      Clash API
```

---

## Features

- **Named users + device registration** — users need an admin-issued code to register a new Mac
- **JWT authentication** — 15-minute access tokens, 24-hour refresh tokens, stored in macOS Keychain
- **Admin dashboard** — create/disable users, kick sessions, generate registration codes, view logs and traffic (HTMX + Tailwind, LAN-only)
- **Domain access log** — every domain/IP a user visits is recorded via Clash API polling, aggregated by hour, retained 90 days
- **Kill switch** — if VPN drops unexpectedly, all internet traffic is blocked until the user reconnects or explicitly restores
- **Certificate pinning** — client rejects any TLS certificate that doesn't match the pinned SHA-256 fingerprint
- **Client version gate** — server rejects outdated clients with HTTP 426 and serves the upgrade zip
- **GeoIP** — login country and city recorded per session (MaxMind GeoLite2)

---

## Prerequisites

### Server (Mac Mini or any macOS/Linux machine)

| Requirement | Version | Notes |
|-------------|---------|-------|
| Python | 3.12+ | `python3 --version` |
| PostgreSQL | 15+ | Homebrew: `brew install postgresql@15` |
| Redis | 7+ | Homebrew: `brew install redis` |
| sing-box | 1.13.4 | See below |
| OpenSSL | any | `brew install openssl` if not present |
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
# put the binary at /usr/local/bin/sing-box
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
openssl x509 -in server/certs/server.crt -fingerprint -sha256 -noout
```

### 3. Generate VLESS Reality key pair

```bash
sing-box generate reality-keypair
```

Note the `PublicKey` and `PrivateKey`. Pick a short ID (8 hex chars, e.g. `a1b2c3d4`).

### 4. Configure sing-box server

Copy the example and fill in your keys:

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

The `users` array is managed automatically by the auth server — the placeholder is replaced when a user connects.

### 5. Create PostgreSQL database

```bash
# Start PostgreSQL if not running
brew services start postgresql@15

createdb weiai_vpn
psql weiai_vpn < server/db/schema.sql
```

### 6. Configure Redis

```bash
# Start Redis if not running
brew services start redis
redis-cli ping   # should print PONG
```

Redis uses database 0 by default. The test suite uses database 1.

### 7. Download GeoIP database

1. Create a free MaxMind account at https://dev.maxmind.com/geoip/geolite2-free-geolocation-data
2. Download **GeoLite2-City.mmdb**
3. Place it at `server/GeoLite2-City.mmdb` (or update the path in config.yaml)

### 8. Create server config

```bash
cp server/config.example.yaml server/config.yaml
```

Edit `server/config.yaml`:

```yaml
database:
  url: "postgresql://YOUR_USER@localhost/weiai_vpn"
  pool_size: 10

redis:
  url: "redis://localhost:6379/0"

server:
  ip: "YOUR_SERVER_IP"          # public IP of this machine
  port: 443
  public_key: "YOUR_REALITY_PUBLIC_KEY"
  private_key: "YOUR_REALITY_PRIVATE_KEY"
  short_id: "YOUR_SHORT_ID"     # 8 hex chars
  server_name: "www.apple.com"  # SNI masquerade target

auth:
  jwt_secret: "CHANGE_ME_use_at_least_32_random_chars"
  jwt_expiry_minutes: 15
  refresh_expiry_hours: 24

admin:
  allowed_lan_prefixes: ["127.", "192.168.", "10."]
  username: "admin"
  # Generate: python3 -c "import bcrypt; print(bcrypt.hashpw(b'yourpassword', bcrypt.gensalt()).decode())"
  password_hash: ""

certs:
  cert_path: "certs/server.crt"
  key_path: "certs/server.key"

sing_box:
  config_path: "sing-box-server.json"
  binary_path: "/usr/local/bin/sing-box"
  clash_api_url: "http://127.0.0.1:9090"

geoip:
  db_path: "GeoLite2-City.mmdb"

log:
  retention_days: 90
  max_domains_per_user_per_day: 500

client:
  min_version: "1.0.0"
  download_url: "https://YOUR_SERVER_IP:9443/download/client"
  client_zip_path: "../client/dist/为爱鼓掌.zip"
```

Set the admin password hash:
```bash
python3 -c "import bcrypt; print(bcrypt.hashpw(b'yourpassword', bcrypt.gensalt()).decode())"
```
Paste the output into `admin.password_hash` in config.yaml.

**`config.yaml` is gitignored** — it contains your private keys and password hash. Never commit it.

### 9. Install Python dependencies

```bash
cd server
pip3 install -r requirements.txt
```

### 10. Start sing-box

```bash
sing-box run -c server/sing-box-server.json
```

### 11. Start the auth server

```bash
cd server
python3 auth_server.py
```

The server starts on port 9443 with TLS. Verify:
```bash
curl -k https://localhost:9443/health
# {"status":"ok","version":"1.0.0"}
```

### 12. Run as a LaunchAgent (optional, for auto-start)

A sample plist is in `server/launchagents/`. Copy and edit it:

```bash
cp server/launchagents/com.weiai.authserver.plist ~/Library/LaunchAgents/
# Edit the plist: set your username and paths
launchctl load ~/Library/LaunchAgents/com.weiai.authserver.plist
```

---

## Admin Dashboard

The admin dashboard is available at `https://YOUR_SERVER_IP:9443/admin` — **LAN only**. Requests from outside your local network are rejected with HTTP 403.

### Login

Navigate to `/admin/login` and enter the admin credentials from `config.yaml`.

### User Management (`/admin/users`)

| Action | What it does |
|--------|-------------|
| **Create user** | Add a username + password; user is active immediately |
| **Disable user** | Blocks login; active session is kicked |
| **Change password** | Takes effect on next login |
| **Generate code** | Produces an 8-char code (valid 15 minutes); give it to the user |
| **Kick** | Force-disconnects the active session |
| **Delete** | Removes user and all their data |

### Dashboard (`/admin/dashboard`)

- Active sessions: who is connected right now, from where, how long, upload/download
- Auto-refreshes every 5 seconds via HTMX

### Logs (`/admin/logs`)

Browse domain/IP access per user per day. Filter by user and date range.

### Stats (`/admin/stats`)

Daily traffic breakdown per user. Filter by user and time period (today / week / month / custom).

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

The fingerprint format is 64 uppercase hex characters with no colons, e.g.:
`6D9093AFB8DA20B4A7FA64525E4DA4310F17C48FC21C909B396FBE7DEDCEE3B1`

To get it from your certificate:
```bash
openssl x509 -in server/certs/server.crt -fingerprint -sha256 -noout \
  | sed 's/SHA256 Fingerprint=//' | tr -d ':'
```

**`config.json` is gitignored** — it contains your server IP. Never commit it.

### 2. Build

```bash
cd client
bash build.sh
```

This will:
1. Compile the Swift app (`swift build -c release`)
2. Download sing-box 1.13.4 for Apple Silicon (first run only)
3. Bundle everything into `dist/为爱鼓掌.app`
4. Sign with ad-hoc identity
5. Remove Gatekeeper quarantine flag
6. Zip to `dist/为爱鼓掌.zip`

### 3. Distribute to users

Send `dist/为爱鼓掌.zip` to your users. They:
1. Unzip → drag `为爱鼓掌.app` to `/Applications`
2. Double-click to launch — a ❤️ icon appears in the menu bar
3. Enter username + password
4. First time on a new Mac: the app shows "此设备未注册 — 请联系管理员获取验证码"
5. Admin generates a code from the dashboard; user enters it; device is registered

### 4. Upload client zip to server (for in-app upgrades)

```bash
cp dist/为爱鼓掌.zip ../server/../client/dist/为爱鼓掌.zip
# or set client.client_zip_path in config.yaml to the absolute path
```

When a user with an old client connects, the server returns HTTP 426, and the client shows a download button pointing to `/download/client`.

---

## Kill Switch

The macOS client has a kill switch: if sing-box crashes or exits unexpectedly, all internet traffic is blocked using null routes until the user acts.

**On unexpected disconnect:**
- All traffic routes to loopback (0.0.0.0/1 and 128.0.0.0/1 → 127.0.0.1)
- A system notification appears
- An alert offers: **Reconnect** or **Quit and restore network**

**On app quit with kill switch active:**
- A confirmation dialog asks whether to restore the network before quitting

**On app launch with kill switch left active:**
- The startup screen detects the leftover routes and asks: **Reconnect** or **Restore and quit**

Kill switch state is persisted to `/tmp/weiai_ks_state` between launches.

---

## Running Tests

```bash
cd server
pytest tests/ -v
```

Tests use a separate database (`weiai_vpn_test`) and Redis DB 1. They are created automatically on first run.

To clean up test state:
```bash
dropdb weiai_vpn_test
redis-cli -n 1 flushdb
```

---

## Version Management

Versions follow **MAJOR.MINOR.PATCH**:

| Change | Bump |
|--------|------|
| Breaking protocol change (client must update) | MAJOR |
| New feature, backward-compatible | MINOR |
| Bug fix, no API change | PATCH |

**Server version** is in `server/version.py`:
```python
VERSION = "1.0.0"           # reported in /health
MIN_CLIENT_VERSION = "1.0.0"  # rejects older clients
```

**Client version** is in `client/Sources/WeiAiApp/Version.swift`:
```swift
enum AppVersion {
    static let current     = "1.0.0"
    static let releaseDate = "2026-03-30"
    static let author      = "Aaron Tong"
}
```

When you bump the client version, also update `build.sh`:
```bash
VERSION="1.0.1"
```

When you need to force users to upgrade:
1. Bump `MIN_CLIENT_VERSION` in `server/version.py`
2. Build the new client, put the zip on the server
3. Restart the auth server

Old clients will get HTTP 426 and see an in-app prompt to download the new version.

---

## Security Notes

| Feature | Detail |
|---------|--------|
| **Transport** | VLESS + XTLS-Reality; looks like normal TLS to port 443 |
| **Certificate pinning** | Client verifies server cert SHA-256 fingerprint; rejects MITM |
| **JWT** | HS256, 15-minute expiry; refresh token stored in Redis with 24-hour TTL |
| **Keychain storage** | Access token, refresh token, username, and password stored in macOS Keychain — not in UserDefaults or on disk |
| **Rate limiting** | 5 auth attempts per 15 minutes per IP (Redis sliding window) |
| **Admin isolation** | Admin endpoints require both LAN IP and a separate JWT; admin JWT uses a different secret suffix |
| **Device pinning** | Each device is registered once; a new Mac requires an admin-issued time-limited code |
| **Kill switch** | Null routes block all traffic on unexpected disconnect |
| **API surface** | OpenAPI/docs endpoints are disabled; no route listing exposed |

---

## Project Structure

```
├── server/
│   ├── auth_server.py          Entry point (FastAPI, port 9443)
│   ├── version.py              Server and min-client version
│   ├── config_loader.py        Config YAML loader (singleton)
│   ├── config.yaml             Runtime config (gitignored)
│   ├── config.example.yaml     Template — safe to commit
│   ├── requirements.txt
│   ├── gen_certs.sh            Self-signed TLS cert generator
│   ├── sing-box-server.json         sing-box config (managed at runtime, gitignored)
│   ├── sing-box-server.example.json Template — safe to commit
│   ├── db/
│   │   └── schema.sql          PostgreSQL DDL
│   ├── routers/
│   │   ├── auth.py             /connect /disconnect /refresh /verify-device
│   │   └── admin.py            /admin/** (LAN only)
│   ├── services/
│   │   ├── clash_poller.py     Polls Clash API every 10s → access_log
│   │   ├── geoip.py            MaxMind GeoLite2 lookup
│   │   └── log_manager.py      Nightly cleanup + traffic_daily aggregation
│   ├── templates/              Jinja2 HTML (HTMX + Tailwind CDN)
│   ├── certs/                  TLS cert and key (gitignored)
│   └── tests/
│       ├── conftest.py
│       ├── test_auth.py        13 auth flow tests
│       ├── test_admin.py       11 admin API tests
│       └── test_log_manager.py 3 log aggregation tests
│
└── client/
    ├── build.sh                Build → sign → zip → dist/
    ├── Package.swift
    ├── Sources/WeiAiApp/
    │   ├── WeiAiApp.swift      App delegate, menu bar, kill switch startup check
    │   ├── MenuView.swift      Login UI, device code form, update prompt
    │   ├── VPNManager.swift    sing-box lifecycle, kill switch hooks
    │   ├── AuthService.swift   HTTP auth, JWT, Keychain, cert pinning
    │   ├── KillSwitch.swift    Null-route kill switch via osascript
    │   ├── KeychainHelper.swift SecKeychain CRUD
    │   ├── Config.swift        Loads config.json from ~/.config or bundle
    │   ├── Version.swift       Version, release date, author
    │   └── NetworkMonitor.swift
    ├── Resources/
    │   ├── config.json         Runtime config (gitignored)
    │   └── config.example.json Template — safe to commit
    └── Tests/WeiAiAppTests/
        ├── ConfigTests.swift
        ├── KeychainHelperTests.swift
        └── KillSwitchTests.swift
```

---

## API Reference

All endpoints are on `https://YOUR_SERVER:9443`.

### Public

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Server status and version |
| POST | `/connect` | Login + get VPN config |
| POST | `/verify-device` | Register device with code + get VPN config |
| POST | `/disconnect` | End session |
| POST | `/refresh` | Exchange refresh token for new access token |
| GET | `/download/client` | Download latest client zip |

### Admin (LAN only, requires cookie auth)

| Method | Path | Description |
|--------|------|-------------|
| GET/POST | `/admin/login` | Admin login |
| GET | `/admin/dashboard` | Online users and live stats |
| GET/POST | `/admin/users` | List users / create user |
| DELETE | `/admin/users/{id}` | Delete user |
| PATCH | `/admin/users/{id}/password` | Change password |
| PATCH | `/admin/users/{id}/active` | Enable/disable user |
| GET | `/admin/users/{id}/verif-code` | Generate registration code |
| POST | `/admin/users/{id}/kick` | Force disconnect |
| GET | `/admin/logs` | Access log browser |
| GET | `/admin/stats` | Traffic stats |

---

## Database Schema

```
users            — VPN users (username, password_hash, is_active)
devices          — Registered Macs per user (device_fingerprint, device_name)
sessions         — Connection records (vless_uuid, login_ip, country, upload/download)
access_log       — Domain/IP per session, aggregated to 1-hour buckets
traffic_daily    — Daily upload/download totals per user
admin_users      — Admin accounts (separate from VPN users)
```

---

## Changelog

See [CHANGELOG.md](CHANGELOG.md).

---

## License

MIT License. Copyright © 2026 Aaron Tong.

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT.

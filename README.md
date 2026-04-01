# 为爱鼓掌 VPN

A self-hosted VPN system for macOS, built on [sing-box](https://github.com/SagerNet/sing-box) with a multi-path transport architecture designed to be resilient against GFW deep-packet inspection and IP blocking.

```
                    ┌─────────────────────────────┐
                    │     Cloudflare CDN/Tunnel    │
                    │  (hides server IP, standard  │
                    │   CA cert, no GFW target)    │
                    └────────┬────────────-────────┘
                             │ HTTPS (standard port 443)
                 ┌───────────▼───────────────────┐
[macOS Client]   │   Go Auth Server  :443        │   ──manages──►  [sing-box]
  sing-box TUN   │   PostgreSQL + Redis           │                   :8443 VLESS+Reality
  kill switch    │   admin dashboard              │                   :8888 VLESS+WS
  auto-update    │   per-user limits + quotas     │
  cert pinning   └───────────────────────────────┘
       │                                                    ▲
       │ Path A: VLESS+Reality (direct, no CDN)             │
       │   Client → Server IP :8443                         │
       │   Looks like www.apple.com TLS to any observer     │
       │                                                     │
       │ Path B: VLESS+WebSocket via CDN (fallback)          │
       └──► Client → Cloudflare CDN :443 ──► Server :8888 ──┘
            Standard TLS (Cloudflare cert)
```

The client automatically picks the faster of the two VPN paths using `urltest`. When the direct IP is blocked, traffic transparently switches to the CDN path.

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
- **Certificate pinning** — client verifies the server's self-signed cert by SHA-256 fingerprint when connecting directly; CDN path uses standard CA validation (Cloudflare certificate)
- **Client version gate** — server rejects outdated clients with HTTP 426 and serves the upgrade zip at `/download/client`
- **Menu bar stats** — shows upload/download speed, quota usage (e.g. `345G/1024G`), time until next reset, and server latency (TCP ping)
- **i18n** — client UI supports English and Simplified Chinese; follows system locale, falls back to English
- **GeoIP** — login country and city recorded per session (MaxMind GeoLite2)
- **CI/CD** — GitHub Actions builds both the Go server and the client zip on every push to `main`

---

## Architecture Overview

### Ports

| Port | Service | Notes |
|------|---------|-------|
| `443` | Auth HTTP server (HTTPS) | Standard port; Cloudflare Tunnel forwards here |
| `8443` | sing-box VLESS+Reality | WAN-exposed; router NATs from `WAN:8443 → server:8443` |
| `8888` | sing-box VLESS+WebSocket | Plain HTTP (Cloudflare terminates TLS); router NATs from `WAN:8888 → server:8888` |
| `9090` | sing-box Clash API | LAN only; polled by auth server for traffic stats |

### Connection Flow

**Auth (login / connect):**
1. Client tries the CDN URL (`cdn_auth_url`) first — standard HTTPS, 12-second timeout.
2. If CDN is unreachable (network error), client falls back to the direct IP (`auth_url`) with certificate pinning.
3. On success, the server returns VPN config including WS fallback fields if configured.

**VPN traffic:**
1. Client generates a sing-box config with two outbounds: `reality-direct` (VLESS+Reality to server IP) and `ws-cdn` (VLESS+WebSocket to CDN domain).
2. A `urltest` outbound selects the lower-latency path, re-checking every 3 minutes.
3. Route bypass entries prevent the VPN traffic itself from looping back through the tunnel.

---

## Prerequisites

### Server (Mac Mini or any macOS/Linux machine)

| Requirement | Version | Notes |
|-------------|---------|-------|
| Go | 1.23+ | `go version` |
| PostgreSQL | 15+ | `brew install postgresql@15` |
| Redis | 7+ | `brew install redis` |
| sing-box | 1.13+ | See below |
| cloudflared | latest | Cloudflare Tunnel daemon |
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

The client pins the server certificate by SHA-256 fingerprint for direct (non-CDN) connections. Generate a self-signed cert valid for your server's public IP:

```bash
cd server
bash gen_certs.sh YOUR_SERVER_IP
```

This writes `certs/server.crt` and `certs/server.key`, then prints the SHA-256 fingerprint. **Copy that fingerprint** — you need it when building the client (`cert_fingerprint` in `config.json`).

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
  "inbounds": [
    {
      "type": "vless",
      "tag": "vless-reality-in",
      "listen": "0.0.0.0",
      "listen_port": 8443,
      "users": [{"uuid": "PLACEHOLDER", "flow": "xtls-rprx-vision"}],
      "tls": {
        "enabled": true,
        "server_name": "www.apple.com",
        "reality": {
          "enabled": true,
          "handshake": {"server": "www.apple.com", "server_port": 443},
          "private_key": "YOUR_PRIVATE_KEY",
          "short_id": ["YOUR_SHORT_ID"]
        }
      }
    },
    {
      "type": "vless",
      "tag": "vless-ws-in",
      "listen": "0.0.0.0",
      "listen_port": 8888,
      "users": [{"uuid": "PLACEHOLDER"}],
      "transport": {"type": "ws", "path": "/ws"}
    }
  ],
  "experimental": {
    "clash_api": {
      "external_controller": "127.0.0.1:9090",
      "secret": ""
    }
  }
}
```

**Notes:**
- The `users` array is managed automatically — the placeholder UUID is replaced on each user connection.
- The WS inbound (`vless-ws-in`) has no `tls` block — Cloudflare terminates TLS; sing-box receives plain HTTP.
- The WS inbound has no `flow` — `xtls-rprx-vision` is incompatible with WebSocket transport.

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

`config.yaml` full field reference:

```yaml
database:
  url: "postgresql://YOUR_USER@localhost/weiai_vpn"
  pool_size: 10

redis:
  url: "redis://localhost:6379/0"

server:
  ip: "YOUR_SERVER_IP"            # public IP written into VLESS configs issued to clients
  port: 8443                      # sing-box VLESS+Reality listen port
  auth_port: 443                  # auth HTTP server listen port (standard HTTPS)
  ws_port: 8888                   # sing-box VLESS+WebSocket listen port (CDN path)
  ws_fallback_domain: ""          # CDN domain proxying WS traffic, e.g. "vpn.yourdomain.com"
                                  # Leave empty to disable WS fallback (single-path mode)
  public_key: "..."               # from: sing-box generate reality-keypair
  private_key: "..."
  short_id: "a1b2c3d4"           # 8 hex chars
  server_name: "www.apple.com"   # SNI masquerade host for Reality

auth:
  jwt_secret: "32+ random chars"
  jwt_expiry_minutes: 15
  refresh_expiry_hours: 24

admin:
  allowed_lan_prefixes:
    - "127."
    - "192.168."
    - "10."
    - "172.16."
  username: "admin"
  password_hash: ""               # generate below

certs:
  cert_path: "certs/server.crt"
  key_path:  "certs/server.key"

sing_box:
  config_path: "sing-box-server.json"
  binary_path: "/opt/homebrew/bin/sing-box"   # or: /usr/local/bin/sing-box
  clash_api_url: "http://127.0.0.1:9090"

geoip:
  db_path: "GeoLite2-City.mmdb"

log:
  retention_days: 90
  max_domains_per_user_per_day: 500

client:
  min_version: "1.0.0"           # clients older than this get HTTP 426
  download_url: "https://yourdomain.com/download/client"
  client_zip_path: "../client/dist/为爱鼓掌.zip"
```

**Key fields:**

| Field | Required | Description |
|-------|----------|-------------|
| `server.ip` | Yes | Public IP. Written into `vless_config.server` returned to clients. |
| `server.port` | Yes | sing-box Reality port. Default `8443`. |
| `server.auth_port` | No | Auth server HTTPS port. Default `443`. |
| `server.ws_port` | No | sing-box WebSocket port. Default `8888`. |
| `server.ws_fallback_domain` | No | CDN hostname for WS path. Leave empty to disable. |
| `client.download_url` | Yes | URL shown when client is outdated. Use your CDN domain if configured. |
| `client.client_zip_path` | Yes | Path to `为爱鼓掌.zip` served at `/download/client`. |

Generate admin password hash:

```bash
htpasswd -bnBC 10 "" yourpassword | tr -d ':\n'
# or without Apache tools:
go run golang.org/x/crypto/bcrypt@latest yourpassword
```

**`config.yaml` is gitignored** — it contains private keys and the password hash. Never commit it.

### 9. Set up Cloudflare Tunnel (optional but recommended)

Cloudflare Tunnel routes HTTPS traffic to your server through Cloudflare's network without exposing your IP. This hides your server's IP address, provides a trusted CA certificate (no custom cert pinning needed on the CDN path), and makes the auth endpoint harder to block.

**Prerequisites:** A Cloudflare account with your domain added (free plan is sufficient).

#### 9a. Install cloudflared

```bash
brew install cloudflared
```

#### 9b. Configure tunnel

In the Cloudflare dashboard, go to **Zero Trust → Networks → Tunnels → Create a tunnel**. Copy the tunnel token.

On the server, create a LaunchAgent to run cloudflared continuously:

```xml
<!-- ~/Library/LaunchAgents/com.cloudflare.cloudflared.plist -->
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>       <string>com.cloudflare.cloudflared</string>
  <key>ProgramArguments</key>
  <array>
    <string>/opt/homebrew/bin/cloudflared</string>
    <string>tunnel</string>
    <string>run</string>
    <string>--token</string>
    <string>YOUR_TUNNEL_TOKEN</string>
  </array>
  <key>RunAtLoad</key>   <true/>
  <key>KeepAlive</key>   <true/>
  <key>StandardOutPath</key> <string>/tmp/cloudflared.log</string>
  <key>StandardErrorPath</key> <string>/tmp/cloudflared.err</string>
</dict>
</plist>
```

```bash
launchctl load ~/Library/LaunchAgents/com.cloudflare.cloudflared.plist
launchctl start com.cloudflare.cloudflared
```

#### 9c. Configure tunnel hostnames in Cloudflare dashboard

Add two **Public Hostnames** under the tunnel:

| Subdomain | Domain | Service | Notes |
|-----------|--------|---------|-------|
| `home` (or any innocuous name) | `yourdomain.com` | `https://localhost:443` | Auth endpoint. Enable **No TLS Verify** in Additional settings → TLS (server uses a self-signed cert). |
| `api` (or any innocuous name) | `yourdomain.com` | `http://localhost:8888` | WS VPN fallback. Plain HTTP — Cloudflare terminates TLS. |

> **Subdomain naming:** Avoid keywords like `vpn`, `proxy`, `tunnel` in the hostname — these are common GFW keyword-filter targets. Use innocuous names like `home`, `api`, `cdn`.

#### 9d. Update server config

```yaml
server:
  ws_fallback_domain: "api.yourdomain.com"   # your WS CDN hostname

client:
  download_url: "https://home.yourdomain.com/download/client"
```

#### 9e. Update client config

```json
{
  "auth_url": "https://YOUR_SERVER_IP",
  "cert_fingerprint": "THE_SHA256_FINGERPRINT",
  "cdn_auth_url": "https://home.yourdomain.com"
}
```

### 10. Build the Go server

```bash
cd server/go
go build -ldflags="-s -w" -o ../authserver .
```

This produces a single static binary at `server/authserver` (~25 MB).

### 11. Start sing-box

```bash
sing-box run -c server/sing-box-server.json
```

### 12. Start the auth server

```bash
WEIAI_CONFIG=/path/to/server/config.yaml server/authserver
```

Verify:
```bash
curl -k https://localhost/health
# {"status":"ok","version":"1.0.0"}
```

### 13. Run as LaunchAgents (auto-start on login)

Both sing-box and the auth server should run as LaunchAgents so they restart on crash and start at login.

**sing-box:**
```bash
cp server/launchagents/com.sing-box.vpn.plist ~/Library/LaunchAgents/
# Edit the plist — update the sing-box binary path and config path
launchctl load ~/Library/LaunchAgents/com.sing-box.vpn.plist
```

**Auth server:**
```bash
cp server/launchagents/com.weiai.authserver.plist ~/Library/LaunchAgents/
# Edit the plist — set WEIAI_CONFIG path
launchctl load ~/Library/LaunchAgents/com.weiai.authserver.plist
```

To reload after config changes:
```bash
launchctl kickstart -k gui/$(id -u)/com.weiai.authserver
launchctl kickstart -k gui/$(id -u)/com.sing-box.vpn
```

### 14. Router port forwarding

On your router, forward these WAN ports to the server's LAN IP:

| WAN Port | LAN Port | Protocol |
|----------|----------|----------|
| `443` | `443` | TCP |
| `8443` | `8443` | TCP |
| `8888` | `8888` | TCP |

> Port 443 serves the auth HTTPS endpoint directly. If Cloudflare Tunnel is configured, auth traffic arrives from Cloudflare's edge servers rather than client IPs — the router rule is still needed for the fallback direct connection.

---

## Upgrading from v1.0.0

If you have an existing database from v1.0.0, run the migration to add speed limit and quota columns:

```bash
psql weiai_vpn < server/db/migration_001_user_limits.sql
```

Then rebuild and restart the auth server. No data is lost — the new columns default to `NULL` (unlimited).

---

## First User Setup

After the server is running, open `https://YOUR_SERVER_IP/admin` in a browser on your LAN (or `https://localhost/admin` from the server itself).

1. Log in with your admin credentials
2. Go to **Users → New User**, create a username and password
3. Click **Reg. Code** next to the user — copy the 8-character code (valid 15 minutes)
4. Send `为爱鼓掌.zip` and the code to the user
5. User installs the app, enters their username/password, then the registration code on first launch
6. Future logins on the same Mac require only username and password

---

## Admin Dashboard

The admin dashboard is available at `https://YOUR_SERVER_IP/admin` — **LAN only**. Requests from outside your local network are rejected with HTTP 403.

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
  "auth_url": "https://YOUR_SERVER_IP",
  "cert_fingerprint": "THE_SHA256_FINGERPRINT_FROM_STEP_2",
  "cdn_auth_url": "https://your-cdn-auth-domain.com"
}
```

**Fields:**

| Field | Required | Description |
|-------|----------|-------------|
| `auth_url` | Yes | Direct HTTPS URL to your server IP. Used as fallback if CDN is unreachable. Certificate pinning is applied here. |
| `cert_fingerprint` | Yes | SHA-256 fingerprint of the server's self-signed TLS certificate. 64 uppercase hex chars, no colons. Used for pinning on the direct `auth_url` path. |
| `cdn_auth_url` | No | HTTPS URL to your Cloudflare CDN hostname for the auth endpoint. Standard CA validation (no pinning). If omitted, all auth goes directly to `auth_url`. |

The client tries `cdn_auth_url` first (12-second timeout). If unreachable, it falls back to `auth_url` with cert pinning (15-second timeout).

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

## Gitignored Files (Sensitive Config)

These files contain secrets or machine-specific values and are intentionally excluded from the repository. CI/CD works because the self-hosted runner (Mac Mini) already has these files in place — they are never pushed to or pulled from GitHub.

| File | Contains | Notes |
|------|----------|-------|
| `server/config.yaml` | DB URL, JWT secret, Reality private key, admin password hash, CDN domain, server IP | Copy from `config.example.yaml` and fill in your values |
| `server/sing-box-server.json` | Reality private key, actual UUIDs | Auto-generated from `sing-box-server.example.json` by the auth server at startup |
| `server/certs/server.crt` | TLS certificate | Generated by `gen_certs.sh` |
| `server/certs/server.key` | TLS private key | Generated by `gen_certs.sh` |
| `client/Resources/config.json` | Server IP, cert fingerprint, CDN auth URL | Copy from `config.example.json` and fill in your values |

The corresponding `*.example.*` files contain only placeholder values and are safe to commit.

**CI/CD note:** The GitHub Actions workflow uses a self-hosted runner on the Mac Mini. Because the runner runs locally on the server, it reads `config.yaml` and `config.json` directly from disk — there is no need to inject secrets via GitHub Secrets for the config itself. The only GitHub Secret needed is your SSH deploy key (for the Go build).

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
3. Client downloads the zip (cert pinning applies if downloading from direct IP; standard CA if from CDN)
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
| **Transport (primary)** | VLESS + XTLS-Reality on port 8443; impersonates www.apple.com TLS handshake; undetectable by DPI |
| **Transport (fallback)** | VLESS + WebSocket via Cloudflare CDN; indistinguishable from normal HTTPS traffic to CDN |
| **Auth endpoint** | Served via Cloudflare Tunnel on standard port 443; server IP is not exposed in this path |
| **Certificate pinning** | Applied on the direct `auth_url` path only. The `cdn_auth_url` path uses standard CA validation (Cloudflare provides the cert). |
| **JWT** | HS256, 15-minute expiry; refresh token in Redis with 24-hour TTL |
| **Keychain storage** | Tokens, username, and password in macOS Keychain — not on disk |
| **Rate limiting** | 5 auth attempts per 15 minutes per IP (Redis sliding window) |
| **Admin isolation** | Admin endpoints require LAN IP + separate JWT with different secret suffix |
| **Device pinning** | Each device registered once; new Mac requires admin-issued time-limited code |
| **Kill switch** | Null routes block all traffic on unexpected disconnect |
| **Subdomain naming** | CDN hostnames should avoid keywords `vpn`, `proxy`, `tunnel` — use innocuous names to avoid GFW keyword filtering |

---

## Project Structure

```
├── server/
│   ├── go/                          Go server (auth + admin + background tasks)
│   │   ├── main.go                  Entry point (Fiber v2, port from config)
│   │   ├── version.go               Server and min-client version constants
│   │   ├── config/config.go         Config YAML loader
│   │   ├── models/models.go         Shared data types (UserPolicy, VlessConfig, etc.)
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
│   │   ├── singbox/singbox.go       Atomic sing-box config update (all VLESS inbounds)
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
│   ├── sing-box-server.example.json sing-box config template (Reality + WS inbounds)
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
    │   ├── VPNManager.swift         sing-box lifecycle, dual-path urltest config, bypass routes
    │   ├── UpdateService.swift      In-app update: download → extract → replace → relaunch
    │   ├── AuthService.swift        HTTP auth, CDN/direct fallback, JWT, Keychain, cert pinning
    │   ├── KillSwitch.swift         Null-route kill switch via osascript
    │   ├── NetworkMonitor.swift     Real-time upload/download speed (menu bar)
    │   ├── KeychainHelper.swift     SecKeychain CRUD
    │   ├── Config.swift             Loads config.json from bundle (auth_url, fingerprint, cdn_auth_url)
    │   ├── L.swift                  Localizable string lookup helper
    │   └── Version.swift            Version, release date, author
    └── Resources/
        ├── en.lproj/Localizable.strings
        ├── zh-Hans.lproj/Localizable.strings
        ├── config.json              Runtime config (gitignored — contains server IP + cert fingerprint)
        └── config.example.json      Template — safe to commit
```

---

## API Reference

All endpoints are on `https://YOUR_SERVER` (port 443, standard HTTPS — no port in URL).

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

#### `POST /connect` response

```json
{
  "access_token": "...",
  "refresh_token": "...",
  "vless_config": {
    "uuid": "...",
    "server": "YOUR_SERVER_IP",
    "port": 8443,
    "public_key": "...",
    "short_id": "...",
    "server_name": "www.apple.com",
    "ws_fallback_domain": "api.yourdomain.com",
    "ws_fallback_port": 8888,
    "ws_fallback_path": "/ws"
  },
  "policy": {
    "speed_limit_up_kbps": null,
    "speed_limit_down_kbps": null,
    "quota_bytes": null,
    "quota_period": null,
    "quota_used_bytes": 0,
    "quota_resets_at": null,
    "quota_exceeded": false
  }
}
```

`ws_fallback_*` fields are omitted when `ws_fallback_domain` is not configured on the server.

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

Please see [CHANGELOG.md](CHANGELOG.md) for a complete history of releases, features, and bug fixes across all server and client iterations.



---

## License

MIT License. Copyright © 2026 Aaron Tong.

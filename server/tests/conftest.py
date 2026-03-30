"""Shared fixtures for WeiAi VPN server tests."""
import json
import os
import subprocess
from pathlib import Path

import asyncpg
import bcrypt
import pytest
import pytest_asyncio
import redis.asyncio as aioredis
import yaml
from httpx import AsyncClient, ASGITransport

# ── Test constants ────────────────────────────────────────────────────────────

TEST_DB_URL   = os.environ.get("TEST_DB_URL",   "postgresql://localhost/weiai_vpn_test")
TEST_REDIS_URL = os.environ.get("TEST_REDIS_URL", "redis://localhost:6379/1")
SCHEMA_FILE   = Path(__file__).parent.parent / "db" / "schema.sql"
TEST_CONFIG_PATH = Path("/tmp/weiai_test_config.yaml")

TEST_CONFIG = {
    "database": {"url": TEST_DB_URL, "pool_size": 5},
    "redis":    {"url": TEST_REDIS_URL},
    "server": {
        "ip":          "127.0.0.1",
        "port":        443,
        "public_key":  "testpublickey12345",
        "private_key": "testprivatekey123",
        "short_id":    "a1b2c3d4",
        "server_name": "www.example.com",
    },
    "auth": {
        "jwt_secret":           "test_secret_key_at_least_32_chars!",
        "jwt_expiry_minutes":   15,
        "refresh_expiry_hours": 24,
    },
    "admin": {
        "allowed_lan_prefixes": ["127.", "192.168.", "10.", "172.16.", "172.17."],
        "username":      "admin",
        "password_hash": bcrypt.hashpw(b"adminpass", bcrypt.gensalt()).decode(),
    },
    "certs":    {"cert_path": "certs/server.crt", "key_path": "certs/server.key"},
    "sing_box": {
        "config_path": "/tmp/weiai_test_singbox.json",
        "binary_path": "/usr/local/bin/sing-box",
        "clash_api_url": "http://127.0.0.1:9090",
    },
    "geoip": {"db_path": "/nonexistent/GeoLite2-City.mmdb"},
    "log":   {"retention_days": 90, "max_domains_per_user_per_day": 500},
}


# ── One-time setup (module level) ─────────────────────────────────────────────

def _bootstrap():
    """Write test config, singbox stub, and apply DB schema."""
    # Write test config YAML
    with open(TEST_CONFIG_PATH, "w") as f:
        yaml.dump(TEST_CONFIG, f)
    os.environ["WEIAI_CONFIG"] = str(TEST_CONFIG_PATH)

    # Write sing-box stub so _update_singbox has a file to read
    stub = {"inbounds": [{"type": "vless",
                          "users": [{"uuid": "stub", "flow": "xtls-rprx-vision"}]}]}
    with open(TEST_CONFIG["sing_box"]["config_path"], "w") as f:
        json.dump(stub, f)

    # Ensure test DB exists and schema is applied (idempotent)
    subprocess.run(["createdb", "weiai_vpn_test"], capture_output=True)  # OK if already exists
    subprocess.run(
        ["psql", "weiai_vpn_test"],
        input=SCHEMA_FILE.read_text(),
        text=True,
        capture_output=True,
    )


_bootstrap()   # runs once at import time


# ── Patches applied once ──────────────────────────────────────────────────────

import routers.auth as _auth_mod


def _noop_singbox(new_uuid, cfg):
    """Replace _update_singbox: update stub file without killing processes."""
    try:
        path = cfg["sing_box"]["config_path"]
        with open(path) as f:
            sb = json.load(f)
        sb["inbounds"][0]["users"] = [{"uuid": new_uuid, "flow": "xtls-rprx-vision"}]
        with open(path, "w") as f:
            json.dump(sb, f)
    except Exception:
        pass


def _noop_geoip(ip: str):
    return ("TestCountry", "TestCity")


_auth_mod._update_singbox = _noop_singbox

try:
    from services import geoip as _geoip_mod
    _geoip_mod.lookup_ip = _noop_geoip
except Exception:
    pass

try:
    import routers.admin as _admin_mod
    if hasattr(_admin_mod, "_update_singbox"):
        _admin_mod._update_singbox = _noop_singbox
except Exception:
    pass


# ── Per-test fixtures (function scope, no event-loop sharing issues) ──────────

@pytest_asyncio.fixture
async def db_pool():
    pool = await asyncpg.create_pool(TEST_DB_URL, min_size=1, max_size=3)
    # Truncate all tables before each test
    await pool.execute(
        "TRUNCATE access_log, traffic_daily, sessions, devices, users RESTART IDENTITY CASCADE"
    )
    yield pool
    await pool.close()


@pytest_asyncio.fixture
async def redis_client():
    client = aioredis.from_url(TEST_REDIS_URL, decode_responses=True)
    await client.flushdb()
    yield client
    await client.aclose()


@pytest_asyncio.fixture
async def app(db_pool, redis_client):
    import config_loader
    from pathlib import Path as _Path
    from fastapi.templating import Jinja2Templates

    config_loader.reset()
    os.environ["WEIAI_CONFIG"] = str(TEST_CONFIG_PATH)

    from auth_server import app as _app

    _app.state.db        = db_pool
    _app.state.redis     = redis_client
    _app.state.cfg       = TEST_CONFIG
    _app.state.templates = Jinja2Templates(
        directory=str(_Path(__file__).parent.parent / "templates")
    )
    yield _app


@pytest_asyncio.fixture
async def client(app):
    async with AsyncClient(transport=ASGITransport(app=app), base_url="http://test") as ac:
        yield ac


# ── Data helpers ──────────────────────────────────────────────────────────────

async def create_user(db_pool, username="alice", password="password123", is_active=True):
    pw_hash = bcrypt.hashpw(password.encode(), bcrypt.gensalt()).decode()
    row = await db_pool.fetchrow(
        "INSERT INTO users (username, password_hash, is_active) VALUES ($1, $2, $3) RETURNING id",
        username, pw_hash, is_active,
    )
    return str(row["id"])


async def register_device(db_pool, user_id,
                           device_id="TESTDEVICE-1234-5678", device_name="TestMac"):
    import uuid as _uuid
    row = await db_pool.fetchrow(
        "INSERT INTO devices (user_id, device_fingerprint, device_name) VALUES ($1, $2, $3) RETURNING id",
        _uuid.UUID(user_id), device_id, device_name,
    )
    return str(row["id"])

"""Auth endpoints: /connect, /disconnect, /refresh, /verify-device."""
import json
import logging
import secrets
import string
import subprocess
import time
import uuid
from datetime import datetime, timezone
from pathlib import Path
from typing import Optional

import asyncpg
import bcrypt
from fastapi import APIRouter, HTTPException, Request
from jose import jwt

import config_loader
from services.geoip import lookup_ip

log = logging.getLogger("weiai.auth")
router = APIRouter()

BASE_DIR = Path(__file__).parent.parent

# ── Version check ─────────────────────────────────────────────────────────────

def _parse_version(v: str) -> tuple[int, ...]:
    """Parse '1.2.3' → (1, 2, 3). Returns (0,) on invalid input."""
    try:
        return tuple(int(x) for x in v.strip().split("."))
    except Exception:
        return (0,)


def _check_client_version(request: Request, cfg: dict) -> None:
    """Raise HTTP 426 if the client version is below the configured minimum."""
    client_cfg = cfg.get("client", {})
    min_ver = client_cfg.get("min_version", "1.0.0")
    download_url = client_cfg.get("download_url", "")

    raw = request.headers.get("X-Client-Version", "")
    if not raw:
        return  # No header → old client before versioning; let through for now

    # Header format: "WeiAiVPN/1.2.3"
    ver_str = raw.split("/")[-1] if "/" in raw else raw

    if _parse_version(ver_str) < _parse_version(min_ver):
        raise HTTPException(426, detail={
            "error": "client_version_outdated",
            "current_version": ver_str,
            "min_version": min_ver,
            "download_url": download_url,
        })


# ── Helpers ──────────────────────────────────────────────────────────────────

def _make_jwt(payload: dict, cfg: dict) -> str:
    now = int(time.time())
    payload.update({
        "iat": now,
        "exp": now + cfg["auth"]["jwt_expiry_minutes"] * 60,
        "iss": "weiai-vpn",
    })
    return jwt.encode(payload, cfg["auth"]["jwt_secret"], algorithm="HS256")


def _verify_jwt(token: str, cfg: dict) -> dict:
    return jwt.decode(token, cfg["auth"]["jwt_secret"], algorithms=["HS256"],
                      options={"verify_iss": True}, issuer="weiai-vpn")


async def _check_rate(redis, ip: str) -> None:
    key = f"rate:{ip}:auth"
    count = await redis.incr(key)
    if count == 1:
        await redis.expire(key, 15 * 60)  # 15-minute sliding window
    if count > 5:
        raise HTTPException(429, "Too many requests. Try again later.")


def _update_singbox(new_uuid: str, cfg: dict) -> None:
    """Replace the UUID in sing-box server config and restart."""
    config_path = Path(cfg["sing_box"]["config_path"])
    if not config_path.is_absolute():
        config_path = BASE_DIR / config_path

    with open(config_path) as f:
        sb_cfg = json.load(f)

    sb_cfg["inbounds"][0]["users"] = [
        {"uuid": new_uuid, "flow": "xtls-rprx-vision"}
    ]

    with open(config_path, "w") as f:
        json.dump(sb_cfg, f, indent=2)

    subprocess.run(["pkill", "-f", "sing-box"], capture_output=True)
    time.sleep(1.5)
    log.info(f"sing-box UUID updated: {new_uuid[:8]}...")


async def _create_session(db, redis, user_id: str, device_id: str, device_row,
                           login_ip: str, cfg: dict) -> tuple[str, str, str]:
    """Deactivate old session, create new one. Returns (vless_uuid, access_token, refresh_token)."""
    # Deactivate existing active sessions for this device
    await db.execute(
        "UPDATE sessions SET is_active=false, disconnected_at=NOW() "
        "WHERE device_id=$1 AND is_active=true",
        device_row["id"],
    )
    # Clean up old Redis session entry
    old_session = await redis.get(f"active_session:{device_id}")
    if old_session:
        old_data = json.loads(old_session)
        await redis.delete(f"vless_map:{old_data['vless_uuid']}")
        await redis.delete(f"refresh:{old_data.get('refresh_token', '')}")

    # New session
    vless_uuid = str(uuid.uuid4())
    country, city = lookup_ip(login_ip)

    session_id = await db.fetchval(
        """INSERT INTO sessions (user_id, device_id, vless_uuid, login_ip,
                                  login_country, login_city)
           VALUES ($1, $2, $3, $4::inet, $5, $6)
           RETURNING id""",
        uuid.UUID(user_id), device_row["id"], vless_uuid, login_ip, country, city,
    )

    # Update sing-box config
    _update_singbox(vless_uuid, cfg)

    # JWT + refresh token
    access_token = _make_jwt({"sub": user_id, "sid": str(session_id)}, cfg)
    refresh_token = secrets.token_urlsafe(32)
    refresh_ttl = cfg["auth"]["refresh_expiry_hours"] * 3600

    session_data = json.dumps({
        "user_id": user_id,
        "session_id": str(session_id),
        "vless_uuid": vless_uuid,
        "refresh_token": refresh_token,
    })
    await redis.set(f"active_session:{device_id}", session_data)
    await redis.set(f"vless_map:{vless_uuid}", session_data)
    await redis.setex(f"refresh:{refresh_token}", refresh_ttl, session_data)

    # Update device last_seen
    await db.execute(
        "UPDATE devices SET last_seen=NOW() WHERE id=$1",
        device_row["id"],
    )

    return vless_uuid, access_token, refresh_token


def _vpn_response(vless_uuid: str, access_token: str, refresh_token: str, cfg: dict) -> dict:
    srv = cfg["server"]
    return {
        "access_token": access_token,
        "refresh_token": refresh_token,
        "vless_config": {
            "uuid":        vless_uuid,
            "server":      srv["ip"],
            "port":        srv["port"],
            "public_key":  srv["public_key"],
            "short_id":    srv["short_id"],
            "server_name": srv["server_name"],
        },
    }


# ── Routes ────────────────────────────────────────────────────────────────────

@router.post("/connect")
async def connect(request: Request):
    cfg = config_loader.get()
    db: asyncpg.Pool = request.app.state.db
    redis = request.app.state.redis
    ip = request.client.host

    _check_client_version(request, cfg)
    await _check_rate(redis, ip)

    try:
        body = await request.json()
    except Exception:
        raise HTTPException(400, "Invalid JSON")

    username = body.get("username", "").strip()
    password = body.get("password", "")
    device_id = body.get("device_id", "").strip()
    device_name = body.get("device_name", "Unknown Device")[:128]

    if not username or not password:
        raise HTTPException(400, "Missing username or password")
    if not device_id or len(device_id) < 8:
        raise HTTPException(400, "Missing or invalid device_id")

    # Verify credentials
    user = await db.fetchrow(
        "SELECT id, password_hash, is_active FROM users WHERE username=$1",
        username,
    )
    if not user:
        log.warning(f"Unknown user '{username}' from {ip}")
        raise HTTPException(401, "Invalid credentials")
    if not user["is_active"]:
        raise HTTPException(403, "Account disabled")
    if not bcrypt.checkpw(password.encode(), user["password_hash"].encode()):
        log.warning(f"Wrong password for '{username}' from {ip}")
        raise HTTPException(401, "Invalid credentials")

    user_id = str(user["id"])

    # Check device registration
    device = await db.fetchrow(
        "SELECT id, is_active FROM devices WHERE device_fingerprint=$1",
        device_id,
    )
    if not device:
        log.info(f"Unregistered device {device_id[:8]}... for user '{username}'")
        raise HTTPException(403, detail={
            "error": "device_not_registered",
            "message": "此设备未注册，请联系管理员获取验证码",
        })
    if not device["is_active"]:
        raise HTTPException(403, "Device disabled")

    # Verify device belongs to this user
    owner = await db.fetchval(
        "SELECT user_id FROM devices WHERE id=$1",
        device["id"],
    )
    if str(owner) != user_id:
        raise HTTPException(403, "Device not associated with this account")

    vless_uuid, access_token, refresh_token = await _create_session(
        db, redis, user_id, device_id, device, ip, cfg,
    )
    log.info(f"User '{username}' connected from {ip} ({country if (country := lookup_ip(ip)[0]) else ip})")
    return _vpn_response(vless_uuid, access_token, refresh_token, cfg)


@router.post("/verify-device")
async def verify_device(request: Request):
    """Register a new device using an admin-issued verification code."""
    cfg = config_loader.get()
    db: asyncpg.Pool = request.app.state.db
    redis = request.app.state.redis
    ip = request.client.host

    _check_client_version(request, cfg)
    await _check_rate(redis, ip)

    try:
        body = await request.json()
    except Exception:
        raise HTTPException(400, "Invalid JSON")

    username = body.get("username", "").strip()
    password = body.get("password", "")
    device_id = body.get("device_id", "").strip()
    device_name = body.get("device_name", "Unknown Device")[:128]
    code = body.get("verification_code", "").strip().upper()

    if not all([username, password, device_id, code]):
        raise HTTPException(400, "Missing required fields")
    if len(device_id) < 8:
        raise HTTPException(400, "Invalid device_id")

    # Verify credentials
    user = await db.fetchrow(
        "SELECT id, password_hash, is_active FROM users WHERE username=$1",
        username,
    )
    if not user:
        raise HTTPException(401, "Invalid credentials")
    if not user["is_active"]:
        raise HTTPException(403, "Account disabled")
    if not bcrypt.checkpw(password.encode(), user["password_hash"].encode()):
        raise HTTPException(401, "Invalid credentials")

    user_id = str(user["id"])

    # Validate verification code (Redis: verif:{code} → user_id, TTL 15min)
    stored_user_id = await redis.get(f"verif:{code}")
    if not stored_user_id:
        raise HTTPException(403, "Invalid or expired verification code")
    if stored_user_id != user_id:
        raise HTTPException(403, "Verification code not issued for this account")

    # Consume the code immediately
    await redis.delete(f"verif:{code}")

    # Register device
    device = await db.fetchrow(
        """INSERT INTO devices (user_id, device_fingerprint, device_name)
           VALUES ($1, $2, $3)
           ON CONFLICT (device_fingerprint)
           DO UPDATE SET is_active=true, last_seen=NOW()
           RETURNING id, is_active""",
        uuid.UUID(user_id), device_id, device_name,
    )

    vless_uuid, access_token, refresh_token = await _create_session(
        db, redis, user_id, device_id, device, ip, cfg,
    )
    log.info(f"New device registered for '{username}': {device_id[:8]}...")
    return _vpn_response(vless_uuid, access_token, refresh_token, cfg)


@router.post("/disconnect")
async def disconnect(request: Request):
    cfg = config_loader.get()
    db: asyncpg.Pool = request.app.state.db
    redis = request.app.state.redis

    try:
        body = await request.json()
    except Exception:
        raise HTTPException(400, "Invalid JSON")

    device_id = body.get("device_id", "").strip()
    if not device_id:
        raise HTTPException(400, "Missing device_id")

    session_data_raw = await redis.get(f"active_session:{device_id}")
    if session_data_raw:
        data = json.loads(session_data_raw)
        session_id = data["session_id"]
        vless_uuid = data["vless_uuid"]
        refresh_token = data.get("refresh_token", "")

        # Mark session inactive in DB
        await db.execute(
            "UPDATE sessions SET is_active=false, disconnected_at=NOW() WHERE id=$1",
            uuid.UUID(session_id),
        )

        # Invalidate Redis entries
        await redis.delete(f"active_session:{device_id}")
        await redis.delete(f"vless_map:{vless_uuid}")
        if refresh_token:
            await redis.delete(f"refresh:{refresh_token}")

        # Invalidate sing-box UUID (set to random unusable UUID)
        _update_singbox(str(uuid.uuid4()), cfg)
        log.info(f"Device {device_id[:8]}... disconnected")

    return {"status": "ok"}


@router.post("/refresh")
async def refresh(request: Request):
    cfg = config_loader.get()
    redis = request.app.state.redis

    try:
        body = await request.json()
    except Exception:
        raise HTTPException(400, "Invalid JSON")

    token = body.get("refresh_token", "").strip()
    if not token:
        raise HTTPException(400, "Missing refresh_token")

    session_data_raw = await redis.get(f"refresh:{token}")
    if not session_data_raw:
        raise HTTPException(401, "Invalid or expired refresh token")

    data = json.loads(session_data_raw)
    access_token = _make_jwt(
        {"sub": data["user_id"], "sid": data["session_id"]}, cfg
    )
    return {"access_token": access_token}

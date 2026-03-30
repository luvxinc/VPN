"""Admin dashboard routes — LAN-only access."""
import json
import logging
import secrets
import string
import uuid
from datetime import date, datetime, timedelta

import asyncpg
import bcrypt
import redis.asyncio as aioredis
from fastapi import APIRouter, Depends, HTTPException, Request, Response
from fastapi.responses import HTMLResponse, JSONResponse, RedirectResponse
from jose import JWTError, jwt

import config_loader

log = logging.getLogger("weiai.admin")
router = APIRouter(prefix="/admin")

# ── LAN middleware ────────────────────────────────────────────────────────────

def _is_lan(ip: str, prefixes: list[str]) -> bool:
    return any(ip.startswith(p) for p in prefixes)


def _require_lan(request: Request):
    cfg = config_loader.get()
    ip = request.client.host
    if not _is_lan(ip, cfg["admin"]["allowed_lan_prefixes"]):
        raise HTTPException(403, "Admin access restricted to LAN only")


# ── Admin JWT helpers ─────────────────────────────────────────────────────────

def _admin_secret(cfg: dict) -> str:
    return cfg["auth"]["jwt_secret"] + ":admin"


def _make_admin_jwt(cfg: dict) -> str:
    import time
    payload = {
        "sub": "admin",
        "iss": "weiai-admin",
        "iat": int(time.time()),
        "exp": int(time.time()) + 8 * 3600,
    }
    return jwt.encode(payload, _admin_secret(cfg), algorithm="HS256")


def _verify_admin_jwt(token: str, cfg: dict) -> bool:
    try:
        jwt.decode(token, _admin_secret(cfg), algorithms=["HS256"],
                   options={"verify_iss": True}, issuer="weiai-admin")
        return True
    except JWTError:
        return False


def _get_admin_token(request: Request) -> str | None:
    return request.cookies.get("admin_token")


def _require_admin_auth(request: Request):
    _require_lan(request)
    cfg = config_loader.get()
    token = _get_admin_token(request)
    if not token or not _verify_admin_jwt(token, cfg):
        raise HTTPException(401, "Not authenticated")


# ── Verif code generator ──────────────────────────────────────────────────────

def _gen_code() -> str:
    alphabet = string.ascii_uppercase + string.digits
    return "".join(secrets.choice(alphabet) for _ in range(8))


# ── Routes ────────────────────────────────────────────────────────────────────

@router.get("/login", response_class=HTMLResponse)
async def login_page(request: Request, _=Depends(_require_lan)):
    templates = request.app.state.templates
    return templates.TemplateResponse("login.html", {"request": request})


@router.post("/login")
async def login(request: Request, _=Depends(_require_lan)):
    cfg = config_loader.get()
    try:
        form = await request.form()
    except Exception:
        raise HTTPException(400, "Invalid form data")

    username = form.get("username", "")
    password = form.get("password", "")

    if username != cfg["admin"]["username"]:
        raise HTTPException(401, "Invalid credentials")
    if not bcrypt.checkpw(password.encode(), cfg["admin"]["password_hash"].encode()):
        raise HTTPException(401, "Invalid credentials")

    token = _make_admin_jwt(cfg)
    response = RedirectResponse("/admin/dashboard", status_code=303)
    response.set_cookie("admin_token", token, httponly=True, samesite="strict",
                        max_age=8 * 3600)
    log.info(f"Admin login from {request.client.host}")
    return response


@router.post("/logout")
async def logout():
    response = RedirectResponse("/admin/login", status_code=303)
    response.delete_cookie("admin_token")
    return response


@router.get("/dashboard", response_class=HTMLResponse)
async def dashboard(request: Request, _=Depends(_require_admin_auth)):
    db: asyncpg.Pool = request.app.state.db
    templates = request.app.state.templates

    online_sessions = await db.fetch(
        """SELECT s.id, u.username, s.login_ip, s.login_country, s.login_city,
                  s.connected_at, s.upload_bytes, s.download_bytes
           FROM sessions s JOIN users u ON s.user_id = u.id
           WHERE s.is_active = true
           ORDER BY s.connected_at DESC"""
    )
    today_traffic = await db.fetch(
        """SELECT u.username, SUM(s.upload_bytes) AS upload, SUM(s.download_bytes) AS download
           FROM sessions s JOIN users u ON s.user_id = u.id
           WHERE DATE(s.connected_at) = CURRENT_DATE
           GROUP BY u.username ORDER BY download DESC"""
    )
    return templates.TemplateResponse("dashboard.html", {
        "request": request,
        "online_sessions": online_sessions,
        "today_traffic": today_traffic,
    })


@router.get("/api/online-count")
async def api_online_count(request: Request, _=Depends(_require_admin_auth)):
    """HTMX partial: just the online count number."""
    db: asyncpg.Pool = request.app.state.db
    count = await db.fetchval(
        "SELECT COUNT(*) FROM sessions WHERE is_active = true"
    )
    return HTMLResponse(str(count))


@router.get("/api/online")
async def api_online(request: Request, _=Depends(_require_admin_auth)):
    """HTMX partial: live online user count + table rows."""
    db: asyncpg.Pool = request.app.state.db
    templates = request.app.state.templates
    rows = await db.fetch(
        """SELECT s.id, u.username, s.login_ip, s.login_country, s.login_city,
                  s.connected_at, s.upload_bytes, s.download_bytes
           FROM sessions s JOIN users u ON s.user_id = u.id
           WHERE s.is_active = true ORDER BY s.connected_at DESC"""
    )
    return templates.TemplateResponse("_online_rows.html", {
        "request": request,
        "online_sessions": rows,
    })


@router.get("/users", response_class=HTMLResponse)
async def users_page(request: Request, _=Depends(_require_admin_auth)):
    db: asyncpg.Pool = request.app.state.db
    templates = request.app.state.templates
    redis = request.app.state.redis

    users = await db.fetch(
        """SELECT u.id, u.username, u.is_active, u.created_at,
                  d.last_seen, d.device_name,
                  (SELECT COUNT(*) FROM sessions s2 WHERE s2.user_id=u.id AND s2.is_active=true) AS online
           FROM users u
           LEFT JOIN devices d ON d.user_id=u.id AND d.is_active=true
           ORDER BY u.created_at DESC"""
    )
    return templates.TemplateResponse("users.html", {
        "request": request,
        "users": users,
    })


@router.post("/users")
async def create_user(request: Request, _=Depends(_require_admin_auth)):
    db: asyncpg.Pool = request.app.state.db
    try:
        form = await request.form()
    except Exception:
        raise HTTPException(400, "Invalid form")

    username = form.get("username", "").strip()
    password = form.get("password", "")
    notes = form.get("notes", "")

    if not username or not password:
        raise HTTPException(400, "Username and password required")
    if len(password) < 8:
        raise HTTPException(400, "Password must be at least 8 characters")

    pw_hash = bcrypt.hashpw(password.encode(), bcrypt.gensalt()).decode()
    try:
        await db.execute(
            "INSERT INTO users (username, password_hash, notes) VALUES ($1, $2, $3)",
            username, pw_hash, notes or None,
        )
    except asyncpg.UniqueViolationError:
        raise HTTPException(409, "Username already exists")

    log.info(f"Admin created user '{username}'")
    return RedirectResponse("/admin/users", status_code=303)


@router.post("/users/{user_id}/delete")
async def delete_user(user_id: str, request: Request, _=Depends(_require_admin_auth)):
    db: asyncpg.Pool = request.app.state.db
    redis = request.app.state.redis

    # Kick active sessions first
    await _kick_user_sessions(user_id, db, redis, config_loader.get())

    await db.execute("DELETE FROM users WHERE id=$1", uuid.UUID(user_id))
    log.info(f"Admin deleted user {user_id[:8]}")
    return RedirectResponse("/admin/users", status_code=303)


@router.post("/users/{user_id}/password")
async def change_password(user_id: str, request: Request, _=Depends(_require_admin_auth)):
    db: asyncpg.Pool = request.app.state.db
    try:
        form = await request.form()
    except Exception:
        raise HTTPException(400, "Invalid form")

    password = form.get("password", "")
    if len(password) < 8:
        raise HTTPException(400, "Password must be at least 8 characters")

    pw_hash = bcrypt.hashpw(password.encode(), bcrypt.gensalt()).decode()
    await db.execute(
        "UPDATE users SET password_hash=$1 WHERE id=$2",
        pw_hash, uuid.UUID(user_id),
    )
    return RedirectResponse("/admin/users", status_code=303)


@router.post("/users/{user_id}/toggle")
async def toggle_user(user_id: str, request: Request, _=Depends(_require_admin_auth)):
    db: asyncpg.Pool = request.app.state.db
    redis = request.app.state.redis
    cfg = config_loader.get()

    row = await db.fetchrow("SELECT is_active FROM users WHERE id=$1", uuid.UUID(user_id))
    if not row:
        raise HTTPException(404, "User not found")

    new_active = not row["is_active"]
    await db.execute("UPDATE users SET is_active=$1 WHERE id=$2", new_active, uuid.UUID(user_id))

    if not new_active:
        await _kick_user_sessions(user_id, db, redis, cfg)

    return RedirectResponse("/admin/users", status_code=303)


@router.post("/users/{user_id}/kick")
async def kick_user(user_id: str, request: Request, _=Depends(_require_admin_auth)):
    db: asyncpg.Pool = request.app.state.db
    redis = request.app.state.redis
    cfg = config_loader.get()
    await _kick_user_sessions(user_id, db, redis, cfg)
    return RedirectResponse("/admin/users", status_code=303)


@router.get("/users/{user_id}/verif-code")
async def generate_verif_code(user_id: str, request: Request, _=Depends(_require_admin_auth)):
    """Generate a one-time 8-char verification code (valid 15 min)."""
    db: asyncpg.Pool = request.app.state.db
    redis = request.app.state.redis

    user = await db.fetchrow("SELECT username FROM users WHERE id=$1", uuid.UUID(user_id))
    if not user:
        raise HTTPException(404, "User not found")

    code = _gen_code()
    await redis.setex(f"verif:{code}", 15 * 60, user_id)

    log.info(f"Admin generated verif code for user '{user['username']}'")
    # Return JSON for HTMX modal
    return {"code": code, "expires_in_seconds": 900, "username": user["username"]}


@router.get("/logs", response_class=HTMLResponse)
async def logs_page(request: Request, _=Depends(_require_admin_auth)):
    db: asyncpg.Pool = request.app.state.db
    templates = request.app.state.templates

    params = request.query_params
    user_id = params.get("user_id", "")
    date_from = params.get("from", str(date.today() - timedelta(days=7)))
    date_to = params.get("to", str(date.today()))

    users = await db.fetch("SELECT id, username FROM users ORDER BY username")
    logs = []
    if user_id:
        logs = await db.fetch(
            """SELECT al.host, al.access_hour, al.request_count,
                      al.upload_bytes, al.download_bytes
               FROM access_log al
               WHERE al.user_id=$1
                 AND al.access_hour >= $2::timestamp
                 AND al.access_hour < ($3::date + interval '1 day')::timestamp
               ORDER BY al.access_hour DESC
               LIMIT 1000""",
            uuid.UUID(user_id),
            date_from,
            date_to,
        )

    return templates.TemplateResponse("logs.html", {
        "request": request,
        "users": users,
        "logs": logs,
        "selected_user": user_id,
        "date_from": date_from,
        "date_to": date_to,
    })


@router.get("/stats", response_class=HTMLResponse)
async def stats_page(request: Request, _=Depends(_require_admin_auth)):
    db: asyncpg.Pool = request.app.state.db
    templates = request.app.state.templates

    params = request.query_params
    user_id = params.get("user_id", "")
    period = params.get("period", "week")

    today = date.today()
    if period == "today":
        date_from = today
    elif period == "month":
        date_from = today.replace(day=1)
    elif period == "custom":
        date_from = date.fromisoformat(params.get("from", str(today - timedelta(days=7))))
    else:  # week default
        date_from = today - timedelta(days=6)

    date_to = today

    users = await db.fetch("SELECT id, username FROM users ORDER BY username")
    daily_rows = []
    summary = {"upload": 0, "download": 0, "sessions": 0}

    if user_id:
        daily_rows = await db.fetch(
            """SELECT date, upload_bytes, download_bytes
               FROM traffic_daily
               WHERE user_id=$1 AND date >= $2 AND date <= $3
               ORDER BY date DESC""",
            uuid.UUID(user_id), date_from, date_to,
        )
        agg = await db.fetchrow(
            """SELECT COALESCE(SUM(upload_bytes),0) AS upload,
                      COALESCE(SUM(download_bytes),0) AS download,
                      COUNT(*) AS days
               FROM traffic_daily
               WHERE user_id=$1 AND date >= $2 AND date <= $3""",
            uuid.UUID(user_id), date_from, date_to,
        )
        session_count = await db.fetchval(
            """SELECT COUNT(*) FROM sessions
               WHERE user_id=$1 AND DATE(connected_at) >= $2 AND DATE(connected_at) <= $3""",
            uuid.UUID(user_id), date_from, date_to,
        )
        if agg:
            summary = {"upload": agg["upload"], "download": agg["download"],
                       "sessions": session_count}

    return templates.TemplateResponse("stats.html", {
        "request": request,
        "users": users,
        "selected_user": user_id,
        "period": period,
        "date_from": str(date_from),
        "date_to": str(date_to),
        "daily_rows": daily_rows,
        "summary": summary,
    })


# ── Internal helper ───────────────────────────────────────────────────────────

async def _kick_user_sessions(user_id: str, db, redis, cfg: dict):
    """Force-disconnect all active sessions for a user."""
    import subprocess, time, json as _json

    active = await db.fetch(
        "SELECT id, device_id, vless_uuid FROM sessions WHERE user_id=$1 AND is_active=true",
        uuid.UUID(user_id),
    )
    for s in active:
        device = await db.fetchrow("SELECT device_fingerprint FROM devices WHERE id=$1", s["device_id"])
        if device:
            await redis.delete(f"active_session:{device['device_fingerprint']}")
        await redis.delete(f"vless_map:{s['vless_uuid']}")

    await db.execute(
        "UPDATE sessions SET is_active=false, disconnected_at=NOW() WHERE user_id=$1 AND is_active=true",
        uuid.UUID(user_id),
    )
    # Invalidate sing-box UUID
    import json as _json
    from pathlib import Path
    config_path = Path(cfg["sing_box"]["config_path"])
    if not config_path.is_absolute():
        config_path = Path(__file__).parent.parent / config_path
    try:
        with open(config_path) as f:
            sb = _json.load(f)
        import uuid as _uuid
        sb["inbounds"][0]["users"] = [{"uuid": str(_uuid.uuid4()), "flow": "xtls-rprx-vision"}]
        with open(config_path, "w") as f:
            _json.dump(sb, f, indent=2)
        subprocess.run(["pkill", "-f", "sing-box"], capture_output=True)
        time.sleep(1.5)
    except Exception as e:
        log.warning(f"Could not update sing-box after kick: {e}")

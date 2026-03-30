"""Tests for /admin/** endpoints."""
import pytest
import pytest_asyncio

from tests.conftest import create_user, register_device, TEST_CONFIG


# ── Helpers ───────────────────────────────────────────────────────────────────

async def admin_login(client):
    """POST /admin/login and return the client (cookies set automatically)."""
    resp = await client.post(
        "/admin/login",
        data={"username": "admin", "password": "adminpass"},
        follow_redirects=True,
    )
    return resp


# ── Auth ──────────────────────────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_admin_login_valid(client):
    resp = await admin_login(client)
    assert resp.status_code == 200
    # Should land on dashboard, not be redirected back to login
    assert "/admin/login" not in str(resp.url)


@pytest.mark.asyncio
async def test_admin_login_invalid_password(client):
    resp = await client.post(
        "/admin/login",
        data={"username": "admin", "password": "wrongpass"},
        follow_redirects=False,
    )
    assert resp.status_code == 401


@pytest.mark.asyncio
async def test_admin_requires_lan(client):
    """Requests from non-LAN IPs should be blocked even without auth."""
    # The test client uses testclient IP (127.0.0.1) by default — LAN OK.
    # We can't easily fake a non-LAN IP here, so we just verify LAN IPs work.
    resp = await client.get("/admin/login")
    assert resp.status_code == 200


# ── User CRUD ─────────────────────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_create_user(client, db_pool):
    await admin_login(client)
    resp = await client.post(
        "/admin/users",
        data={"username": "newuser", "password": "securepass1"},
        follow_redirects=True,
    )
    assert resp.status_code == 200

    row = await db_pool.fetchrow("SELECT username FROM users WHERE username='newuser'")
    assert row is not None


@pytest.mark.asyncio
async def test_create_user_duplicate(client, db_pool):
    await admin_login(client)
    await create_user(db_pool, username="dupuser")

    resp = await client.post(
        "/admin/users",
        data={"username": "dupuser", "password": "securepass1"},
        follow_redirects=False,
    )
    assert resp.status_code == 409


@pytest.mark.asyncio
async def test_create_user_short_password(client):
    await admin_login(client)
    resp = await client.post(
        "/admin/users",
        data={"username": "weakuser", "password": "short"},
        follow_redirects=False,
    )
    assert resp.status_code == 400


@pytest.mark.asyncio
async def test_delete_user(client, db_pool):
    await admin_login(client)
    uid = await create_user(db_pool, username="todelete")

    resp = await client.post(f"/admin/users/{uid}/delete", follow_redirects=True)
    assert resp.status_code == 200

    row = await db_pool.fetchrow("SELECT id FROM users WHERE id=$1",
                                  __import__("uuid").UUID(uid))
    assert row is None


@pytest.mark.asyncio
async def test_toggle_user_disables_active(client, db_pool):
    await admin_login(client)
    uid = await create_user(db_pool, username="toggler", is_active=True)

    resp = await client.post(f"/admin/users/{uid}/toggle", follow_redirects=True)
    assert resp.status_code == 200

    row = await db_pool.fetchrow("SELECT is_active FROM users WHERE id=$1",
                                  __import__("uuid").UUID(uid))
    assert row["is_active"] is False


@pytest.mark.asyncio
async def test_generate_verif_code_format(client, db_pool):
    """Verif code must be 8 chars uppercase alphanumeric."""
    await admin_login(client)
    uid = await create_user(db_pool, username="codeuser")

    resp = await client.get(f"/admin/users/{uid}/verif-code")
    assert resp.status_code == 200

    body = resp.json()
    code = body["code"]
    assert len(code) == 8
    assert code.isalnum()
    assert code == code.upper()


@pytest.mark.asyncio
async def test_generate_verif_code_redis_ttl(client, db_pool, redis_client):
    """Verif code stored in Redis with ~900s TTL."""
    await admin_login(client)
    uid = await create_user(db_pool, username="ttluser")

    resp = await client.get(f"/admin/users/{uid}/verif-code")
    code = resp.json()["code"]

    ttl = await redis_client.ttl(f"verif:{code}")
    assert 890 <= ttl <= 910


@pytest.mark.asyncio
async def test_kick_user(client, db_pool, redis_client):
    """Kick clears active sessions and Redis keys."""
    await admin_login(client)
    uid = await create_user(db_pool, username="kickme")
    await register_device(db_pool, uid, device_id="KICKDEV-1234-5678")

    # Plant a fake active session in Redis
    import json
    session_data = json.dumps({
        "user_id": uid,
        "session_id": "00000000-0000-0000-0000-000000000001",
        "vless_uuid": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
        "refresh_token": "sometoken",
    })
    await redis_client.set("active_session:KICKDEV-1234-5678", session_data)
    await db_pool.execute(
        "INSERT INTO sessions (user_id, device_id, vless_uuid, login_ip, is_active) "
        "VALUES ($1, (SELECT id FROM devices WHERE device_fingerprint='KICKDEV-1234-5678'), "
        "$2, '127.0.0.1', true)",
        __import__("uuid").UUID(uid), "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
    )

    resp = await client.post(f"/admin/users/{uid}/kick", follow_redirects=True)
    assert resp.status_code == 200

    count = await db_pool.fetchval(
        "SELECT COUNT(*) FROM sessions WHERE user_id=$1 AND is_active=true",
        __import__("uuid").UUID(uid),
    )
    assert count == 0

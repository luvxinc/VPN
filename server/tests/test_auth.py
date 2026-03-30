"""Tests for /connect, /verify-device, /disconnect, /refresh."""
import json
import pytest
import pytest_asyncio

from tests.conftest import create_user, register_device, TEST_CONFIG


DEVICE_ID = "TESTDEVICE-ABCD-1234"
DEVICE_ID_2 = "SECONDDEV-EFGH-5678"


# ── Helpers ───────────────────────────────────────────────────────────────────

def connect_payload(username="alice", password="password123", device_id=DEVICE_ID):
    return {"username": username, "password": password, "device_id": device_id,
            "device_name": "TestMac"}


# ── /connect ──────────────────────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_connect_unregistered_device(client, db_pool):
    """Unregistered device → 403 device_not_registered."""
    await create_user(db_pool)
    resp = await client.post("/connect", json=connect_payload())
    assert resp.status_code == 403
    body = resp.json()
    assert body["detail"]["error"] == "device_not_registered"


@pytest.mark.asyncio
async def test_connect_registered_device_success(client, db_pool):
    """Registered device + correct credentials → 200 with vless_config."""
    uid = await create_user(db_pool)
    await register_device(db_pool, uid, device_id=DEVICE_ID)

    resp = await client.post("/connect", json=connect_payload())
    assert resp.status_code == 200
    body = resp.json()
    assert "access_token" in body
    assert "refresh_token" in body
    vc = body["vless_config"]
    assert "uuid" in vc
    assert vc["server"] == TEST_CONFIG["server"]["ip"]


@pytest.mark.asyncio
async def test_connect_wrong_password(client, db_pool):
    """Wrong password → 401."""
    uid = await create_user(db_pool)
    await register_device(db_pool, uid, device_id=DEVICE_ID)

    resp = await client.post("/connect", json=connect_payload(password="wrongpass"))
    assert resp.status_code == 401


@pytest.mark.asyncio
async def test_connect_unknown_user(client, db_pool):
    """Unknown username → 401."""
    resp = await client.post("/connect", json=connect_payload(username="nobody"))
    assert resp.status_code == 401


@pytest.mark.asyncio
async def test_connect_inactive_user(client, db_pool):
    """Disabled user → 403."""
    uid = await create_user(db_pool, is_active=False)
    await register_device(db_pool, uid, device_id=DEVICE_ID)

    resp = await client.post("/connect", json=connect_payload())
    assert resp.status_code == 403


@pytest.mark.asyncio
async def test_connect_rate_limit(client, db_pool):
    """More than 5 requests from same IP within window → 429."""
    # Make 6 rapid requests (all will fail auth — that's fine, we just check 429)
    for i in range(6):
        resp = await client.post("/connect", json=connect_payload(username="nobody"))
    assert resp.status_code == 429


@pytest.mark.asyncio
async def test_disconnect_clears_session(client, db_pool, redis_client):
    """After disconnect, session is marked inactive and Redis keys are removed."""
    uid = await create_user(db_pool)
    await register_device(db_pool, uid, device_id=DEVICE_ID)

    connect_resp = await client.post("/connect", json=connect_payload())
    assert connect_resp.status_code == 200

    resp = await client.post("/disconnect", json={"device_id": DEVICE_ID})
    assert resp.status_code == 200
    assert resp.json()["status"] == "ok"

    # Session should be inactive in DB
    row = await db_pool.fetchrow(
        "SELECT is_active FROM sessions WHERE device_id="
        "(SELECT id FROM devices WHERE device_fingerprint=$1)",
        DEVICE_ID,
    )
    assert row is not None
    assert row["is_active"] is False

    # Redis key should be gone
    val = await redis_client.get(f"active_session:{DEVICE_ID}")
    assert val is None


@pytest.mark.asyncio
async def test_refresh_token_valid(client, db_pool):
    """Valid refresh token → new access_token."""
    uid = await create_user(db_pool)
    await register_device(db_pool, uid, device_id=DEVICE_ID)

    connect_resp = await client.post("/connect", json=connect_payload())
    refresh_token = connect_resp.json()["refresh_token"]

    resp = await client.post("/refresh", json={"refresh_token": refresh_token})
    assert resp.status_code == 200
    assert "access_token" in resp.json()


@pytest.mark.asyncio
async def test_refresh_token_invalid(client):
    """Invalid refresh token → 401."""
    resp = await client.post("/refresh", json={"refresh_token": "bogus_token"})
    assert resp.status_code == 401


# ── /verify-device ────────────────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_verify_device_valid_code(client, db_pool, redis_client):
    """Valid verification code → registers device and returns vless_config."""
    uid = await create_user(db_pool)

    # Plant a verif code in Redis
    code = "TESTCODE"
    await redis_client.setex(f"verif:{code}", 900, uid)

    payload = {
        "username": "alice", "password": "password123",
        "device_id": DEVICE_ID, "device_name": "TestMac",
        "verification_code": code,
    }
    resp = await client.post("/verify-device", json=payload)
    assert resp.status_code == 200
    body = resp.json()
    assert "access_token" in body
    assert "vless_config" in body

    # Code should be consumed
    assert await redis_client.get(f"verif:{code}") is None

    # Device should be in DB
    row = await db_pool.fetchrow(
        "SELECT device_name FROM devices WHERE device_fingerprint=$1", DEVICE_ID
    )
    assert row is not None


@pytest.mark.asyncio
async def test_verify_device_expired_code(client, db_pool):
    """Missing/expired code → 403."""
    await create_user(db_pool)
    payload = {
        "username": "alice", "password": "password123",
        "device_id": DEVICE_ID, "device_name": "TestMac",
        "verification_code": "EXPIRED1",
    }
    resp = await client.post("/verify-device", json=payload)
    assert resp.status_code == 403


@pytest.mark.asyncio
async def test_verify_device_wrong_user_code(client, db_pool, redis_client):
    """Code issued for different user → 403."""
    uid = await create_user(db_pool, username="alice")
    other_uid = await create_user(db_pool, username="bob")

    code = "BOBSCODE"
    await redis_client.setex(f"verif:{code}", 900, other_uid)  # code for bob, not alice

    payload = {
        "username": "alice", "password": "password123",
        "device_id": DEVICE_ID, "device_name": "TestMac",
        "verification_code": code,
    }
    resp = await client.post("/verify-device", json=payload)
    assert resp.status_code == 403


@pytest.mark.asyncio
async def test_second_connect_deactivates_first_session(client, db_pool):
    """Reconnecting same device closes the previous session."""
    uid = await create_user(db_pool)
    await register_device(db_pool, uid, device_id=DEVICE_ID)

    resp1 = await client.post("/connect", json=connect_payload())
    assert resp1.status_code == 200
    uuid1 = resp1.json()["vless_config"]["uuid"]

    resp2 = await client.post("/connect", json=connect_payload())
    assert resp2.status_code == 200
    uuid2 = resp2.json()["vless_config"]["uuid"]

    assert uuid1 != uuid2  # New UUID issued

    # Only 1 active session
    count = await db_pool.fetchval("SELECT COUNT(*) FROM sessions WHERE is_active=true")
    assert count == 1

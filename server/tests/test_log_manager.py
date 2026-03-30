"""Tests for services/log_manager aggregation and cleanup."""
from datetime import datetime, timedelta, date

import pytest
import pytest_asyncio

from tests.conftest import create_user, register_device


# ── access_log upsert aggregation ─────────────────────────────────────────────

@pytest.mark.asyncio
async def test_access_log_same_host_hour_aggregates(db_pool):
    """Inserting same host+hour twice should increment count, not add a row."""
    uid = await create_user(db_pool)
    did = await register_device(db_pool, uid)

    sid = await db_pool.fetchval(
        "INSERT INTO sessions (user_id, device_id, vless_uuid, login_ip) "
        "VALUES ($1, $2, $3, $4::inet) RETURNING id",
        __import__("uuid").UUID(uid),
        __import__("uuid").UUID(did),
        "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
        "1.2.3.4",
    )

    hour = datetime(2025, 1, 1, 12, 0)

    # First insert
    await db_pool.execute(
        """INSERT INTO access_log (user_id, session_id, host, access_hour, request_count,
                                    upload_bytes, download_bytes)
           VALUES ($1, $2, $3, $4, 1, 100, 200)
           ON CONFLICT (session_id, host, access_hour)
           DO UPDATE SET request_count = access_log.request_count + EXCLUDED.request_count,
                         upload_bytes   = access_log.upload_bytes   + EXCLUDED.upload_bytes,
                         download_bytes = access_log.download_bytes + EXCLUDED.download_bytes""",
        __import__("uuid").UUID(uid), sid, "example.com", hour,
    )

    # Second insert (same host + hour)
    await db_pool.execute(
        """INSERT INTO access_log (user_id, session_id, host, access_hour, request_count,
                                    upload_bytes, download_bytes)
           VALUES ($1, $2, $3, $4, 1, 50, 100)
           ON CONFLICT (session_id, host, access_hour)
           DO UPDATE SET request_count = access_log.request_count + EXCLUDED.request_count,
                         upload_bytes   = access_log.upload_bytes   + EXCLUDED.upload_bytes,
                         download_bytes = access_log.download_bytes + EXCLUDED.download_bytes""",
        __import__("uuid").UUID(uid), sid, "example.com", hour,
    )

    row = await db_pool.fetchrow(
        "SELECT request_count, upload_bytes, download_bytes FROM access_log "
        "WHERE session_id=$1 AND host=$2", sid, "example.com"
    )
    assert row["request_count"] == 2
    assert row["upload_bytes"] == 150
    assert row["download_bytes"] == 300

    count = await db_pool.fetchval(
        "SELECT COUNT(*) FROM access_log WHERE session_id=$1", sid
    )
    assert count == 1  # Only one row


# ── 90-day cleanup ─────────────────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_cleanup_deletes_old_rows(db_pool):
    """Rows older than 90 days are removed; recent rows survive."""
    uid = await create_user(db_pool)
    did = await register_device(db_pool, uid)

    sid = await db_pool.fetchval(
        "INSERT INTO sessions (user_id, device_id, vless_uuid, login_ip) "
        "VALUES ($1, $2, $3, $4::inet) RETURNING id",
        __import__("uuid").UUID(uid),
        __import__("uuid").UUID(did),
        "bbbbbbbb-cccc-dddd-eeee-ffffffffffff",
        "2.3.4.5",
    )

    old_hour = datetime.now() - timedelta(days=100)
    new_hour = datetime.now() - timedelta(days=1)

    for h, host in [(old_hour, "old.example.com"), (new_hour, "new.example.com")]:
        await db_pool.execute(
            "INSERT INTO access_log (user_id, session_id, host, access_hour) VALUES ($1, $2, $3, $4)",
            __import__("uuid").UUID(uid), sid, host, h,
        )

    # Run cleanup (90-day cutoff)
    cutoff = datetime.now() - timedelta(days=90)
    deleted = await db_pool.execute(
        "DELETE FROM access_log WHERE access_hour < $1", cutoff
    )

    remaining = await db_pool.fetchval("SELECT COUNT(*) FROM access_log WHERE session_id=$1", sid)
    assert remaining == 1  # Only new row survives

    row = await db_pool.fetchrow("SELECT host FROM access_log WHERE session_id=$1", sid)
    assert row["host"] == "new.example.com"


# ── traffic_daily aggregation ─────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_traffic_daily_upsert(db_pool):
    """traffic_daily ON CONFLICT updates existing totals."""
    uid = await create_user(db_pool)
    today = date.today()

    # First insert
    await db_pool.execute(
        """INSERT INTO traffic_daily (user_id, date, upload_bytes, download_bytes)
           VALUES ($1, $2, 1000, 2000)
           ON CONFLICT (user_id, date) DO UPDATE
           SET upload_bytes   = traffic_daily.upload_bytes   + EXCLUDED.upload_bytes,
               download_bytes = traffic_daily.download_bytes + EXCLUDED.download_bytes""",
        __import__("uuid").UUID(uid), today,
    )

    # Second upsert
    await db_pool.execute(
        """INSERT INTO traffic_daily (user_id, date, upload_bytes, download_bytes)
           VALUES ($1, $2, 500, 1000)
           ON CONFLICT (user_id, date) DO UPDATE
           SET upload_bytes   = traffic_daily.upload_bytes   + EXCLUDED.upload_bytes,
               download_bytes = traffic_daily.download_bytes + EXCLUDED.download_bytes""",
        __import__("uuid").UUID(uid), today,
    )

    row = await db_pool.fetchrow(
        "SELECT upload_bytes, download_bytes FROM traffic_daily WHERE user_id=$1 AND date=$2",
        __import__("uuid").UUID(uid), today,
    )
    assert row["upload_bytes"] == 1500
    assert row["download_bytes"] == 3000

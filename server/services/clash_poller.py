"""Background task: polls sing-box Clash API and writes access_log to PostgreSQL."""
import asyncio
import json
import logging
import uuid
from datetime import datetime, timezone

import httpx

log = logging.getLogger("weiai.clash_poller")

# Track the last-seen upload/download per connection ID to compute deltas
_prev_stats: dict[str, dict] = {}


async def run(app):
    """Runs forever. Poll Clash API every 10 seconds."""
    while True:
        try:
            await _poll_once(app)
        except asyncio.CancelledError:
            return
        except Exception as e:
            log.debug(f"Clash API poll error (will retry): {e}")
        await asyncio.sleep(10)


async def _poll_once(app):
    cfg = app.state.cfg
    clash_url = cfg["sing_box"]["clash_api_url"]
    db = app.state.db
    redis = app.state.redis

    async with httpx.AsyncClient(timeout=5.0) as client:
        try:
            resp = await client.get(f"{clash_url}/connections")
            resp.raise_for_status()
        except Exception:
            # sing-box not running locally during dev — skip silently
            return

        data = resp.json()
        connections = data.get("connections", [])
        active_ids = set()

        for conn in connections:
            conn_id = conn.get("id", "")
            active_ids.add(conn_id)

            # Extract vless UUID from the connection chains
            vless_uuid = _extract_vless_uuid(conn)
            if not vless_uuid:
                continue

            session_raw = await redis.get(f"vless_map:{vless_uuid}")
            if not session_raw:
                continue

            session_info = json.loads(session_raw)
            user_id = session_info["user_id"]
            session_id = session_info["session_id"]

            host = (
                conn.get("metadata", {}).get("host")
                or conn.get("metadata", {}).get("destinationIP")
                or "unknown"
            )
            if not host or host == "unknown":
                continue

            # Compute delta bytes since last poll
            upload_total = conn.get("upload", 0)
            download_total = conn.get("download", 0)
            prev = _prev_stats.get(conn_id, {"upload": 0, "download": 0})
            upload_delta = max(0, upload_total - prev["upload"])
            download_delta = max(0, download_total - prev["download"])
            _prev_stats[conn_id] = {"upload": upload_total, "download": download_total}

            if upload_delta == 0 and download_delta == 0:
                continue

            # Truncate timestamp to the current hour for aggregation
            start_str = conn.get("start", "")
            try:
                access_hour = datetime.fromisoformat(
                    start_str.replace("Z", "+00:00")
                ).replace(minute=0, second=0, microsecond=0)
            except Exception:
                access_hour = datetime.now(timezone.utc).replace(minute=0, second=0, microsecond=0)

            # Upsert into access_log (aggregate same host within the same hour)
            await db.execute(
                """INSERT INTO access_log
                     (user_id, session_id, host, access_hour, upload_bytes, download_bytes)
                   VALUES ($1, $2, $3, $4, $5, $6)
                   ON CONFLICT (session_id, host, access_hour)
                   DO UPDATE SET
                     request_count  = access_log.request_count + 1,
                     upload_bytes   = access_log.upload_bytes + EXCLUDED.upload_bytes,
                     download_bytes = access_log.download_bytes + EXCLUDED.download_bytes
                """,
                uuid.UUID(user_id),
                uuid.UUID(session_id),
                _normalize_host(host),
                access_hour,
                upload_delta,
                download_delta,
            )

            # Update running totals in sessions table
            await db.execute(
                """UPDATE sessions SET upload_bytes = upload_bytes + $1,
                                       download_bytes = download_bytes + $2
                   WHERE id = $3""",
                upload_delta, download_delta, uuid.UUID(session_id),
            )

        # Prune stale entries from our local delta tracker
        stale = [cid for cid in _prev_stats if cid not in active_ids]
        for cid in stale:
            del _prev_stats[cid]


def _extract_vless_uuid(conn: dict) -> str | None:
    """Extract the VLESS user UUID from the connection metadata or chains."""
    # sing-box Clash API puts the outbound chain in "chains"
    # The vless inbound user UUID appears in metadata.sourceIP correlation
    # In practice, we key off the outbound tag name since per-user UUIDs
    # are stored in Redis under vless_map:{uuid}. We iterate all known
    # active UUIDs to find a match — the Clash API doesn't expose the UUID
    # directly, but we can match via conn["id"] mapped to our Redis data.
    #
    # Better: sing-box 1.13+ includes "inboundUser" in connection metadata.
    metadata = conn.get("metadata", {})
    inbound_user = metadata.get("inboundUser", {})
    if isinstance(inbound_user, dict):
        return inbound_user.get("uuid")
    # Fallback: look for uuid-shaped string in chains
    for chain_item in conn.get("chains", []):
        if len(chain_item) == 36 and chain_item.count("-") == 4:
            return chain_item
    return None


def _normalize_host(host: str) -> str:
    """Strip port from host:port strings, truncate to 253 chars."""
    if ":" in host and not host.startswith("["):
        host = host.rsplit(":", 1)[0]
    return host[:253]

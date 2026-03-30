"""Background task: nightly log cleanup and traffic_daily aggregation."""
import asyncio
import logging
from datetime import date, datetime, timedelta, timezone

log = logging.getLogger("weiai.log_manager")


async def cleanup_loop(app):
    """Runs forever. At 3am each night, purge old logs and aggregate daily stats."""
    while True:
        try:
            await _sleep_until_3am()
            await _run_cleanup(app)
        except asyncio.CancelledError:
            return
        except Exception as e:
            log.error(f"Log cleanup error: {e}")
            await asyncio.sleep(3600)  # retry in 1 hour on error


async def _sleep_until_3am():
    now = datetime.now()
    target = now.replace(hour=3, minute=0, second=0, microsecond=0)
    if target <= now:
        target += timedelta(days=1)
    wait_seconds = (target - now).total_seconds()
    log.debug(f"Next log cleanup in {wait_seconds/3600:.1f} hours")
    await asyncio.sleep(wait_seconds)


async def _run_cleanup(app):
    import config_loader
    cfg = config_loader.get()
    db = app.state.db
    retention_days = cfg["log"]["retention_days"]
    max_domains = cfg["log"]["max_domains_per_user_per_day"]

    # 1. Delete access_log rows older than retention_days
    cutoff = datetime.now() - timedelta(days=retention_days)
    deleted = await db.fetchval(
        "DELETE FROM access_log WHERE access_hour < $1 RETURNING COUNT(*)",
        cutoff,
    )
    log.info(f"Purged {deleted or 0} access_log rows older than {retention_days} days")

    # 2. Aggregate yesterday's sessions into traffic_daily
    yesterday = date.today() - timedelta(days=1)
    await db.execute(
        """INSERT INTO traffic_daily (user_id, date, upload_bytes, download_bytes)
           SELECT user_id,
                  $1::date,
                  COALESCE(SUM(upload_bytes), 0),
                  COALESCE(SUM(download_bytes), 0)
           FROM sessions
           WHERE DATE(connected_at) = $1
           GROUP BY user_id
           ON CONFLICT (user_id, date)
           DO UPDATE SET
             upload_bytes   = EXCLUDED.upload_bytes,
             download_bytes = EXCLUDED.download_bytes
        """,
        yesterday,
    )
    log.info(f"Aggregated traffic_daily for {yesterday}")

    # 3. Cap access_log per user per day to max_domains (keep highest-traffic hosts)
    await db.execute(
        """DELETE FROM access_log
           WHERE id IN (
             SELECT id FROM (
               SELECT id,
                      ROW_NUMBER() OVER (
                        PARTITION BY user_id, DATE(access_hour)
                        ORDER BY download_bytes DESC, upload_bytes DESC
                      ) AS rn
               FROM access_log
               WHERE access_hour >= $1
             ) ranked
             WHERE rn > $2
           )
        """,
        cutoff,
        max_domains,
    )
    log.info("Log cleanup complete")

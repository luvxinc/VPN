"""GeoIP lookup using MaxMind GeoLite2-City.mmdb."""
import logging
from pathlib import Path

log = logging.getLogger("weiai.geoip")
_reader = None


def _get_reader():
    global _reader
    if _reader is not None:
        return _reader

    import config_loader
    cfg = config_loader.get()
    db_path = Path(cfg["geoip"]["db_path"])
    if not db_path.is_absolute():
        db_path = Path(__file__).parent.parent / db_path

    if not db_path.exists():
        log.warning(
            f"GeoLite2-City.mmdb not found at {db_path}. "
            "Download free copy from https://dev.maxmind.com/geoip/geolite2-free-geolocation-data"
        )
        return None

    try:
        import geoip2.database
        _reader = geoip2.database.Reader(str(db_path))
        log.info(f"GeoIP database loaded from {db_path}")
    except Exception as e:
        log.warning(f"Failed to load GeoIP database: {e}")
        _reader = None

    return _reader


def lookup_ip(ip: str) -> tuple[str, str]:
    """Return (country, city) for the given IP. Falls back to ('Unknown', 'Unknown')."""
    # Skip private/loopback IPs
    if ip.startswith(("127.", "192.168.", "10.", "172.", "::1", "localhost")):
        return "Local", "Local"

    reader = _get_reader()
    if reader is None:
        return "Unknown", "Unknown"

    try:
        result = reader.city(ip)
        country = result.country.name or "Unknown"
        city = result.city.name or "Unknown"
        return country, city
    except Exception:
        return "Unknown", "Unknown"

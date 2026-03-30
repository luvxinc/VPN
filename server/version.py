"""Single source of truth for WeiAi VPN server version."""

# Format: MAJOR.MINOR.PATCH
# MAJOR — breaking protocol change (client must update)
# MINOR — new feature, backward-compatible
# PATCH — bug fix, no API change
VERSION = "1.0.0"

# Minimum client version the server accepts
# Bump when a client-side protocol change is required
MIN_CLIENT_VERSION = "1.0.0"

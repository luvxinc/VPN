#!/bin/sh
# WeiAi VPN privileged helper — installed to /usr/local/bin/weiai-helper
# All actions that require root: route changes, sing-box lifecycle, kill switch.
ACTION="$1"; shift
case "$ACTION" in
  launch)
    # args: <sb_path> <cfg_path> <pid_path> <gateway> <server_ip> [ws_ip ...]
    SB="$1" CFG="$2" PID="$3" GW="$4" SRV="$5"; shift 5
    /sbin/route delete -net 0.0.0.0/1 127.0.0.1 2>/dev/null || true
    /sbin/route delete -net 128.0.0.0/1 127.0.0.1 2>/dev/null || true
    rm -f /tmp/weiai_ks_active
    /sbin/route add -host "$SRV" "$GW" 2>/dev/null || true
    for ip in "$@"; do /sbin/route add -host "$ip" "$GW" 2>/dev/null || true; done
    "$SB" run -c "$CFG" > /tmp/weiai_sb.log 2>&1 &
    echo $! > "$PID"
    # Wait up to 12 s for sing-box Clash API to be ready before handing control back.
    # This ensures strict_route is fully active AND the proxy chain is established
    # before the Swift layer declares "connected".
    i=0
    while [ $i -lt 24 ]; do
      sleep 0.5
      /usr/bin/curl -sf --max-time 1 http://127.0.0.1:9091/version >/dev/null 2>&1 && exit 0
      i=$((i + 1))
    done
    # sing-box failed to become ready in time — clean up and signal failure
    P=$(cat "$PID" 2>/dev/null)
    [ -n "$P" ] && kill "$P" 2>/dev/null || true
    rm -f "$PID"
    exit 1
    ;;
  stop)
    # args: <pid_path> <server_ip> [ws_ip ...]
    PIDFILE="$1" SRV="$2"; shift 2
    P=$(cat "$PIDFILE" 2>/dev/null)
    [ -n "$P" ] && kill "$P" 2>/dev/null || true
    /sbin/route delete -host "$SRV" 2>/dev/null || true
    for ip in "$@"; do /sbin/route delete -host "$ip" 2>/dev/null || true; done
    rm -f "$PIDFILE"
    ;;
  ks-on)
    /sbin/route add -net 0.0.0.0/1 127.0.0.1 2>/dev/null || true
    /sbin/route add -net 128.0.0.0/1 127.0.0.1 2>/dev/null || true
    touch /tmp/weiai_ks_active
    ;;
  ks-off)
    /sbin/route delete -net 0.0.0.0/1 127.0.0.1 2>/dev/null || true
    /sbin/route delete -net 128.0.0.0/1 127.0.0.1 2>/dev/null || true
    rm -f /tmp/weiai_ks_active
    ;;
esac

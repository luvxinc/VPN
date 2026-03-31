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

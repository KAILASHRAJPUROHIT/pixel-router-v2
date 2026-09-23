#!/system/bin/sh
# Queues a command for pc_command_watcher.py (runs on the PC as a
# Windows Scheduled Task, D:\AI_PROJECTS\pixel-router\ai) to pick up on
# its next poll. There's no direct phone->PC connectivity path - the
# PC's Tailscale IP would be the obvious choice, but Tailscale can't run
# on the phone alongside the WireGuard tunnel this router already
# depends on for Airtel-bypass routing (Android only allows one
# VpnService-based VPN active at a time, and WireGuard has that slot).
# So this flips the direction: the PC polls this small JSON file over
# the same public dashboard URL it already fetches stats from, instead
# of the phone reaching out to the PC. No new inbound exposure on
# either end - same basic-auth-protected static file serving as
# everything else on this dashboard.
BODY=""
if [ "$REQUEST_METHOD" = "POST" ] && [ -n "$CONTENT_LENGTH" ] && [ "$CONTENT_LENGTH" -gt 0 ] 2>/dev/null; then
  BODY=$(dd bs=1 count="$CONTENT_LENGTH" 2>/dev/null)
fi

decode_field() {
  RAW=$(printf '%s' "$BODY" | tr '&' '\n' | sed -n "s/^$1=//p" | head -1)
  DECODED=$(printf '%s' "$RAW" | sed 's/+/ /g; s/%/\\x/g')
  printf '%b' "$DECODED" 2>/dev/null
}

ACTION=$(decode_field action)

case "$ACTION" in
  restart_pc|restart_rustdesk)
    TS=$(date +%s)
    cat > /data/local/tmp/dashboard/pc_command.json.tmp <<EOF
{"command":"$ACTION","ts":$TS}
EOF
    chmod 644 /data/local/tmp/dashboard/pc_command.json.tmp
    mv /data/local/tmp/dashboard/pc_command.json.tmp /data/local/tmp/dashboard/pc_command.json
    ;;
esac

echo "Status: 303 See Other"
echo "Location: /index.html"
echo ""

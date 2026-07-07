#!/system/bin/sh
# One-click fixes for the handful of issues that come up most often on
# this router (added 2026-07-07 alongside debug_console.cgi) - saves a
# trip to the raw command console for the common cases.
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
  reboot)
    # Backgrounded with a short delay so the 303 redirect below actually
    # reaches the browser first - otherwise the connection just dies
    # mid-response and looks like the dashboard itself crashed.
    ( sleep 2; su -c reboot ) &
    ;;
  restart_aod)
    su -c 'am force-stop com.router.aod; settings put secure screensaver_enabled 0; sleep 1; settings put secure screensaver_enabled 1; input keyevent KEYCODE_WAKEUP' >/dev/null 2>&1 &
    ;;
  restart_dashboard)
    su -c '
      pkill -f "sh /data/adb/dashboard/update.sh" 2>/dev/null
      pkill -f "busybox httpd" 2>/dev/null
      sleep 1
      nohup sh /data/adb/dashboard/update.sh > /data/adb/dashboard/update.log 2>&1 < /dev/null &
      nohup /data/adb/magisk/busybox httpd -f -vv -p 8080 -h /data/local/tmp/dashboard -c /data/local/tmp/dashboard/httpd.conf > /data/adb/dashboard/httpd.log 2>&1 < /dev/null &
    ' >/dev/null 2>&1 &
    ;;
  restart_failover)
    su -c 'pkill -f "sh /data/adb/service.d/failover_monitor.sh" 2>/dev/null; sleep 1; nohup sh /data/adb/service.d/failover_monitor.sh > /dev/null 2>&1 < /dev/null &' >/dev/null 2>&1 &
    ;;
  restart_hotspot)
    # Reuses hotspot_on_boot.sh itself rather than a generic `svc wifi`
    # toggle - that script already knows the right `cmd wifi
    # stop-softap`/`start-softap` sequence (confirmed start-softap can
    # silently no-op without the stop first) plus the SSID/password
    # lookup and PMF compatibility workaround, so re-running it is the
    # same as what boot already does.
    su -c 'sh /data/adb/service.d/hotspot_on_boot.sh' >/dev/null 2>&1 &
    ;;
esac

echo "Status: 303 See Other"
echo "Location: /index.html"
echo ""

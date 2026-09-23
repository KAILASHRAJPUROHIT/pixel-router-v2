#!/system/bin/sh
# Every log file under the dashboard state dir just appends forever
# (aod_watchdog.log, failover_monitor.log, connection_optimizer.log,
# etc.) - fine for weeks, but with no bound they'd eventually eat
# storage over months of continuous operation. Truncates any .log file
# over MAX_SIZE_BYTES down to its last KEEP_LINES lines, checked
# hourly - cheap, and loses nothing that matters (these are rolling
# debug logs, not audit records).

STATE_DIR=/data/adb/dashboard
MAX_SIZE_BYTES=1048576   # 1MB
KEEP_LINES=2000
CHECK_INTERVAL=3600       # hourly

while [ "$(getprop sys.boot_completed)" != "1" ]; do
  sleep 2
done
sleep 60

while true; do
  for f in "$STATE_DIR"/*.log; do
    [ -f "$f" ] || continue
    SIZE=$(stat -c %s "$f" 2>/dev/null || echo 0)
    if [ "$SIZE" -gt "$MAX_SIZE_BYTES" ]; then
      tail -n "$KEEP_LINES" "$f" > "$f.tmp" 2>/dev/null && mv "$f.tmp" "$f"
    fi
  done
  sleep "$CHECK_INTERVAL"
done

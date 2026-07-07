#!/system/bin/sh
# Copies the AOD app's self-rendered preview PNG (written every ~5s by
# RouterDreamService.capturePreview() to its own files dir - not
# directly root-readable in the general case, but this device's httpd/
# CGI scripts already run as root, same access level as the rest of
# this dashboard) into the httpd-served directory, added 2026-07-07.
# Dedicated fast loop rather than piggybacking on dashboard_watchdog.sh's
# 120s cycle - a preview lagging 2 minutes behind isn't "live".
SRC=/data/data/com.router.aod/files/aod_preview.png
DEST=/data/local/tmp/dashboard/aod_preview.png
CHECK_INTERVAL=5

while [ "$(getprop sys.boot_completed)" != "1" ]; do
  sleep 2
done
sleep 20

while true; do
  [ -f "$SRC" ] && cp "$SRC" "$DEST" 2>/dev/null
  sleep "$CHECK_INTERVAL"
done

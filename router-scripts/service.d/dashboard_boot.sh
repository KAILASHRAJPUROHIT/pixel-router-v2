#!/system/bin/sh
# Starts the router status dashboard: a background loop that regenerates
# the HTML page, plus a busybox httpd instance serving it on port 8080.
# httpd.conf enforces basic auth on everything under the httpd root -
# added once the dashboard became reachable from the public internet via
# a Cloudflare tunnel, not just the hotspot's own LAN.

while [ "$(getprop sys.boot_completed)" != "1" ]; do
  sleep 2
done
sleep 10

nohup sh /data/adb/dashboard/update.sh > /data/adb/dashboard/update.log 2>&1 &
nohup /data/adb/magisk/busybox httpd -f -p 8080 -h /data/local/tmp/dashboard -c /data/local/tmp/dashboard/httpd.conf > /data/adb/dashboard/httpd.log 2>&1 &

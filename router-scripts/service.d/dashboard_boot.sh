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
# -vv logs each request's source IP and response code to httpd.log -
# useful for debugging, though note it's the tunnel/WireGuard peer
# address on both public paths, not the real internet client (neither
# cloudflared's quick-tunnel mode nor the WireGuard hop preserves the
# original client IP down to this layer), so it can't drive per-IP
# banning here the way Caddy's own access log does on the EC2 side.
nohup /data/adb/magisk/busybox httpd -f -vv -p 8080 -h /data/local/tmp/dashboard -c /data/local/tmp/dashboard/httpd.conf > /data/adb/dashboard/httpd.log 2>&1 &

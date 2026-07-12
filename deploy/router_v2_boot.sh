#!/system/bin/sh
# Starts V2's supervisor at boot - the ONLY thing Magisk's service.d
# needs to know about V2. Every actual service (webserver, hotspot,
# usbtether, forwarder, quota, tunnel, ttlproxy) is started and
# restarted by the supervisor itself from services.json, not by
# separate service.d entries the way V1's ~15 independent scripts
# work. This file lives alongside V1's own service.d scripts but never
# reads or writes anything under V1's paths (/data/adb/dashboard,
# V1's iptables chain names, etc.) - the two are fully isolated.

while [ "$(getprop sys.boot_completed)" != "1" ]; do
  sleep 2
done
sleep 10

nohup /data/adb/router_v2/bin/supervisor \
  --manifest=/data/adb/router_v2/services.json \
  --status=/data/adb/router_v2/state/supervisor_status.json \
  --log=/data/adb/router_v2/logs/supervisor.jsonl \
  --service-log-dir=/data/adb/router_v2/logs \
  --alert-number=+918767635484 \
  > /data/adb/router_v2/logs/supervisor_stdout.log 2>&1 < /dev/null &

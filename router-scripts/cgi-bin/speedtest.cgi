#!/system/bin/sh
# On-demand speed test of the router's own uplink (whatever it's
# currently using - own cellular or a borrowed hotspot), run ON THE
# PHONE ITSELF rather than in the viewer's browser. Deliberately not a
# browser-side JS test: this dashboard is often viewed remotely through
# the Cloudflare tunnel or the EC2/Caddy path, where a browser-side test
# would measure the viewer's own path to a test server, not the
# router's actual uplink - conflating two very different numbers. This
# way the result is the same regardless of who's looking at the
# dashboard or from where.
#
# Uses Cloudflare's public, unauthenticated speed-test endpoints (the
# same ones speed.cloudflare.com itself uses) - no curl on this device
# (busybox build doesn't include it, same constraint as everywhere else
# in this project), so busybox wget's --post-file covers the upload
# leg. Kept deliberately small (5MB down / 2MB up) - enough to get past
# TCP slow-start for a real reading without a long wait or meaningfully
# denting the 300GB/cycle cellular quota (a run here costs ~0.002% of
# it).
BUSYBOX=/data/adb/magisk/busybox
STATE_DIR=/data/adb/dashboard
RESULT_FILE="$STATE_DIR/speedtest_result.json"
UPLOAD_TMP=/data/local/tmp/speedtest_upload.bin

echo "Content-Type: text/plain; charset=utf-8"
echo ""

now_ns() { date +%s%N; }

# Download: 5,000,000 bytes.
T1=$(now_ns)
$BUSYBOX wget -q -O /dev/null -T 20 "https://speed.cloudflare.com/__down?bytes=5000000" 2>/dev/null
DL_OK=$?
T2=$(now_ns)
DOWN_MBPS=$(awk -v t1="$T1" -v t2="$T2" 'BEGIN{d=(t2-t1)/1000000000; if(d<=0)d=0.001; printf "%.2f", (5000000*8)/(d*1000000)}')

# Upload: 2,000,000 bytes (zero-filled - content doesn't matter, only size).
$BUSYBOX dd if=/dev/zero of="$UPLOAD_TMP" bs=1024 count=2000 2>/dev/null
T3=$(now_ns)
$BUSYBOX wget -q -O /dev/null -T 20 --post-file="$UPLOAD_TMP" "https://speed.cloudflare.com/__up" 2>/dev/null
UL_OK=$?
T4=$(now_ns)
UP_MBPS=$(awk -v t1="$T3" -v t2="$T4" 'BEGIN{d=(t2-t1)/1000000000; if(d<=0)d=0.001; printf "%.2f", (2000000*8)/(d*1000000)}')
rm -f "$UPLOAD_TMP"

# Latency: round-trip to Cloudflare's own resolver, matches the target
# actually being used for the throughput legs above.
PING_MS=$(ping -c 3 -W 2 1.1.1.1 2>/dev/null | grep -oE 'time=[0-9.]+' | cut -d= -f2 | awk '{s+=$1; n++} END{if(n>0) printf "%.0f", s/n}')
[ -z "$PING_MS" ] && PING_MS="--"

TS=$(date +%s)

if [ "$DL_OK" != "0" ] || [ "$UL_OK" != "0" ]; then
  echo "Speed test failed - router's uplink may be unreachable right now (internet_type: $(cat "$STATE_DIR/internet_type" 2>/dev/null))"
else
  cat > "$RESULT_FILE.tmp" <<EOF
{"down":$DOWN_MBPS,"up":$UP_MBPS,"ping":"$PING_MS","ts":$TS}
EOF
  chmod 644 "$RESULT_FILE.tmp"
  mv "$RESULT_FILE.tmp" "$RESULT_FILE"
  echo "Down: ${DOWN_MBPS} Mbps  /  Up: ${UP_MBPS} Mbps  /  Ping: ${PING_MS} ms"
fi

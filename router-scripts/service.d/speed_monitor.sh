#!/system/bin/sh
# Dedicated fast-sampling loop for live Down/Up speed, decoupled from
# update.sh's own ~10-15s cycle (which also does thermal/battery/SMS
# forwarder checks etc. each pass, so its own RX/TX sample couldn't
# update much faster without slowing everything else down too).
# Samples every SAMPLE_INTERVAL seconds and writes a small JSON file the
# dashboard's own JS polls independently, so Down/Up updates in place
# without a full page reload the way the rest of the page does.

WWW=/data/local/tmp/dashboard
SAMPLE_INTERVAL=2
BUSYBOX=/data/adb/magisk/busybox
# detect_wan_if/get_bytes - shared with update.sh and
# cellular_quota_monitor.sh, extracted 2026-07-07 after being
# copy-pasted into all three ("kept in sync manually" was the old
# approach here).
. /data/adb/dashboard/lib_net_common.sh

while [ "$(getprop sys.boot_completed)" != "1" ]; do
  sleep 2
done
sleep 20

mkdir -p "$WWW"
WAN_IF=$(detect_wan_if)
[ -z "$WAN_IF" ] && WAN_IF=rmnet1
RX1=$(get_bytes "$WAN_IF" rx)
TX1=$(get_bytes "$WAN_IF" tx)

while true; do
  sleep "$SAMPLE_INTERVAL"
  WAN_IF=$(detect_wan_if)
  [ -z "$WAN_IF" ] && WAN_IF=rmnet1
  RX2=$(get_bytes "$WAN_IF" rx)
  TX2=$(get_bytes "$WAN_IF" tx)

  # Negative delta means the interface got renumbered/reset (reboot,
  # WAN_IF switched between rmnet<->wlan0 on a borrowed-hotspot
  # transition) - clamp to 0 for this one sample rather than showing a
  # nonsensical negative speed.
  DOWN_MBPS=$(awk -v r2="$RX2" -v r1="$RX1" -v s="$SAMPLE_INTERVAL" 'BEGIN{d=(r2-r1)/s; if(d<0)d=0; printf "%.2f", d*8/1000000}')
  UP_MBPS=$(awk -v t2="$TX2" -v t1="$TX1" -v s="$SAMPLE_INTERVAL" 'BEGIN{d=(t2-t1)/s; if(d<0)d=0; printf "%.2f", d*8/1000000}')

  cat > "$WWW/speed.json.tmp" <<EOF
{"down":${DOWN_MBPS},"up":${UP_MBPS}}
EOF
  chmod 644 "$WWW/speed.json.tmp"
  mv "$WWW/speed.json.tmp" "$WWW/speed.json"

  RX1=$RX2
  TX1=$TX2
done

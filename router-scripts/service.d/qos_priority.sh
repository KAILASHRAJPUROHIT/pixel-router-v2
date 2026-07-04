#!/system/bin/sh
# Best-effort QoS: gives one priority device (e.g. the Android TV, for
# smooth streaming) egress priority over the shared cellular uplink
# during contention, using a classic prio+fq_codel setup - the same
# general approach OpenWrt's SQM uses. NEEDS ON-DEVICE VERIFICATION:
# whether this kernel's `tc` binary/module support is present at all is
# unknown until tested live - this script checks and logs rather than
# assuming.
#
# PRIORITY_MAC must be set to the actual priority device's MAC address
# before this does anything - leave unset/empty to disable.

LOG=/data/adb/dashboard/qos_priority.log
PRIORITY_MAC=""   # TODO: set once we identify the TV's real MAC on-device

log() {
  echo "$(date '+%Y-%m-%d %H:%M:%S') - $1" >> "$LOG"
}

detect_wan_if() {
  CANDIDATE=$(ip route show default 2>/dev/null | awk '{for(i=1;i<=NF;i++) if ($i=="dev") print $(i+1)}' | grep '^rmnet' | head -1)
  if [ -n "$CANDIDATE" ]; then
    echo "$CANDIDATE"
  else
    awk '$1 ~ /^rmnet[0-9]+:/ { print $1, $2+$10 }' /proc/net/dev | sort -k2 -rn | head -1 | sed 's/://' | awk '{print $1}'
  fi
}

if [ -z "$PRIORITY_MAC" ]; then
  log "PRIORITY_MAC not set - skipping QoS setup (nothing to prioritize yet)"
  exit 0
fi

if ! command -v tc >/dev/null 2>&1 && ! /data/adb/magisk/busybox tc -h >/dev/null 2>&1; then
  log "No usable 'tc' binary found on this device - QoS setup not possible without a kernel-level traffic control tool. Leaving network untouched."
  exit 0
fi
TC="tc"
command -v tc >/dev/null 2>&1 || TC="/data/adb/magisk/busybox tc"

WAN_IF=$(detect_wan_if)
[ -z "$WAN_IF" ] && WAN_IF=rmnet1

PRIORITY_IP=$(ip neigh show | grep -i "$PRIORITY_MAC" | grep lladdr | head -1 | awk '{print $1}')
if [ -z "$PRIORITY_IP" ]; then
  log "Priority device (MAC $PRIORITY_MAC) not currently connected - skipping this pass"
  exit 0
fi

# Reset any prior qdisc/filter state on this interface before reapplying,
# so repeated runs (e.g. after a WAN interface change) don't stack rules.
$TC qdisc del dev "$WAN_IF" root 2>/dev/null

$TC qdisc add dev "$WAN_IF" root handle 1: prio bands 3 priomap 1 2 2 2 1 2 0 0 1 1 1 1 1 1 1 1
$TC qdisc add dev "$WAN_IF" parent 1:1 handle 10: fq_codel
$TC qdisc add dev "$WAN_IF" parent 1:2 handle 20: fq_codel
$TC qdisc add dev "$WAN_IF" parent 1:3 handle 30: fq_codel

$TC filter add dev "$WAN_IF" protocol ip parent 1:0 prio 1 u32 match ip src "$PRIORITY_IP" flowid 1:1
$TC filter add dev "$WAN_IF" protocol ip parent 1:0 prio 1 u32 match ip dst "$PRIORITY_IP" flowid 1:1

log "QoS applied on $WAN_IF: priority device $PRIORITY_MAC ($PRIORITY_IP) mapped to band 1:1"

#!/system/bin/sh
# Regenerates the router status page: clock, live up/down speed, connected
# devices, and cumulative total data used (persists across reboots).
# Served by busybox httpd on port 8080, and shown periodically by the
# kiosk-pulse scheduler (wake screen -> show -> sleep).

WWW=/data/local/tmp/dashboard
STATE_DIR=/data/adb/dashboard
TOTAL_FILE="$STATE_DIR/total_data_bytes"
PERDEV_DIR="$STATE_DIR/perdevice"
BUSYBOX=/data/adb/magisk/busybox

mkdir -p "$WWW" "$STATE_DIR" "$PERDEV_DIR"
[ -f "$TOTAL_FILE" ] || echo 0 > "$TOTAL_FILE"

# Per-device usage accounting via iptables. NAT/masquerade means the
# FORWARD chain sees the client's real LAN IP on both legs - upload
# packets still have src=<client IP> (not yet rewritten), and download
# return packets already have dst=<client IP> (conntrack un-NATs before
# FORWARD). So matching -s/-d by IP in FORWARD, not by MAC, is what
# actually works for both directions - MAC only survives on the AP-side
# leg, not the WAN-side return leg. IP-keyed rather than MAC-keyed means
# a DHCP lease change mid-session is a known, accepted edge case (rare
# on a home hotspot with long leases per device).
ensure_device_accounting() {
  IP="$1"
  TAG=$(echo "$IP" | tr '.' '_' | tr ':' '_')
  if ! iptables -C FORWARD -s "$IP" -j ACCEPT -m comment --comment "devacct_up_$TAG" 2>/dev/null; then
    iptables -I FORWARD 1 -s "$IP" -j ACCEPT -m comment --comment "devacct_up_$TAG"
  fi
  if ! iptables -C FORWARD -d "$IP" -j ACCEPT -m comment --comment "devacct_dn_$TAG" 2>/dev/null; then
    iptables -I FORWARD 1 -d "$IP" -j ACCEPT -m comment --comment "devacct_dn_$TAG"
  fi
}

get_accounting_bytes() {
  # $1 = comment tag -> prints byte count from the matching iptables rule (0 if not found)
  iptables -L FORWARD -v -x -n 2>/dev/null | grep "$1" | head -1 | awk '{print $2}'
}

update_device_total() {
  # $1=ip $2=mac -> updates PERDEV_DIR/<mac>.bytes, awk-based (overflow-safe)
  IP="$1"; MAC="$2"
  TAG=$(echo "$IP" | tr '.' '_' | tr ':' '_')
  UP_BYTES=$(get_accounting_bytes "devacct_up_$TAG")
  DN_BYTES=$(get_accounting_bytes "devacct_dn_$TAG")
  [ -z "$UP_BYTES" ] && UP_BYTES=0
  [ -z "$DN_BYTES" ] && DN_BYTES=0
  CUR_SUM=$(awk -v u="$UP_BYTES" -v d="$DN_BYTES" 'BEGIN{printf "%.0f", u+d}')

  SAFE_MAC=$(echo "$MAC" | tr ':' '_')
  DEV_FILE="$PERDEV_DIR/${SAFE_MAC}.bytes"
  PREV_FILE="$PERDEV_DIR/${SAFE_MAC}.prevsum"
  [ -f "$DEV_FILE" ] || echo 0 > "$DEV_FILE"
  PREV=$(cat "$PREV_FILE" 2>/dev/null || echo 0)
  STORED=$(cat "$DEV_FILE" 2>/dev/null || echo 0)
  # iptables counters reset to 0 on every device (re)join since the rule
  # is freshly created then - treat a drop below the last-seen value as
  # a fresh session, same convention as the overall total's own reset case.
  NEW_TOTAL=$(awk -v s="$STORED" -v c="$CUR_SUM" -v p="$PREV" 'BEGIN{
    delta = (c >= p) ? (c - p) : c
    printf "%.0f", s + delta
  }')
  echo "$NEW_TOTAL" > "$DEV_FILE"
  echo "$CUR_SUM" > "$PREV_FILE"
  awk -v b="$NEW_TOTAL" 'BEGIN{printf "%.2f", b/1000000000}'
}

detect_ap_if() {
  if ip addr show ap_br_wlan2 >/dev/null 2>&1; then
    echo ap_br_wlan2
  else
    echo wlan2
  fi
}

detect_wan_if() {
  # Only trust a default-route interface if it's actually a cellular
  # (rmnet*) interface - the default route can transiently point at the
  # AP bridge itself, which would mislabel LAN traffic as WAN throughput.
  CANDIDATE=$(ip route show default 2>/dev/null | awk '{for(i=1;i<=NF;i++) if ($i=="dev") print $(i+1)}' | grep '^rmnet' | head -1)
  if [ -n "$CANDIDATE" ]; then
    echo "$CANDIDATE"
  else
    # Fall back to whichever rmnet* interface has moved the most bytes.
    awk '$1 ~ /^rmnet[0-9]+:/ { print $1, $2+$10 }' /proc/net/dev | sort -k2 -rn | head -1 | sed 's/://' | awk '{print $1}'
  fi
}

get_bytes() {
  # $1 = interface, $2 = rx|tx -> prints byte count (0 if interface missing)
  awk -v ifc="$1:" -v field="$2" '
    $1==ifc {
      if (field=="rx") print $2
      else print $10
      found=1
    }
    END { if (!found) print 0 }' /proc/net/dev
}

# Sum RX+TX across all rmnet* interfaces - robust to the active WAN
# interface being renumbered across reconnects.
sum_all_rmnet_bytes() {
  awk '
    $1 ~ /^rmnet[0-9]+:/ { rx+=$2; tx+=$10 }
    END { print rx+tx }' /proc/net/dev
}

PREV_SUM=$(sum_all_rmnet_bytes)

while true; do
  AP_IF=$(detect_ap_if)
  WAN_IF=$(detect_wan_if)
  [ -z "$WAN_IF" ] && WAN_IF=rmnet1

  RX1=$(get_bytes "$WAN_IF" rx)
  TX1=$(get_bytes "$WAN_IF" tx)
  sleep 5
  RX2=$(get_bytes "$WAN_IF" rx)
  TX2=$(get_bytes "$WAN_IF" tx)

  # RX1/RX2/TX1/TX2 are cumulative since last boot, not since this script
  # started - on an always-on router they cross the ~2GB 32-bit boundary
  # within days, so this diff must stay in awk (double precision), never
  # shell $(( )) - same overflow class as the total-bytes bug below.
  # Negative deltas mean the interface was renumbered mid-window - clamp to 0.
  DOWN_MBPS=$(awk -v r2="$RX2" -v r1="$RX1" 'BEGIN{d=(r2-r1)/5; if(d<0)d=0; printf "%.2f", d*8/1000000}')
  UP_MBPS=$(awk -v t2="$TX2" -v t1="$TX1" 'BEGIN{d=(t2-t1)/5; if(d<0)d=0; printf "%.2f", d*8/1000000}')

  # Cumulative total: add the positive delta since last check to the
  # persistent counter. A drop (reboot/interface reset) is treated as a
  # fresh session - just add the new value directly, not a negative delta.
  CUR_SUM=$(sum_all_rmnet_bytes)
  # Use awk (double-precision) for the running total, not shell $(( )) -
  # this device's ash arithmetic is 32-bit signed and silently wraps
  # negative once cumulative bytes cross ~2^31 (~2GB) - confirmed by
  # total_data_bytes actually going negative (-1948575436) in testing.
  STORED_TOTAL=$(cat "$TOTAL_FILE" 2>/dev/null || echo 0)
  NEW_TOTAL=$(awk -v s="$STORED_TOTAL" -v c="$CUR_SUM" -v p="$PREV_SUM" 'BEGIN{
    delta = (c >= p) ? (c - p) : c
    printf "%.0f", s + delta
  }')
  echo "$NEW_TOTAL" > "$TOTAL_FILE"
  PREV_SUM=$CUR_SUM
  TOTAL_GB=$(awk -v b="$NEW_TOTAL" 'BEGIN{printf "%.2f", b/1000000000}')

  # Only lines with a resolved lladdr are real clients (FAILED = stale/gone).
  # Each device can show multiple rows (IPv4 + IPv6 + link-local sharing one
  # MAC), so group by MAC and keep the first (usually IPv4) address per device.
  CLIENTS=$($BUSYBOX ip neigh show dev "$AP_IF" 2>/dev/null | grep "lladdr")
  DEVICE_ROWS=$(echo "$CLIENTS" | awk '
    { mac=$3; ip=$1 }
    mac!="" && !seen[mac] { seen[mac]=1; order[++n]=mac; ipof[mac]=ip }
    END { for (i=1;i<=n;i++) printf "%s|%s\n", ipof[order[i]], order[i] }')
  CLIENT_COUNT=$(echo "$DEVICE_ROWS" | grep -c .)
  [ -z "$DEVICE_ROWS" ] && CLIENT_COUNT=0

  BATT_LEVEL=$(dumpsys battery | grep -m1 "level:" | awk '{print $2}')
  BATT_TEMP=$(dumpsys battery | grep -m1 "temperature:" | awk '{printf "%.1f", $2/10}')

  # Thermal status: 0=none 1=light 2=moderate 3=severe 4=critical 5=emergency 6=shutdown
  THERMAL_STATUS=$(dumpsys thermalservice 2>/dev/null | grep -m1 "^Thermal Status:" | awk '{print $3}')
  SKIN_TEMP=$(dumpsys thermalservice 2>/dev/null | grep "^	Temperature{.*VIRTUAL-SKIN" | tail -1 | grep -oE 'mValue=[0-9.]+' | cut -d= -f2 | awk '{printf "%.1f", $1}')
  case "$THERMAL_STATUS" in
    0) THERMAL_LABEL="Normal"; THERMAL_COLOR="#2ecc71" ;;
    1) THERMAL_LABEL="Light"; THERMAL_COLOR="#2ecc71" ;;
    2) THERMAL_LABEL="Moderate"; THERMAL_COLOR="#f1c40f" ;;
    3) THERMAL_LABEL="Severe"; THERMAL_COLOR="#e67e22" ;;
    4) THERMAL_LABEL="Critical"; THERMAL_COLOR="#e74c3c" ;;
    5|6) THERMAL_LABEL="Emergency"; THERMAL_COLOR="#c0392b" ;;
    *) THERMAL_LABEL="Unknown"; THERMAL_COLOR="#888" ;;
  esac

  # SMS forwarder health: is the app enabled right now?
  FWD_STATE=$(pm dump cn.ppps.forwarder 2>/dev/null | grep -o "enabled=[01]" | head -1)
  if [ "$FWD_STATE" = "enabled=1" ]; then
    FWD_LABEL="OK"; FWD_COLOR="#2ecc71"
  else
    FWD_LABEL="DOWN"; FWD_COLOR="#e74c3c"
  fi

  # Real internet reachability (not just "cell registered") - a single
  # short-timeout ping out the WAN interface. 2s worst case is fine inside
  # a 5s-per-cycle loop.
  if ping -c 1 -W 2 8.8.8.8 >/dev/null 2>&1; then
    NET_LABEL="Online"; NET_COLOR="#2ecc71"
  else
    NET_LABEL="Offline"; NET_COLOR="#e74c3c"
  fi

  # Cellular signal quality from RSRP. This SIM is LTE-only (Jio 4G) -
  # the LTE rsrp field lives under mLte=CellSignalStrengthLte inside the
  # mSignalStrength= line, NOT "ssRsrp" (that field is 5G-NR-specific and
  # always reports the 2147483647 "not measured" sentinel on an LTE-only
  # connection - confirmed via raw dumpsys telephony.registry output).
  TDUMP=$(dumpsys telephony.registry 2>/dev/null)
  SIG_LINE=$(echo "$TDUMP" | grep -m1 "mSignalStrength=")
  RSRP=$(echo "$SIG_LINE" | grep -oE "mLte=CellSignalStrengthLte:[^,]*rsrp=-?[0-9]+" | grep -oE 'rsrp=-?[0-9]+' | cut -d= -f2)
  if [ -z "$RSRP" ]; then
    SIGNAL_LABEL="Unknown"; SIGNAL_COLOR="#888"
  elif [ "$RSRP" -ge -80 ]; then
    SIGNAL_LABEL="Excellent"; SIGNAL_COLOR="#2ecc71"
  elif [ "$RSRP" -ge -90 ]; then
    SIGNAL_LABEL="Good"; SIGNAL_COLOR="#2ecc71"
  elif [ "$RSRP" -ge -100 ]; then
    SIGNAL_LABEL="Fair"; SIGNAL_COLOR="#f1c40f"
  else
    SIGNAL_LABEL="Poor"; SIGNAL_COLOR="#e74c3c"
  fi

  # Health traffic-light: one glanceable good/warning/bad signal combining
  # internet reachability, SMS-forwarder health, thermal state, and signal
  # quality - so you don't have to read four separate stats to know "is
  # everything actually fine right now".
  if [ "$NET_LABEL" = "Offline" ] || [ "$FWD_LABEL" = "DOWN" ]; then
    HEALTH_LABEL="Issue"; HEALTH_COLOR="#e74c3c"
  elif { [ -n "$THERMAL_STATUS" ] && [ "$THERMAL_STATUS" -ge 2 ] 2>/dev/null; } || [ "$SIGNAL_LABEL" = "Poor" ] || [ "$SIGNAL_LABEL" = "Unknown" ]; then
    HEALTH_LABEL="Warning"; HEALTH_COLOR="#f1c40f"
  else
    HEALTH_LABEL="Good"; HEALTH_COLOR="#2ecc71"
  fi

  ROWS=""
  if [ "$CLIENT_COUNT" -gt 0 ]; then
    ROWS=$(echo "$DEVICE_ROWS" | awk -F'|' '{print $1"|"$2}' | while IFS='|' read -r dip dmac; do
      ensure_device_accounting "$dip"
      dtotal=$(update_device_total "$dip" "$dmac")
      printf "<tr><td>%s</td><td>%s</td><td>%s GB</td></tr>\n" "$dip" "$dmac" "$dtotal"
    done)
  else
    ROWS="<tr><td colspan=3>No devices connected</td></tr>"
  fi

  NOW_TIME=$(date '+%H:%M')
  NOW_DATE=$(date '+%a, %d %b')

  cat > "$WWW/index.html.tmp" <<EOF
<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta http-equiv="refresh" content="10">
<title>Router Status</title>
<style>
  * { box-sizing:border-box; }
  body { font-family: -apple-system, sans-serif; background:#000; color:#eee; padding:24px; margin:0; }
  .clock { font-size:4.2em; font-weight:200; line-height:1; letter-spacing:-2px; }
  .date { color:#888; font-size:1em; margin-bottom:20px; }
  .stat { display:inline-block; background:#151515; border-radius:12px; padding:14px 22px; margin:5px; min-width:110px; }
  .stat .label { color:#888; font-size:0.75em; text-transform:uppercase; letter-spacing:0.05em; }
  .stat .value { font-size:1.7em; font-weight:600; margin-top:2px; }
  .stat .value.colored { color: var(--c); }
  table { border-collapse:collapse; margin-top:18px; width:100%; max-width:420px; }
  td, th { text-align:left; padding:6px 10px; border-bottom:1px solid #222; font-size:0.85em; }
  th { color:#888; font-weight:normal; }
</style>
</head>
<body>
<div class="clock">${NOW_TIME}</div>
<div class="date">${NOW_DATE}</div>
<div class="stat"><div class="label">Down</div><div class="value">${DOWN_MBPS}</div></div>
<div class="stat"><div class="label">Up</div><div class="value">${UP_MBPS}</div></div>
<div class="stat"><div class="label">Devices</div><div class="value">${CLIENT_COUNT}</div></div>
<div class="stat"><div class="label">Total Used</div><div class="value">${TOTAL_GB} GB</div></div>
<div class="stat"><div class="label">Battery</div><div class="value">${BATT_LEVEL}%</div></div>
<div class="stat"><div class="label">Thermal</div><div class="value colored" style="--c:${THERMAL_COLOR}">${THERMAL_LABEL}</div></div>
<div class="stat"><div class="label">Skin Temp</div><div class="value">${SKIN_TEMP}&deg;C</div></div>
<div class="stat"><div class="label">SMS Fwd</div><div class="value colored" style="--c:${FWD_COLOR}">${FWD_LABEL}</div></div>
<div class="stat"><div class="label">Internet</div><div class="value colored" style="--c:${NET_COLOR}">${NET_LABEL}</div></div>
<div class="stat"><div class="label">Signal</div><div class="value colored" style="--c:${SIGNAL_COLOR}">${SIGNAL_LABEL}</div></div>
<div class="stat"><div class="label">Health</div><div class="value colored" style="--c:${HEALTH_COLOR}">${HEALTH_LABEL}</div></div>
<table>
<tr><th>Device IP</th><th>MAC</th><th>Total Used</th></tr>
${ROWS}
</table>
</body>
</html>
EOF
  mv "$WWW/index.html.tmp" "$WWW/index.html"
done

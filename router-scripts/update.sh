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
# Android's own tethering DHCP server doesn't persist per-client
# hostnames anywhere queryable on this build (dumpsys tethering only
# exposes IP<->MAC, no hostname; no dnsmasq lease file on disk) - so
# there's no way to auto-resolve a device name the way a consumer router
# UI appears to. This file is a manually-maintained MAC->name mapping
# instead, one line per device: "aa:bb:cc:dd:ee:ff=Some Name".
DEVICE_NAMES_FILE="$STATE_DIR/device_names.conf"

lookup_device_name() {
  # $1 = MAC (lowercase, colon-separated) -> prints the mapped name, or
  # "Unknown device" if this MAC hasn't been added to the file yet.
  MAC_LC=$(echo "$1" | tr 'A-Z' 'a-z')
  if [ -f "$DEVICE_NAMES_FILE" ]; then
    NAME=$(grep -i "^${MAC_LC}=" "$DEVICE_NAMES_FILE" | head -1 | cut -d= -f2-)
  fi
  [ -n "$NAME" ] && echo "$NAME" || echo "Unknown device"
}

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
  # While failed-over to a borrowed iPhone hotspot, real WAN traffic
  # flows over wlan0 (the STA link to it), not any rmnet* interface -
  # cellular carries essentially nothing during that time, so Down/Up
  # read as ~0 the whole time we're borrowed. Confirmed as a real bug
  # 2026-07-06 (user reported speeds stuck/not moving while borrowed).
  if [ "$(cat /data/adb/dashboard/internet_type 2>/dev/null)" = "borrowed" ]; then
    echo wlan0
    return
  fi

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

  BLOCKLIST_FILE="$STATE_DIR/mac_blocklist.conf"

  ROWS=""
  if [ "$CLIENT_COUNT" -gt 0 ]; then
    ROWS=$(echo "$DEVICE_ROWS" | awk -F'|' '{print $1"|"$2}' | while IFS='|' read -r dip dmac; do
      ensure_device_accounting "$dip"
      dtotal=$(update_device_total "$dip" "$dmac")
      dname=$(lookup_device_name "$dmac")
      # HTML-escape before embedding in the input's value attribute -
      # dname can come from user-submitted rename_device.cgi text.
      dname_esc=$(printf '%s' "$dname" | sed 's/&/\&amp;/g; s/</\&lt;/g; s/>/\&gt;/g; s/"/\&quot;/g')
      printf '<tr><td>%s</td><td>%s</td><td>%s</td><td>%s GB</td><td>' "$dname" "$dip" "$dmac" "$dtotal"
      printf '<form method="POST" action="/cgi-bin/rename_device.cgi" style="display:flex; gap:4px;">'
      printf '<input type="hidden" name="mac" value="%s">' "$dmac"
      printf '<input type="text" name="name" value="%s" maxlength="60" style="width:120px; background:#151515; color:#eee; border:1px solid #333; border-radius:6px; padding:4px 6px; font-size:0.85em;">' "$dname_esc"
      printf '<button type="submit" style="background:#333; color:#eee; border:none; border-radius:6px; padding:4px 10px; font-size:0.8em;">Save</button>'
      printf '</form></td><td>'
      if [ -f "$BLOCKLIST_FILE" ] && grep -qi "^${dmac}$" "$BLOCKLIST_FILE"; then
        printf '<form method="POST" action="/cgi-bin/toggle_block.cgi">'
        printf '<input type="hidden" name="mac" value="%s"><input type="hidden" name="action" value="unblock">' "$dmac"
        printf '<button type="submit" style="background:#2ecc71; color:#000; border:none; border-radius:6px; padding:4px 10px; font-size:0.8em; font-weight:600;">Allow</button>'
        printf '</form>'
      else
        printf '<form method="POST" action="/cgi-bin/toggle_block.cgi">'
        printf '<input type="hidden" name="mac" value="%s"><input type="hidden" name="action" value="block">' "$dmac"
        printf '<button type="submit" style="background:#e74c3c; color:#fff; border:none; border-radius:6px; padding:4px 10px; font-size:0.8em; font-weight:600;">Deny</button>'
        printf '</form>'
      fi
      printf '</td></tr>\n'
    done)
  else
    ROWS="<tr><td colspan=6>No devices connected</td></tr>"
  fi

  # Recent SMS: mirrors the last N genuinely-received messages (type=1,
  # excludes the forwarder app's own sent/relay copies) straight to the
  # dashboard - separate from cn.ppps.forwarder's own filtering rules
  # (banking/weather still get forwarded per that app's rule per
  # 2026-07-06's blacklist setup), this is just a quick glance without
  # needing to unlock the phone at all.
  #
  # `content query`'s output has one blank-line-separated "Row: N ..."
  # block per message, and message bodies can themselves contain
  # embedded newlines - RS="Row: " (a busybox awk extension, confirmed
  # working here 2026-07-06) splits cleanly on real row boundaries
  # regardless of that. address/date are found by searching for the
  # LAST ", date=" substring rather than the first, since a body could
  # in principle contain that exact text coincidentally.
  # Grouped by sender (2026-07-06 request) - senders are ordered by
  # their most recent message (the query is already date DESC, so a
  # sender's first appearance in the stream is its newest message),
  # with that sender's own messages listed newest-first underneath.
  # ash/busybox has no associative arrays, so grouping is done via
  # per-sender scratch files on disk (keyed by a sanitized filename)
  # plus an order file recording first-seen order - built inside the
  # `while` subshell (piped, so the shell vars themselves don't
  # survive it, but the files it writes do).
  SMS_LIMIT=15
  SMS_TMPDIR="/data/local/tmp/dashboard/.sms_tmp"
  rm -rf "$SMS_TMPDIR"
  mkdir -p "$SMS_TMPDIR"
  ORDER_FILE="$SMS_TMPDIR/order.txt"
  : > "$ORDER_FILE"
  content query --uri content://sms --projection address:body:date --where "type=1" --sort "date DESC" 2>/dev/null | awk -v limit="$SMS_LIMIT" '
    BEGIN { RS="Row: "; count=0 }
    NR>1 {
      if (count >= limit) exit
      rec = $0
      astart = index(rec, "address=") + 8
      aend = index(rec, ", body=") - 1
      addr = substr(rec, astart, aend - astart + 1)
      dpos = 0
      tmp = rec
      offset = 0
      while ((p = index(tmp, ", date=")) > 0) {
        dpos = offset + p
        offset += p + 6
        tmp = substr(tmp, p + 7)
      }
      bstart = aend + 8
      bend = dpos - 1
      body = substr(rec, bstart, bend - bstart + 1)
      datestr = substr(rec, dpos + 7)
      gsub(/[\r\n]+$/, "", datestr)
      gsub(/\n/, " ", body)
      gsub(/\|/, "-", body); gsub(/\|/, "-", addr)
      print addr "|" datestr "|" body
      count++
    }
  ' | while IFS='|' read -r saddr sdate sbody; do
      # HTML-escape - this is real message content, could contain <, >,
      # & (URLs etc). No length truncation - `cut -c` operates on raw
      # bytes not Unicode codepoints, which would corrupt multi-byte
      # Devanagari/Marathi text at the cut boundary (many senders here
      # send Marathi weather alerts); SMS bodies are naturally
      # length-capped anyway so this doesn't risk huge page bloat.
      saddr_esc=$(printf '%s' "$saddr" | sed 's/&/\&amp;/g; s/</\&lt;/g; s/>/\&gt;/g')
      sbody_esc=$(printf '%s' "$sbody" | sed 's/&/\&amp;/g; s/</\&lt;/g; s/>/\&gt;/g')
      # Shell $(( )) arithmetic is 32-bit signed on this device (same
      # class of bug already found in the cumulative-bytes counters
      # elsewhere in this script) - a millisecond epoch (13 digits) is
      # far beyond that range and silently wraps to garbage, confirmed
      # 2026-07-06 (produced "12-Jan" instead of the real date). awk
      # uses double-precision, same fix pattern as those counters.
      sdate_sec=$(awk -v ms="$sdate" 'BEGIN{printf "%.0f", ms/1000}')
      stime=$(date -d "@$sdate_sec" "+%H:%M %d-%b" 2>/dev/null)
      SAFE_ADDR=$(printf '%s' "$saddr" | tr -c 'A-Za-z0-9' '_')
      ADDR_FILE="$SMS_TMPDIR/$SAFE_ADDR.html"
      [ -f "$ADDR_FILE" ] || printf '%s\n' "$saddr_esc" >> "$ORDER_FILE"
      printf '<div style="background:#151515; border-radius:8px; padding:10px 14px; margin-bottom:6px;"><div style="display:flex; justify-content:space-between; color:#888; font-size:0.8em;"><span>%s</span></div><div style="margin-top:4px; font-size:0.9em; word-break:break-word;">%s</div></div>\n' "$stime" "$sbody_esc" >> "$ADDR_FILE"
    done
  SMS_ROWS=""
  while IFS= read -r sender_esc; do
    [ -z "$sender_esc" ] && continue
    SAFE_ADDR=$(printf '%s' "$sender_esc" | tr -c 'A-Za-z0-9' '_')
    ADDR_FILE="$SMS_TMPDIR/$SAFE_ADDR.html"
    [ -f "$ADDR_FILE" ] || continue
    SMS_ROWS="${SMS_ROWS}<h4 style=\"margin:14px 0 6px; color:#ccc; font-size:0.95em;\">${sender_esc}</h4>$(cat "$ADDR_FILE")"
  done < "$ORDER_FILE"
  rm -rf "$SMS_TMPDIR"
  [ -z "$SMS_ROWS" ] && SMS_ROWS='<div style="color:#888;">No messages</div>'

  # AOD status: which Daydream is currently active. idle_photo_switcher.sh
  # flips this between our own stats AOD and Google Photos' built-in
  # screensaver based on idleness/time of day - surface which one is live
  # right now rather than leaving that invisible outside the phone itself.
  CURRENT_DREAM=$(settings get secure screensaver_components 2>/dev/null)
  case "$CURRENT_DREAM" in
    *router.aod*) AOD_LABEL="Stats"; AOD_COLOR="#2ecc71" ;;
    *photos*) AOD_LABEL="Photos"; AOD_COLOR="#3498db" ;;
    *) AOD_LABEL="Off"; AOD_COLOR="#e74c3c" ;;
  esac

  # AOD layout picker: names must stay in the exact order of the
  # AodLayout.java enum (its ordinal is what layout_index actually
  # stores) - there's no way to query the enum's labels from a shell
  # script, so this list is manually kept in sync with that file.
  AOD_LAYOUT_NAMES="Corner stack|Centered radial|Split halves + grid|Sidebar rail|Ticker marquee|Widget cascade|Giant stack|Mega grid|Bento modern|Glyph minimal|One UI minimal (Samsung style)|OnePlus Accent (clean & bold)|OnePlus Horizon (modern & symmetrical)|Samsung One UI information (AOD)|OnePlus Minimal (elegant)|Samsung Ultra watch style|OnePlus Grid (balanced)|OnePlus Pro (smart AOD)|Samsung One UI compact|OnePlus Flux (dynamic)|One UI Color (vibrant)|OnePlus Bold (dynamic)|Neon Cyber (futuristic)|Samsung One UI+ (colorful cards)|Gradient Flow (elegant)|Nature Green (fresh)|Sunset Vibes (warm)|Ocean Blue (calm)|Violet Glow (premium)|Rainbow Dash (playful)"
  CURRENT_LAYOUT_IDX=$(sed -n 's/.*layout_index" value="\([0-9]*\)".*/\1/p' /data/data/com.router.aod/shared_prefs/aod_prefs.xml 2>/dev/null)
  [ -z "$CURRENT_LAYOUT_IDX" ] && CURRENT_LAYOUT_IDX=0
  AOD_LAYOUT_OPTIONS=$(echo "$AOD_LAYOUT_NAMES" | awk -v cur="$CURRENT_LAYOUT_IDX" -F'|' '{
    for (i=1; i<=NF; i++) {
      sel = (i-1 == cur) ? " selected" : ""
      printf "<option value=\"%d\"%s>%s</option>", i-1, sel, $i
    }
  }')

  # AOD color picker: same pattern as the layout picker above - names
  # must stay in the exact order of AodTheme.java's ALL[] array (its
  # index is what theme_index actually stores), manually kept in sync
  # since there's no way to query the array's names from shell.
  AOD_THEME_NAMES="Neon cyberpunk|Glass frost|Retro CRT terminal|Luxury gold|Vaporwave sunset|E-ink newspaper|Holographic|Nature organic|True Black (OLED)"
  CURRENT_THEME_IDX=$(sed -n 's/.*theme_index" value="\([0-9]*\)".*/\1/p' /data/data/com.router.aod/shared_prefs/aod_prefs.xml 2>/dev/null)
  [ -z "$CURRENT_THEME_IDX" ] && CURRENT_THEME_IDX=0
  AOD_THEME_OPTIONS=$(echo "$AOD_THEME_NAMES" | awk -v cur="$CURRENT_THEME_IDX" -F'|' '{
    for (i=1; i<=NF; i++) {
      sel = (i-1 == cur) ? " selected" : ""
      printf "<option value=\"%d\"%s>%s</option>", i-1, sel, $i
    }
  }')

  # Internet type: own cellular, or failed over to a family member's
  # iPhone hotspot (written by failover_monitor.sh) - name the actual
  # source rather than a generic "Self"/"Borrowed" label, so it's
  # obvious at a glance whose hotspot is actually carrying traffic.
  FAILOVER_CONFIG=/data/adb/dashboard/failover.properties
  INTERNET_TYPE=$(cat /data/adb/dashboard/internet_type 2>/dev/null)
  if [ "$INTERNET_TYPE" = "borrowed" ]; then
    BORROWED_SSID_NOW=$(cat /data/adb/dashboard/borrowed_ssid 2>/dev/null)
    P1_NAME=$(grep "^phone1.name=" "$FAILOVER_CONFIG" 2>/dev/null | cut -d= -f2-)
    P1_SSID=$(grep "^phone1.ssid=" "$FAILOVER_CONFIG" 2>/dev/null | cut -d= -f2-)
    P2_NAME=$(grep "^phone2.name=" "$FAILOVER_CONFIG" 2>/dev/null | cut -d= -f2-)
    P2_SSID=$(grep "^phone2.ssid=" "$FAILOVER_CONFIG" 2>/dev/null | cut -d= -f2-)
    if [ -n "$BORROWED_SSID_NOW" ] && [ "$BORROWED_SSID_NOW" = "$P1_SSID" ] && [ -n "$P1_NAME" ]; then
      INTERNET_TYPE_LABEL="${P1_NAME}'s iPhone"
    elif [ -n "$BORROWED_SSID_NOW" ] && [ "$BORROWED_SSID_NOW" = "$P2_SSID" ] && [ -n "$P2_NAME" ]; then
      INTERNET_TYPE_LABEL="${P2_NAME}'s iPhone"
    elif [ -n "$BORROWED_SSID_NOW" ]; then
      INTERNET_TYPE_LABEL="$BORROWED_SSID_NOW"
    else
      INTERNET_TYPE_LABEL="Borrowed"
    fi
    INTERNET_TYPE_COLOR="#f1c40f"
  else
    INTERNET_TYPE_LABEL="Cellular"; INTERNET_TYPE_COLOR="#2ecc71"
  fi

  # Custom message: message.cgi (posted from the form below) writes an
  # expiry epoch + text to this file. Only surface it here while still
  # within its 5-minute window - RouterDreamService.java picks this stat
  # up the same way it already does every other one (generic label/value
  # regex parse) and covers the whole AOD screen with it.
  MSG_STATE=/data/adb/dashboard/custom_message.txt
  CUSTOM_MESSAGE="—"
  if [ -f "$MSG_STATE" ]; then
    MSG_EXPIRY=$(sed -n '1p' "$MSG_STATE")
    MSG_TEXT=$(sed -n '2,$p' "$MSG_STATE")
    NOW_EPOCH=$(date +%s)
    if [ -n "$MSG_EXPIRY" ] && [ "$NOW_EPOCH" -lt "$MSG_EXPIRY" ] 2>/dev/null; then
      CUSTOM_MESSAGE="$MSG_TEXT"
    fi
  fi
  # failover_monitor.sh writes this (no expiry - persists until it
  # clears the file itself, either once a backup hotspot connects or
  # once our own cellular recovers) when it wants a human to manually
  # flip a phone's Personal Hotspot on. Takes priority over the timed
  # custom message since it's actionable, not just informational.
  FAILOVER_PROMPT_FILE=/data/adb/dashboard/failover_prompt.txt
  if [ -s "$FAILOVER_PROMPT_FILE" ]; then
    CUSTOM_MESSAGE=$(cat "$FAILOVER_PROMPT_FILE")
  fi
  # HTML-escape before embedding - this is free-form user input going
  # straight into the page, and it also has to stay parseable by the
  # AOD's label/value regex (which stops at the first "<").
  CUSTOM_MESSAGE_ESC=$(printf '%s' "$CUSTOM_MESSAGE" | sed 's/&/\&amp;/g; s/</\&lt;/g; s/>/\&gt;/g')

  # AKD follower counts: akd_followers_fetch.sh polls AKD's own
  # always-on analytics API every 5 minutes and caches the last-known
  # value per platform (a Meta token expiry means Instagram/Facebook
  # can go stale for days at a time - "--" here just means no
  # successful fetch has ever landed, not necessarily 0 followers).
  AKD_CACHE=/data/adb/dashboard/akd_followers.txt
  AKD_YT="--"; AKD_IG="--"; AKD_FB="--"
  if [ -f "$AKD_CACHE" ]; then
    AKD_YT=$(sed -n '1p' "$AKD_CACHE")
    AKD_IG=$(sed -n '2p' "$AKD_CACHE")
    AKD_FB=$(sed -n '3p' "$AKD_CACHE")
  fi
  AKD_BADGE_TEXT="YT ${AKD_YT} · IG ${AKD_IG} · FB ${AKD_FB}"

  # Cellular data cutoff: cellular_quota_monitor.sh tracks cycle-scoped
  # rmnet bytes separately from the lifetime TOTAL_GB counter above, and
  # disables mobile data once the cycle hits 300GB, staying on borrowed
  # internet until the configured billing day resets it.
  CYCLE_BYTES=$(cat /data/adb/dashboard/cellular_cycle_bytes 2>/dev/null)
  [ -z "$CYCLE_BYTES" ] && CYCLE_BYTES=0
  CYCLE_GB=$(awk -v b="$CYCLE_BYTES" 'BEGIN{printf "%.2f", b/1000000000}')
  BILLING_DAY=$(cat /data/adb/dashboard/billing_cycle_day 2>/dev/null)
  [ -z "$BILLING_DAY" ] && BILLING_DAY=1
  if [ -f /data/adb/dashboard/data_cutoff_active ]; then
    QUOTA_LABEL="${CYCLE_GB} / 300 GB - CUTOFF ACTIVE"
    QUOTA_COLOR="#e74c3c"
  else
    QUOTA_LABEL="${CYCLE_GB} / 300 GB this cycle"
    QUOTA_COLOR="#888"
  fi
  BILLING_DAY_OPTIONS=$(awk -v cur="$BILLING_DAY" 'BEGIN{
    for (i=1; i<=28; i++) {
      sel = (i == cur) ? " selected" : ""
      printf "<option value=\"%d\"%s>%d</option>", i, sel, i
    }
  }')

  # WireGuard tunnel health: vpn_tether_routing.sh writes this every
  # ~30s - surfaced here after a real 2026-07-06/07 incident where the
  # tunnel silently died and nobody noticed until a manual check caught
  # it. Also feeds the AOD's own net_type_badge line so it's visible
  # even without opening the dashboard.
  TUNNEL_ALIVE=$(cat /data/adb/dashboard/tunnel_alive 2>/dev/null)
  if [ ! -d /sys/class/net/tun0 ]; then
    TUNNEL_LABEL="Not present"; TUNNEL_COLOR="#888"
  elif [ "$TUNNEL_ALIVE" = "1" ]; then
    TUNNEL_LABEL="Alive"; TUNNEL_COLOR="#2ecc71"
  else
    TUNNEL_LABEL="DEAD - reconnect WireGuard on the phone"; TUNNEL_COLOR="#e74c3c"
  fi

  # Live CPU/GPU clock, for visibility into aod_power_governor.sh's
  # idle-tier throttling actually taking effect - scaling_cur_freq is the
  # frequency actually in use right now, not the scaling_max_freq ceiling
  # the governor sets.
  CPU_KHZ=$(cat /sys/devices/system/cpu/cpu0/cpufreq/scaling_cur_freq 2>/dev/null)
  CPU_MHZ=$(awk -v k="$CPU_KHZ" 'BEGIN{printf "%.0f", k/1000}')
  GPU_KHZ=$(cat /sys/devices/platform/1c500000.mali/cur_freq 2>/dev/null)
  [ -z "$GPU_KHZ" ] && GPU_KHZ=0
  GPU_MHZ=$(awk -v k="$GPU_KHZ" 'BEGIN{printf "%.0f", k/1000}')

  NOW_TIME=$(date '+%H:%M')
  NOW_DATE=$(date '+%a, %d %b')

  cat > "$WWW/index.html.tmp" <<EOF
<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta http-equiv="refresh" content="10">
<meta http-equiv="Cache-Control" content="no-store, no-cache, must-revalidate, max-age=0">
<meta http-equiv="Pragma" content="no-cache">
<meta http-equiv="Expires" content="0">
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
  table { border-collapse:collapse; margin-top:18px; width:100%; max-width:780px; }
  td, th { text-align:left; padding:6px 10px; border-bottom:1px solid #222; font-size:0.85em; }
  th { color:#888; font-weight:normal; }
</style>
</head>
<body>
<div class="clock">${NOW_TIME}</div>
<div class="date">${NOW_DATE}</div>
<div class="stat"><div class="label">Down</div><div class="value" id="speed-down">${DOWN_MBPS}</div></div>
<div class="stat"><div class="label">Up</div><div class="value" id="speed-up">${UP_MBPS}</div></div>
<div class="stat"><div class="label">Devices</div><div class="value">${CLIENT_COUNT}</div></div>
<div class="stat"><div class="label">Total Used</div><div class="value">${TOTAL_GB} GB</div></div>
<div class="stat"><div class="label">Battery</div><div class="value">${BATT_LEVEL}%</div></div>
<div class="stat"><div class="label">Thermal</div><div class="value colored" style="--c:${THERMAL_COLOR}">${THERMAL_LABEL}</div></div>
<div class="stat"><div class="label">Skin Temp</div><div class="value">${SKIN_TEMP}&deg;C</div></div>
<div class="stat"><div class="label">SMS Fwd</div><div class="value colored" style="--c:${FWD_COLOR}">${FWD_LABEL}</div></div>
<div class="stat"><div class="label">Internet</div><div class="value colored" style="--c:${NET_COLOR}">${NET_LABEL}</div></div>
<div class="stat"><div class="label">Signal</div><div class="value colored" style="--c:${SIGNAL_COLOR}">${SIGNAL_LABEL}</div></div>
<div class="stat"><div class="label">Health</div><div class="value colored" style="--c:${HEALTH_COLOR}">${HEALTH_LABEL}</div></div>
<div class="stat"><div class="label">AOD Status</div><div class="value colored" style="--c:${AOD_COLOR}">${AOD_LABEL}</div></div>
<div class="stat"><div class="label">CPU MHz</div><div class="value">${CPU_MHZ}</div></div>
<div class="stat"><div class="label">GPU MHz</div><div class="value">${GPU_MHZ}</div></div>
<div class="stat"><div class="label">Internet Type</div><div class="value colored" style="--c:${INTERNET_TYPE_COLOR}">${INTERNET_TYPE_LABEL}</div></div>
<div class="stat"><div class="label">Cellular Cycle</div><div class="value colored" style="--c:${QUOTA_COLOR}">${QUOTA_LABEL}</div></div>
<div class="stat"><div class="label">WG Tunnel</div><div class="value colored" style="--c:${TUNNEL_COLOR}">${TUNNEL_LABEL}</div></div>
<div class="stat"><div class="label">AKD</div><div class="value">${AKD_BADGE_TEXT}</div></div>
<div class="stat"><div class="label">Custom Message</div><div class="value">${CUSTOM_MESSAGE_ESC}</div></div>
<form method="POST" action="/cgi-bin/message.cgi" style="margin-top:18px; max-width:420px;">
  <input type="text" name="message" maxlength="200" placeholder="Send a message home..."
    style="width:100%; box-sizing:border-box; background:#151515; color:#eee; border:1px solid #333; border-radius:8px; padding:10px 14px; font-size:1em;">
  <div style="display:flex; gap:8px; margin-top:8px; align-items:center;">
    <input type="number" name="duration_min" value="5" min="1" max="1440"
      style="width:80px; box-sizing:border-box; background:#151515; color:#eee; border:1px solid #333; border-radius:8px; padding:10px 10px; font-size:1em;">
    <span style="color:#888; font-size:0.85em;">minutes</span>
    <button type="submit"
      style="margin-left:auto; background:#2ecc71; color:#000; border:none; border-radius:8px; padding:10px 18px; font-size:0.9em; font-weight:600;">Show on AOD</button>
  </div>
</form>
<form method="POST" action="/cgi-bin/set_aod_layout.cgi" style="margin-top:10px; max-width:420px; display:flex; gap:8px;">
  <select name="layout"
    style="flex:1; background:#151515; color:#eee; border:1px solid #333; border-radius:8px; padding:10px 14px; font-size:1em;">
    ${AOD_LAYOUT_OPTIONS}
  </select>
  <button type="submit"
    style="background:#3498db; color:#000; border:none; border-radius:8px; padding:10px 18px; font-size:0.9em; font-weight:600;">Switch AOD</button>
</form>
<form method="POST" action="/cgi-bin/set_aod_theme.cgi" style="margin-top:10px; max-width:420px; display:flex; gap:8px;">
  <select name="theme"
    style="flex:1; background:#151515; color:#eee; border:1px solid #333; border-radius:8px; padding:10px 14px; font-size:1em;">
    ${AOD_THEME_OPTIONS}
  </select>
  <button type="submit"
    style="background:#9b59b6; color:#fff; border:none; border-radius:8px; padding:10px 18px; font-size:0.9em; font-weight:600;">Switch Color</button>
</form>
<form method="POST" action="/cgi-bin/set_billing_day.cgi" style="margin-top:10px; max-width:420px; display:flex; gap:8px; align-items:center;">
  <span style="color:#888; font-size:0.85em; white-space:nowrap;">Billing cycle day</span>
  <select name="day"
    style="flex:1; background:#151515; color:#eee; border:1px solid #333; border-radius:8px; padding:10px 14px; font-size:1em;">
    ${BILLING_DAY_OPTIONS}
  </select>
  <button type="submit"
    style="background:#16a085; color:#fff; border:none; border-radius:8px; padding:10px 18px; font-size:0.9em; font-weight:600;">Set</button>
</form>
<h3 style="margin-top:24px; margin-bottom:8px; color:#888; font-size:0.85em; text-transform:uppercase; letter-spacing:0.05em; font-weight:normal;">AOD Preview</h3>
<div style="max-width:320px; margin-bottom:14px;">
  <img id="aod-preview-img" src="/aod_preview.png" style="width:100%; border-radius:12px; border:1px solid #333; display:block; background:#0a0a0a;"
    onerror="this.style.display='none'; document.getElementById('aod-preview-fallback').style.display='block';">
  <div id="aod-preview-fallback" style="display:none; color:#888; font-size:0.85em; padding:20px; text-align:center; border:1px dashed #333; border-radius:12px;">
    No preview yet - only captured while the AOD is actually dreaming (not awake, not showing the Photos slideshow).
  </div>
  <div style="color:#666; font-size:0.75em; margin-top:6px;">Updates every ~5s while the AOD is active - may be stale otherwise.</div>
</div>
<h3 style="margin-top:24px; margin-bottom:8px; color:#888; font-size:0.85em; text-transform:uppercase; letter-spacing:0.05em; font-weight:normal;">Debug</h3>
<div style="display:flex; flex-wrap:wrap; gap:8px; max-width:680px; margin-bottom:14px;">
  <form method="POST" action="/cgi-bin/debug_action.cgi" onsubmit="return confirm('Reboot the router now? This will drop the hotspot for ~1-2 minutes.');">
    <input type="hidden" name="action" value="reboot">
    <button type="submit" style="background:#e74c3c; color:#fff; border:none; border-radius:8px; padding:10px 16px; font-size:0.85em; font-weight:600;">Reboot Device</button>
  </form>
  <form method="POST" action="/cgi-bin/debug_action.cgi">
    <input type="hidden" name="action" value="restart_aod">
    <button type="submit" style="background:#151515; color:#eee; border:1px solid #333; border-radius:8px; padding:10px 16px; font-size:0.85em;">Restart AOD</button>
  </form>
  <form method="POST" action="/cgi-bin/debug_action.cgi">
    <input type="hidden" name="action" value="restart_dashboard">
    <button type="submit" style="background:#151515; color:#eee; border:1px solid #333; border-radius:8px; padding:10px 16px; font-size:0.85em;">Restart Dashboard</button>
  </form>
  <form method="POST" action="/cgi-bin/debug_action.cgi">
    <input type="hidden" name="action" value="restart_failover">
    <button type="submit" style="background:#151515; color:#eee; border:1px solid #333; border-radius:8px; padding:10px 16px; font-size:0.85em;">Restart Failover</button>
  </form>
  <form method="POST" action="/cgi-bin/debug_action.cgi" onsubmit="return confirm('Restart the hotspot? Connected devices will briefly drop.');">
    <input type="hidden" name="action" value="restart_hotspot">
    <button type="submit" style="background:#151515; color:#eee; border:1px solid #333; border-radius:8px; padding:10px 16px; font-size:0.85em;">Restart Hotspot</button>
  </form>
</div>
<div style="max-width:680px; margin-bottom:14px;">
  <textarea id="console-cmd" placeholder="shell command, runs as root..." rows="2"
    style="width:100%; box-sizing:border-box; background:#151515; color:#eee; border:1px solid #333; border-radius:8px; padding:10px 14px; font-size:0.9em; font-family:monospace; resize:vertical;"></textarea>
  <div style="display:flex; gap:8px; margin-top:6px;">
    <button type="button" id="console-run"
      style="background:#3498db; color:#000; border:none; border-radius:8px; padding:8px 16px; font-size:0.85em; font-weight:600;">Run</button>
    <span id="console-status" style="color:#888; font-size:0.8em; align-self:center;"></span>
  </div>
  <pre id="console-output" style="background:#0a0a0a; border:1px solid #222; border-radius:8px; padding:10px 14px; margin-top:8px; max-height:280px; overflow:auto; font-size:0.8em; white-space:pre-wrap; word-break:break-word; display:none;"></pre>
</div>
<table>
<tr><th>Device</th><th>IP</th><th>MAC</th><th>Total Used</th><th>Rename</th><th>Access</th></tr>
${ROWS}
</table>
<h3 style="margin-top:24px; margin-bottom:8px; color:#888; font-size:0.85em; text-transform:uppercase; letter-spacing:0.05em; font-weight:normal;">Recent SMS</h3>
<div style="max-width:680px;">
${SMS_ROWS}
</div>
<script>
// Polls speed.json (written every 2s by speed_monitor.sh, a dedicated
// fast-sampling loop separate from this page's own ~10-15s regeneration
// cycle) so Down/Up feels live without a full page reload interrupting
// scroll position or in-progress form input every 10s. A ?t= cache-buster
// is appended on top of {cache:'no-store'} - confirmed 2026-07-06 the
// backend/JSON endpoint itself was updating correctly end-to-end (direct
// curl through the public URL showed real changing values) whenever this
// was reported as "frozen", so the fix is to remove any possibility of an
// intermediate cache (browser disk cache, a proxy) serving a stale copy,
// not a server-side change.
setInterval(function() {
  fetch('/speed.json?t=' + Date.now(), {cache: 'no-store'})
    .then(function(r) { return r.json(); })
    .then(function(d) {
      document.getElementById('speed-down').textContent = d.down;
      document.getElementById('speed-up').textContent = d.up;
    })
    .catch(function() {});
}, 1500);

// Debug console: POSTs the typed command to debug_console.cgi and shows
// whatever it prints (stdout+stderr from a root shell) in the <pre>
// below. No confirm() here unlike the reboot/hotspot buttons - this is
// the "I know what I'm typing" path, same trust level as an adb shell.
document.getElementById('console-run').addEventListener('click', function() {
  var cmd = document.getElementById('console-cmd').value;
  var statusEl = document.getElementById('console-status');
  var outputEl = document.getElementById('console-output');
  if (!cmd.trim()) return;
  statusEl.textContent = 'running...';
  outputEl.style.display = 'none';
  fetch('/cgi-bin/debug_console.cgi', {
    method: 'POST',
    headers: {'Content-Type': 'application/x-www-form-urlencoded'},
    body: 'cmd=' + encodeURIComponent(cmd)
  })
    .then(function(r) { return r.text(); })
    .then(function(t) {
      statusEl.textContent = '';
      outputEl.textContent = t || '(no output)';
      outputEl.style.display = 'block';
    })
    .catch(function(e) {
      statusEl.textContent = '';
      outputEl.textContent = 'Request failed: ' + e;
      outputEl.style.display = 'block';
    });
});

// AOD preview: cache-bust every ~5s (matches the app's own capture
// interval) - a plain <img src> reload would otherwise show a browser-
// cached copy of the very first load forever.
setInterval(function() {
  var img = document.getElementById('aod-preview-img');
  img.src = '/aod_preview.png?t=' + Date.now();
}, 5000);
</script>
</body>
</html>
EOF
  # Security audit 2026-07-05 found this file world-writable (666) -
  # the umask in whatever context this loop runs under produces that
  # for the .tmp file, and mv preserves it. Now served on a public
  # HTTPS URL, so explicitly re-assert 644 every cycle rather than
  # trust the ambient umask.
  chmod 644 "$WWW/index.html.tmp"
  mv "$WWW/index.html.tmp" "$WWW/index.html"
done

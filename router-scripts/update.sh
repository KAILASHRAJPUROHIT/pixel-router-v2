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
# detect_wan_if/get_bytes/sum_all_rmnet_bytes - shared with
# speed_monitor.sh and cellular_quota_monitor.sh, see that file's own
# header comment for why this was extracted 2026-07-07.
. "$STATE_DIR/lib_net_common.sh"
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

# Per-client 2.4GHz/5GHz band, added 2026-07-08 once bridged dual-band
# was working (see hotspot_on_boot.sh) and it became clear clients roam
# between the two radios on their own - a device shown "on 5GHz" a
# minute ago may have already dropped back to 2.4GHz by the next
# dashboard refresh, so this needs to be genuinely live every cycle,
# not a one-time lookup.
#
# `dumpsys wifi` has no per-instance client MAC list in its static
# snapshot (only a live one-off event log when softap first starts, not
# queryable later) and `ip neigh show dev wlan2/wlan3` returns nothing
# (L3 neighbor entries live on the bridge interface, not its enslaved
# physical ports). What does work: the bridge's own MAC forwarding
# table (`brctl showmacs`) tags every learned MAC with which physical
# port it arrived on, including each radio's own "is local" bssid entry
# - cross-referencing that against dumpsys wifi's bssid->frequency
# mapping (the only place that ties an instance name to its live
# frequency) gives a real port->band, then MAC->band.
BAND_BY_BSSID_FILE="$STATE_DIR/band_by_bssid.txt"
PORT_BAND_FILE="$STATE_DIR/port_band.txt"
CLIENT_BAND_FILE="$STATE_DIR/client_bands.txt"

build_client_band_map() {
  > "$CLIENT_BAND_FILE"
  if ! ip addr show ap_br_wlan2 >/dev/null 2>&1; then
    return
  fi
  SAPINFO=$(dumpsys wifi 2>&1 | grep -m1 "mCurrentSoftApInfoMap")
  [ -z "$SAPINFO" ] && return

  echo "$SAPINFO" | grep -oE "frequency= [0-9]+,bssid=[0-9a-f:]+" | \
    awk -F'[ ,=]+' '{freq=$2; bssid=$4; band=(freq<3000)?"2.4GHz":"5GHz"; print bssid, band}' > "$BAND_BY_BSSID_FILE"

  BRMACS_FILE="$STATE_DIR/brmacs.txt"
  $BUSYBOX brctl showmacs ap_br_wlan2 2>/dev/null > "$BRMACS_FILE"

  awk 'FNR==NR { bandof[$1]=$2; next } $3=="yes" { print $1, bandof[$2] }' \
    "$BAND_BY_BSSID_FILE" "$BRMACS_FILE" > "$PORT_BAND_FILE"

  awk 'FNR==NR { bandofport[$1]=$2; next } $3=="no" { print $2"="bandofport[$1] }' \
    "$PORT_BAND_FILE" "$BRMACS_FILE" > "$CLIENT_BAND_FILE"
}

lookup_band() {
  # $1 = MAC -> prints "2.4GHz"/"5GHz" if bridged and known, "--" if the
  # device hasn't been seen on either radio's forwarding table yet
  # (just associated, or not currently bridged - a single-band fallback
  # where AP_IF is plain "wlan2" has no ambiguity to resolve anyway).
  MAC_LC=$(echo "$1" | tr 'A-Z' 'a-z')
  BAND=$(grep -i "^${MAC_LC}=" "$CLIENT_BAND_FILE" 2>/dev/null | head -1 | cut -d= -f2-)
  [ -n "$BAND" ] && echo "$BAND" || echo "--"
}


PREV_SUM=$(sum_all_rmnet_bytes)

while true; do
  AP_IF=$(detect_ap_if)
  build_client_band_map
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
  # BatteryManager status: 2=charging, 5=full, anything else (commonly 4
  # "not charging" here) = not actively charging. Expected to read "No"
  # most of the time even while plugged in - this device uses the
  # native 80% bypass-charge feature (see project history), which holds
  # the battery at its target level without actively topping it up.
  BATT_STATUS_CODE=$(dumpsys battery | grep -m1 "status:" | awk '{print $2}')
  if [ "$BATT_STATUS_CODE" = "2" ]; then
    BATT_CHARGING="Yes"
  else
    BATT_CHARGING="No"
  fi

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
      dband=$(lookup_band "$dmac")
      # HTML-escape before embedding in the input's value attribute -
      # dname can come from user-submitted rename_device.cgi text.
      dname_esc=$(printf '%s' "$dname" | sed 's/&/\&amp;/g; s/</\&lt;/g; s/>/\&gt;/g; s/"/\&quot;/g')
      IS_BLOCKED=0
      [ -f "$BLOCKLIST_FILE" ] && grep -qi "^${dmac}$" "$BLOCKLIST_FILE" && IS_BLOCKED=1
      if [ "$IS_BLOCKED" = "1" ]; then
        STATUS_PILL='<span class="status-pill" style="background:var(--red-bg); color:var(--red);">Blocked</span>'
      else
        STATUS_PILL='<span class="status-pill">Active</span>'
      fi
      printf '<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s GB</td><td>%s</td><td>' "$dname" "$dip" "$dmac" "$dband" "$dtotal" "$STATUS_PILL"
      printf '<form method="POST" action="/cgi-bin/rename_device.cgi" style="display:flex; gap:4px; margin-bottom:6px;">'
      printf '<input type="hidden" name="mac" value="%s">' "$dmac"
      printf '<input type="text" name="name" value="%s" maxlength="60" style="width:110px; padding:4px 6px; font-size:0.85em; border-radius:6px;">' "$dname_esc"
      printf '<button type="submit" class="btn-neutral" style="padding:4px 10px; font-size:0.8em; border-radius:6px;">Save</button>'
      printf '</form>'
      if [ "$IS_BLOCKED" = "1" ]; then
        printf '<form method="POST" action="/cgi-bin/toggle_block.cgi">'
        printf '<input type="hidden" name="mac" value="%s"><input type="hidden" name="action" value="unblock">' "$dmac"
        printf '<button type="submit" class="btn-allow">Allow</button>'
        printf '</form>'
      else
        printf '<form method="POST" action="/cgi-bin/toggle_block.cgi">'
        printf '<input type="hidden" name="mac" value="%s"><input type="hidden" name="action" value="block">' "$dmac"
        printf '<button type="submit" class="btn-block">Block</button>'
        printf '</form>'
      fi
      printf '</td></tr>\n'
    done)
  else
    ROWS="<tr><td colspan=7>No devices connected</td></tr>"
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
      # Rough sender/body-keyword categorization for the dashboard's
      # filter tabs - a simple heuristic, not a real classifier, but
      # good enough to actually filter something rather than every tab
      # showing the same unfiltered list.
      case "$saddr $sbody" in
        *[Jj][Ii][Oo]*) SCAT="jio" ;;
        *[Oo][Tt][Pp]*) SCAT="otp" ;;
        *[Dd]ata*[Qq]uota*|*[Dd]ata*[Uu]sed*|*[Dd]ata*[Bb]alance*) SCAT="data" ;;
        *) SCAT="alerts" ;;
      esac
      printf '<div class="sms-item" data-category="%s"><div class="meta"><span>%s</span></div><div class="body">%s</div></div>\n' "$SCAT" "$stime" "$sbody_esc" >> "$ADDR_FILE"
    done
  SMS_ROWS=""
  while IFS= read -r sender_esc; do
    [ -z "$sender_esc" ] && continue
    SAFE_ADDR=$(printf '%s' "$sender_esc" | tr -c 'A-Za-z0-9' '_')
    ADDR_FILE="$SMS_TMPDIR/$SAFE_ADDR.html"
    [ -f "$ADDR_FILE" ] || continue
    SMS_ROWS="${SMS_ROWS}<h4 class=\"sender-title\">${sender_esc}</h4>$(cat "$ADDR_FILE")"
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
  CURRENT_THEME_NAME=$(echo "$AOD_THEME_NAMES" | awk -v cur="$CURRENT_THEME_IDX" -F'|' '{print $(cur+1)}')
  [ -z "$CURRENT_THEME_NAME" ] && CURRENT_THEME_NAME="Unknown"

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

  # Last speed test result (speedtest.cgi writes this on demand, not on
  # a timer - a real throughput test costs real cellular data, so it
  # only runs when someone clicks the button). Rendered here so the
  # card shows the last known reading immediately on page load instead
  # of being blank until clicked again.
  SPEEDTEST_FILE="$STATE_DIR/speedtest_result.json"
  if [ -f "$SPEEDTEST_FILE" ]; then
    ST_DOWN=$(sed -n 's/.*"down":\([0-9.]*\).*/\1/p' "$SPEEDTEST_FILE")
    ST_UP=$(sed -n 's/.*"up":\([0-9.]*\).*/\1/p' "$SPEEDTEST_FILE")
    ST_PING=$(sed -n 's/.*"ping":"\([^"]*\)".*/\1/p' "$SPEEDTEST_FILE")
    ST_TS=$(sed -n 's/.*"ts":\([0-9]*\).*/\1/p' "$SPEEDTEST_FILE")
    ST_AGE_MIN=$(awk -v ts="$ST_TS" -v now="$(date +%s)" 'BEGIN{printf "%.0f", (now-ts)/60}')
    SPEEDTEST_SUMMARY="Down: ${ST_DOWN} Mbps  /  Up: ${ST_UP} Mbps  /  Ping: ${ST_PING} ms  (tested ${ST_AGE_MIN}m ago)"
  else
    SPEEDTEST_SUMMARY="Not run yet"
  fi

  # Coordinating-manager status (hotspot_manager.sh) for the sidebar's
  # System Status box - separate from HEALTH_LABEL above, which is this
  # script's own narrower internet/thermal/signal read. The manager's
  # status additionally covers whether the AOD and dashboard processes
  # themselves are actually alive, not just what this cycle observes.
  MANAGER_STATUS_FILE="$STATE_DIR/hotspot_manager_status"
  if [ -f "$MANAGER_STATUS_FILE" ]; then
    MGR_HOTSPOT=$(sed -n 's/.*"hotspot":\([01]\).*/\1/p' "$MANAGER_STATUS_FILE")
    MGR_DASH=$(sed -n 's/.*"dashboard":\([01]\).*/\1/p' "$MANAGER_STATUS_FILE")
    MGR_AOD=$(sed -n 's/.*"aod":\([01]\).*/\1/p' "$MANAGER_STATUS_FILE")
    MGR_NET=$(sed -n 's/.*"internet":\([01]\).*/\1/p' "$MANAGER_STATUS_FILE")
    MGR_TS=$(sed -n 's/.*"ts":\([0-9]*\).*/\1/p' "$MANAGER_STATUS_FILE")
    MGR_AGE_SEC=$(awk -v ts="$MGR_TS" -v now="$(date +%s)" 'BEGIN{printf "%.0f", now-ts}')
    if [ "$MGR_HOTSPOT" = "1" ] && [ "$MGR_DASH" = "1" ] && [ "$MGR_AOD" = "1" ] && [ "$MGR_NET" = "1" ]; then
      SYSTEM_STATUS_LABEL="All systems operational"; SYSTEM_STATUS_COLOR="#22c55e"
    else
      SYSTEM_STATUS_LABEL="Attention needed"; SYSTEM_STATUS_COLOR="#ef4444"
    fi
    SYSTEM_STATUS_AGE="Last checked ${MGR_AGE_SEC}s ago"
  else
    SYSTEM_STATUS_LABEL="Manager starting up"; SYSTEM_STATUS_COLOR="#8b93a7"
    SYSTEM_STATUS_AGE="No check yet"
  fi

  # Manager chat history - manager_chat.cgi appends to this (rolling
  # last 40 lines); rendered here so a page reload shows the existing
  # conversation instead of starting blank every time.
  CHAT_HISTORY_FILE="$STATE_DIR/manager_chat_history.jsonl"
  CHAT_HISTORY_HTML=""
  if [ -f "$CHAT_HISTORY_FILE" ]; then
    CHAT_HISTORY_HTML=$(awk -F'"text":"' '{
      split($2, rest, "\",\"ts\"")
      text = rest[1]
      role = ($0 ~ /"role":"user"/) ? "user" : "manager"
      printf "<div class=\"chat-msg chat-%s\">%s</div>\n", role, text
    }' "$CHAT_HISTORY_FILE")
  fi
  [ -z "$CHAT_HISTORY_HTML" ] && CHAT_HISTORY_HTML='<div class="chat-msg chat-manager">Ask me about my status, alerts, poll interval, ops budget, flapping services, or reboots.</div>'

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

  CYCLE_PCT=$(awk -v c="$CYCLE_GB" 'BEGIN{p=c/300*100; if(p>100)p=100; printf "%.1f", p}')

  cat > "$WWW/index.html.tmp" <<EOF
<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta http-equiv="Cache-Control" content="no-store, no-cache, must-revalidate, max-age=0">
<meta http-equiv="Pragma" content="no-cache">
<meta http-equiv="Expires" content="0">
<title>AOD Control Center</title>
<style>
  :root {
    --bg: #0a0d16;
    --sidebar-bg: #0b0e18;
    --surface: #12151f;
    --surface-2: #191d2b;
    --border: #232838;
    --text: #f3f4f6;
    --muted: #8b93a7;
    --purple: #7c6cf0;
    --purple-bg: rgba(124,108,240,0.14);
    --blue: #3b82f6;
    --blue-bg: rgba(59,130,246,0.14);
    --green: #22c55e;
    --green-bg: rgba(34,197,94,0.14);
    --orange: #f59e0b;
    --orange-bg: rgba(245,158,11,0.14);
    --red: #ef4444;
    --red-bg: rgba(239,68,68,0.14);
    --radius: 14px;
    --sidebar-w: 232px;
  }
  * { box-sizing: border-box; }
  body {
    font-family: -apple-system, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
    background: var(--bg);
    color: var(--text);
    margin: 0;
    -webkit-font-smoothing: antialiased;
  }
  a { color: inherit; text-decoration: none; }

  /* ---------- Sidebar ---------- */
  .sidebar {
    position: fixed; top: 0; left: 0; bottom: 0; width: var(--sidebar-w);
    background: var(--sidebar-bg); border-right: 1px solid var(--border);
    display: flex; flex-direction: column; padding: 22px 16px; overflow-y: auto;
  }
  .sidebar-head { display: flex; align-items: center; gap: 10px; padding: 0 6px 20px; border-bottom: 1px solid var(--border); margin-bottom: 16px; }
  .sidebar-logo { width: 34px; height: 34px; border-radius: 9px; background: var(--purple-bg); color: var(--purple); display: flex; align-items: center; justify-content: center; font-size: 1.2em; flex: none; }
  .sidebar-title { font-weight: 800; font-size: 1.05em; line-height: 1.1; }
  .sidebar-subtitle { font-size: 0.62em; color: var(--muted); letter-spacing: 0.08em; font-weight: 700; }
  .sidebar-nav { display: flex; flex-direction: column; gap: 2px; flex: 1; }
  .nav-item { display: flex; align-items: center; gap: 10px; padding: 10px 12px; border-radius: 10px; font-size: 0.88em; font-weight: 600; color: var(--muted); }
  .nav-item .nav-ic { width: 18px; text-align: center; flex: none; }
  .nav-item.active { background: var(--purple); color: #fff; }
  .sidebar-status { margin-top: 14px; padding: 14px; background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius); }
  .sidebar-status-title { font-size: 0.72em; color: var(--muted); font-weight: 700; text-transform: uppercase; letter-spacing: 0.04em; margin-bottom: 8px; }
  .sidebar-status-value { font-size: 0.85em; font-weight: 700; }
  .sidebar-status-age { font-size: 0.74em; color: var(--muted); margin-top: 4px; }

  /* ---------- Main content ---------- */
  .main { margin-left: var(--sidebar-w); padding: 26px 30px 60px; max-width: 1320px; }

  .topbar { display: flex; flex-wrap: wrap; align-items: flex-end; justify-content: space-between; gap: 16px; margin-bottom: 22px; }
  .clock { font-size: 2.6em; font-weight: 800; line-height: 1; letter-spacing: -0.5px; }
  .date { color: var(--muted); font-size: 0.9em; margin-top: 4px; }
  .pills { display: flex; flex-wrap: wrap; gap: 8px; }
  .pill {
    display: flex; align-items: center; gap: 6px; background: var(--surface); border: 1px solid var(--border);
    border-radius: 999px; padding: 7px 14px; font-size: 0.8em; font-weight: 600; white-space: nowrap;
  }
  .pill .dot { width: 7px; height: 7px; border-radius: 50%; background: var(--c, var(--muted)); flex-shrink: 0; }
  /* These label/value divs must stay in this EXACT adjacent
     div.label + div.value form (no wrapping span, no whitespace
     between the two divs) - RouterDreamService.java's STAT_PATTERN
     regex parses this exact HTML to populate the AOD across all 30
     layouts. Confirmed broken 2026-07-07 when a redesign first used
     span.lbl here instead - Internet/Signal rows and the health badge
     went stale across every layout. The value div CAN carry extra
     classes/attributes (the regex allows that) - only the label div
     must stay bare. */
  .pill .label { color: var(--muted); font-weight: 400; }
  .pill .value { font-weight: 700; }

  /* ---------- Generic card ---------- */
  .card { background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius); padding: 18px 20px; }
  .card-title { font-size: 0.78em; font-weight: 700; text-transform: uppercase; letter-spacing: 0.06em; color: var(--muted); display: flex; align-items: center; gap: 8px; margin-bottom: 14px; }
  .card-title .card-ic { color: var(--text); font-size: 1.1em; }
  .row2 { display: grid; grid-template-columns: 380px 1fr; gap: 18px; margin-bottom: 18px; align-items: start; }
  @media (max-width: 900px) { .row2 { grid-template-columns: 1fr; } }

  /* ---------- Phone-frame AOD preview card ---------- */
  .phone-card-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 12px; }
  .live-badge { background: var(--green-bg); color: var(--green); font-size: 0.68em; font-weight: 800; letter-spacing: 0.04em; padding: 3px 9px; border-radius: 999px; }
  .phone-frame { background: #06070c; border: 1px solid var(--border); border-radius: 24px; padding: 20px 18px; }
  .phone-time { font-size: 1.7em; font-weight: 700; text-align: center; }
  .phone-date { text-align: center; color: var(--muted); font-size: 0.72em; margin-top: 2px; }
  .phone-signal { text-align: center; color: var(--muted); font-size: 0.72em; margin: 10px 0 14px; letter-spacing: 0.02em; }
  .phone-signal .label, .phone-signal .value { display: inline; }
  .phone-signal .value { color: var(--muted); font-weight: 600; }
  .phone-rows { display: grid; grid-template-columns: 1fr 1fr; gap: 12px 20px; border-top: 1px solid var(--border); padding-top: 14px; }
  .phone-rows .label { font-size: 0.64em; color: var(--muted); letter-spacing: 0.05em; text-transform: uppercase; }
  .phone-rows .value { font-size: 1.02em; font-weight: 700; margin-top: 2px; display: block; }
  .phone-rows .value.good { color: var(--green); }
  .phone-theme { margin-top: 16px; padding-top: 14px; border-top: 1px solid var(--border); text-align: center; }
  .phone-theme .label { font-size: 0.64em; color: var(--muted); letter-spacing: 0.05em; text-transform: uppercase; }
  .phone-theme .value { color: var(--purple); font-weight: 700; margin-top: 3px; display: block; }
  .phone-card-foot { display: flex; align-items: center; justify-content: center; gap: 6px; color: var(--muted); font-size: 0.74em; margin-top: 12px; }

  /* ---------- Key metrics ---------- */
  .metrics-grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 14px; }
  @media (max-width: 900px) { .metrics-grid { grid-template-columns: repeat(2, 1fr); } }
  .metric-card { background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius); padding: 16px 16px 14px; }
  .metric-icon { width: 34px; height: 34px; border-radius: 9px; display: flex; align-items: center; justify-content: center; font-size: 1.1em; margin-bottom: 10px; }
  .metric-icon.blue { background: var(--blue-bg); color: var(--blue); }
  .metric-icon.green { background: var(--green-bg); color: var(--green); }
  .metric-icon.purple { background: var(--purple-bg); color: var(--purple); }
  .metric-icon.orange { background: var(--orange-bg); color: var(--orange); }
  .metric-cap { font-size: 0.66em; font-weight: 800; letter-spacing: 0.05em; text-transform: uppercase; margin-bottom: 6px; }
  .metric-cap.blue { color: var(--blue); }
  .metric-cap.green { color: var(--green); }
  .metric-cap.purple { color: var(--purple); }
  .metric-cap.orange { color: var(--orange); }
  .metric-card .label { display: none; }
  .metric-value { font-size: 1.55em; font-weight: 800; display: block; }
  .metric-sub { color: var(--muted); font-size: 0.76em; margin-top: 4px; }
  .metric-sub .label, .metric-sub .value, .metric-foot .label, .metric-foot .value { display: inline; font-weight: 400; }
  .metric-foot .value, .qa-status .value { margin-left: 4px; }
  .metric-bar-track { background: var(--surface-2); border-radius: 999px; height: 5px; margin-top: 10px; overflow: hidden; }
  .metric-bar-fill { height: 100%; border-radius: 999px; }
  .metric-bar-fill.blue { background: var(--blue); }
  .metric-bar-fill.green { background: var(--green); }
  .metric-foot { color: var(--muted); font-size: 0.72em; margin-top: 8px; }
  .metric-foot-link { color: var(--purple); font-size: 0.76em; font-weight: 700; margin-top: 8px; display: inline-block; }

  /* ---------- Quick actions (details/summary - no extra JS needed
     to expand/collapse the underlying form controls) ---------- */
  .qa-grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 14px; }
  @media (max-width: 900px) { .qa-grid { grid-template-columns: repeat(2, 1fr); } }
  .qa-card { background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius); padding: 4px; }
  .qa-card summary { list-style: none; cursor: pointer; padding: 14px 14px 12px; }
  .qa-card summary::-webkit-details-marker { display: none; }
  .qa-icon { width: 36px; height: 36px; border-radius: 10px; display: flex; align-items: center; justify-content: center; font-size: 1.15em; margin-bottom: 10px; }
  .qa-icon.purple { background: var(--purple-bg); color: var(--purple); }
  .qa-icon.blue { background: var(--blue-bg); color: var(--blue); }
  .qa-icon.green { background: var(--green-bg); color: var(--green); }
  .qa-icon.orange { background: var(--orange-bg); color: var(--orange); }
  .qa-label { font-weight: 700; font-size: 0.92em; }
  .qa-sub { color: var(--muted); font-size: 0.74em; margin-top: 3px; }
  .qa-card[open] { border-color: var(--purple); }
  .qa-body { padding: 0 14px 14px; border-top: 1px solid var(--border); padding-top: 12px; margin-top: -2px; }
  .qa-status { color: var(--muted); font-size: 0.78em; }
  .qa-status .label, .qa-status .value { display: inline; font-weight: 400; }
  .detail-item .label { font-size: 0.66em; color: var(--muted); text-transform: uppercase; letter-spacing: 0.05em; }
  .detail-item .value { font-size: 1.1em; font-weight: 700; display: block; margin-top: 2px; }
  .qa-result { margin-top: 8px; font-size: 0.85em; }

  table { border-collapse: collapse; width: 100%; }
  td, th { text-align: left; padding: 10px 12px; border-bottom: 1px solid var(--border); font-size: 0.85em; }
  th { color: var(--muted); font-weight: 700; font-size: 0.68em; text-transform: uppercase; letter-spacing: 0.05em; }
  tr:last-child td { border-bottom: none; }
  .status-pill { display: inline-block; padding: 4px 11px; border-radius: 999px; font-size: 0.78em; font-weight: 700; background: var(--green-bg); color: var(--green); }
  .btn-block { background: var(--red-bg); color: var(--red); border: none; border-radius: 8px; padding: 6px 12px; font-size: 0.78em; font-weight: 700; cursor: pointer; }
  .btn-allow { background: var(--green-bg); color: var(--green); border: none; border-radius: 8px; padding: 6px 12px; font-size: 0.78em; font-weight: 700; cursor: pointer; }

  input, select, textarea, button {
    font-family: inherit; font-size: 0.88em; border-radius: 9px; border: 1px solid var(--border);
    background: var(--surface-2); color: var(--text); padding: 9px 12px;
  }
  button { border: none; font-weight: 700; cursor: pointer; }
  .btn-primary { background: var(--purple); color: #fff; }
  .btn-neutral { background: var(--surface-2); color: var(--text); border: 1px solid var(--border); }
  .btn-danger { background: var(--red); color: #fff; }
  .btn-teal { background: var(--green); color: #08130c; }
  .form-row { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; margin-bottom: 10px; }
  .form-row:last-child { margin-bottom: 0; }
  .form-row select, .form-row input:not([type=number]) { flex: 1; min-width: 120px; }

  /* ---------- SMS & Alerts ---------- */
  .sms-tabs { display: flex; gap: 6px; flex-wrap: wrap; }
  .sms-tab { background: var(--surface-2); border: 1px solid var(--border); border-radius: 999px; padding: 6px 14px; font-size: 0.78em; font-weight: 700; cursor: pointer; color: var(--muted); }
  .sms-tab.active { background: var(--purple); color: #fff; border-color: var(--purple); }
  .sms-item { background: var(--surface-2); border-radius: 10px; padding: 10px 14px; margin-bottom: 6px; }
  .sms-item .meta { display: flex; justify-content: space-between; color: var(--muted); font-size: 0.78em; }
  .sms-item .body { margin-top: 4px; font-size: 0.88em; word-break: break-word; }
  .sender-title { margin: 14px 0 6px; color: var(--text); font-size: 0.9em; font-weight: 700; }

  /* ---------- Manager chat ---------- */
  .chat-window { background: #06070c; border: 1px solid var(--border); border-radius: var(--radius); padding: 14px; max-height: 320px; overflow-y: auto; margin-bottom: 12px; }
  .chat-msg { max-width: 78%; padding: 9px 13px; border-radius: 12px; margin-bottom: 8px; font-size: 0.86em; line-height: 1.4; }
  .chat-user { background: var(--purple); color: #fff; margin-left: auto; border-bottom-right-radius: 3px; }
  .chat-manager { background: var(--surface-2); color: var(--text); margin-right: auto; border-bottom-left-radius: 3px; }
  .chat-input-row { display: flex; gap: 8px; }
  .chat-input-row input { flex: 1; }

  .debug-card { border-color: #3a2020; }
  .debug-actions { display: flex; flex-wrap: wrap; gap: 8px; margin-bottom: 14px; }
  #console-cmd { width: 100%; font-family: monospace; resize: vertical; }
  #console-output { background: #06070c; border: 1px solid var(--border); border-radius: 10px; padding: 10px 14px; margin-top: 8px; max-height: 280px; overflow: auto; font-size: 0.8em; white-space: pre-wrap; word-break: break-word; display: none; }
  #aod-preview-img { width: 100%; border-radius: 12px; border: 1px solid var(--border); display: block; background: #06070c; }
  #aod-preview-fallback { display: none; color: var(--muted); font-size: 0.85em; padding: 20px; text-align: center; border: 1px dashed var(--border); border-radius: 12px; }
  .section { margin-bottom: 18px; }
</style>
</head>
<body>
<div class="sidebar">
  <div class="sidebar-head">
    <div class="sidebar-logo">&#128737;</div>
    <div>
      <div class="sidebar-title">AOD</div>
      <div class="sidebar-subtitle">CONTROL CENTER</div>
    </div>
  </div>
  <nav class="sidebar-nav">
    <a href="#top" class="nav-item active"><span class="nav-ic">&#8962;</span>Dashboard</a>
    <a href="#aod-preview-section" class="nav-item"><span class="nav-ic">&#128241;</span>AOD Preview</a>
    <a href="#data-usage-section" class="nav-item"><span class="nav-ic">&#128202;</span>Data Usage</a>
    <a href="#devices-section" class="nav-item"><span class="nav-ic">&#128421;</span>Devices</a>
    <a href="#sms-section" class="nav-item"><span class="nav-ic">&#9993;</span>SMS &amp; Alerts</a>
    <a href="#speedtest-section" class="nav-item"><span class="nav-ic">&#9201;</span>Speed Test</a>
    <a href="#billing-section" class="nav-item"><span class="nav-ic">&#128179;</span>Billing</a>
    <a href="#debug-section" class="nav-item"><span class="nav-ic">&#9881;</span>Settings</a>
  </nav>
  <div class="sidebar-status">
    <div class="sidebar-status-title">System Status</div>
    <div class="sidebar-status-value" style="color:${SYSTEM_STATUS_COLOR};">${SYSTEM_STATUS_LABEL}</div>
    <div class="sidebar-status-age">${SYSTEM_STATUS_AGE}</div>
  </div>
</div>

<div class="main" id="top">

<!-- live-data wraps everything that refreshes periodically via JS
     (see the poll loop near the bottom) - the Debug/Admin card and the
     Manager Chat card are deliberately OUTSIDE this wrapper so an
     in-progress console command, half-typed shell command, or a chat
     message being typed never gets silently wiped by a background
     refresh. -->
<div id="live-data">

<div class="topbar">
  <div>
    <div class="clock">${NOW_TIME}</div>
    <div class="date">${NOW_DATE}</div>
  </div>
  <div class="pills">
    <div class="pill"><span class="dot" style="--c:${HEALTH_COLOR}"></span><div class="label">Health</div><div class="value">${HEALTH_LABEL}</div></div>
    <div class="pill"><span class="dot" style="--c:${NET_COLOR}"></span><div class="label">Internet</div><div class="value">${NET_LABEL}</div></div>
    <div class="pill"><span class="dot" style="--c:${AOD_COLOR}"></span><span class="lbl">AOD</span> ${AOD_LABEL}</div>
    <div class="pill"><span class="dot" style="--c:${SIGNAL_COLOR}"></span><div class="label">Signal</div><div class="value">${SIGNAL_LABEL}</div></div>
  </div>
</div>

<div class="row2 section" id="aod-preview-section">
  <div class="card">
    <div class="phone-card-head">
      <div class="card-title" style="margin-bottom:0;">AOD Preview</div>
      <span class="live-badge">LIVE</span>
    </div>
    <div class="phone-frame">
      <div class="phone-time">${NOW_TIME}</div>
      <div class="phone-date">${NOW_DATE}</div>
      <div class="phone-signal">
        ${SIGNAL_LABEL} &middot; ${INTERNET_TYPE_LABEL} &middot;
        <div class="label">Down</div><div class="value" id="speed-down">${DOWN_MBPS}</div>/<div class="label">Up</div><div class="value" id="speed-up">${UP_MBPS}</div> Mbps
      </div>
      <div class="phone-rows">
        <div><div class="label">Signal</div><div class="value">${SIGNAL_LABEL}</div></div>
        <div><div class="label">Data Used</div><div class="value">${CYCLE_GB} GB</div></div>
        <div><div class="label">Internet</div><div class="value">${NET_LABEL}</div></div>
        <div><div class="label">Devices</div><div class="value">${CLIENT_COUNT}</div></div>
        <div><div class="label">SMS Fwd</div><div class="value good">${FWD_LABEL}</div></div>
        <div><div class="label">Total Used</div><div class="value">${TOTAL_GB} GB</div></div>
        <div><div class="label">Battery</div><div class="value">${BATT_LEVEL}%</div></div>
        <div><div class="label">Skin Temp</div><div class="value">${SKIN_TEMP}&deg;C</div></div>
      </div>
      <div class="phone-theme">
        <div class="label">Theme</div>
        <div class="value">${CURRENT_THEME_NAME}</div>
      </div>
    </div>
    <div class="phone-card-foot">Last updated <span id="aod-card-age">just now</span> &#8635;</div>
  </div>

  <div class="metrics-grid" id="data-usage-section">
    <div class="metric-card">
      <div class="metric-icon blue">&#128190;</div>
      <div class="metric-cap blue">Data Used</div>
      <div class="label">Total Used</div><div class="value metric-value">${TOTAL_GB} GB</div>
      <div class="metric-sub">${CYCLE_GB} GB this cycle</div>
      <div class="metric-bar-track"><div class="metric-bar-fill blue" style="width:${CYCLE_PCT}%;"></div></div>
      <div class="metric-foot">of 300 GB &middot; ${CYCLE_PCT}% used</div>
    </div>
    <div class="metric-card">
      <div class="metric-icon green">&#128267;</div>
      <div class="metric-cap green">Battery</div>
      <div class="label">Battery</div><div class="value metric-value">${BATT_LEVEL}%</div>
      <div class="metric-sub">Charging: ${BATT_CHARGING}</div>
      <div class="metric-bar-track"><div class="metric-bar-fill green" style="width:${BATT_LEVEL}%;"></div></div>
      <div class="metric-foot"><div class="label">Thermal</div><div class="value" style="color:${THERMAL_COLOR};">${THERMAL_LABEL}</div></div>
    </div>
    <div class="metric-card">
      <div class="metric-icon purple">&#128246;</div>
      <div class="metric-cap purple">Internet</div>
      <div class="value metric-value">${NET_LABEL}</div>
      <div class="metric-sub">Connection: <div class="label">Custom Message</div><div class="value">${CUSTOM_MESSAGE_ESC}</div></div>
      <div class="metric-foot">Status: <div class="label">Internet Type</div><div class="value">${INTERNET_TYPE_LABEL}</div></div>
    </div>
    <div class="metric-card">
      <div class="metric-icon orange">&#128241;</div>
      <div class="metric-cap orange">Devices</div>
      <div class="label">Devices</div><div class="value metric-value">${CLIENT_COUNT}</div>
      <div class="metric-sub">Connected</div>
      <a href="#devices-section" class="metric-foot-link">View all devices &rsaquo;</a>
    </div>
  </div>
</div>

<div class="card section">
  <div class="card-title">Quick Actions</div>
  <div class="qa-grid">
    <details class="qa-card" id="speedtest-section">
      <summary>
        <div class="qa-icon purple">&#9201;</div>
        <div class="qa-label">Run Speed Test</div>
        <div class="qa-sub">Check internet performance</div>
      </summary>
      <div class="qa-body">
        <button type="button" id="speedtest-run" class="btn-primary" style="width:100%;">Run Now</button>
        <span id="speedtest-status" class="qa-status"></span>
        <div id="speedtest-result" class="qa-result">${SPEEDTEST_SUMMARY}</div>
      </div>
    </details>

    <details class="qa-card">
      <summary>
        <div class="qa-icon blue">&#9635;</div>
        <div class="qa-label">Switch Layout</div>
        <div class="qa-sub">${CURRENT_THEME_NAME}</div>
      </summary>
      <div class="qa-body">
        <form method="POST" action="/cgi-bin/set_aod_layout.cgi" class="form-row">
          <select name="layout">
            ${AOD_LAYOUT_OPTIONS}
          </select>
          <button type="submit" class="btn-primary">Switch Layout</button>
        </form>
        <form method="POST" action="/cgi-bin/set_aod_theme.cgi" class="form-row">
          <select name="theme">
            ${AOD_THEME_OPTIONS}
          </select>
          <button type="submit" class="btn-primary" style="background:var(--purple);">Switch Color</button>
        </form>
      </div>
    </details>

    <details class="qa-card">
      <summary>
        <div class="qa-icon green">&#128172;</div>
        <div class="qa-label">Show on AOD</div>
        <div class="qa-sub">Send custom message to display</div>
      </summary>
      <div class="qa-body">
        <form method="POST" action="/cgi-bin/message.cgi">
          <input type="text" name="message" maxlength="200" placeholder="Send a message home..." style="width:100%; margin-bottom:8px;">
          <div class="form-row">
            <input type="number" name="duration_min" value="5" min="1" max="1440" style="width:80px; flex:none;">
            <span class="qa-status">minutes</span>
            <button type="submit" class="btn-primary" style="margin-left:auto;">Show on AOD</button>
          </div>
        </form>
      </div>
    </details>

    <details class="qa-card" id="billing-section">
      <summary>
        <div class="qa-icon orange">&#128197;</div>
        <div class="qa-label">Set Billing Day</div>
        <div class="qa-sub">Current day: ${BILLING_DAY}</div>
      </summary>
      <div class="qa-body">
        <form method="POST" action="/cgi-bin/set_billing_day.cgi" class="form-row">
          <select name="day">
            ${BILLING_DAY_OPTIONS}
          </select>
          <button type="submit" class="btn-teal">Set</button>
        </form>
        <div class="qa-status" style="margin-top:8px;"><div class="label">Cellular Cycle</div><div class="value" style="color:${QUOTA_COLOR};">${QUOTA_LABEL}</div></div>
        <div class="qa-status" style="margin-top:4px;"><div class="label">WG Tunnel</div><div class="value" style="color:${TUNNEL_COLOR};">${TUNNEL_LABEL}</div></div>
      </div>
    </details>
  </div>
</div>

<div class="card section">
  <div class="card-title">Device &amp; System Details</div>
  <div class="qa-grid" style="grid-template-columns: repeat(4, 1fr);">
    <div class="detail-item"><div class="label">CPU MHz</div><div class="value">${CPU_MHZ}</div></div>
    <div class="detail-item"><div class="label">GPU MHz</div><div class="value">${GPU_MHZ}</div></div>
    <div class="detail-item"><div class="label">AKD</div><div class="value">${AKD_BADGE_TEXT}</div></div>
    <div class="detail-item"><div class="label">Real AOD screenshot</div>
      <img id="aod-preview-img" src="/aod_preview.png" style="margin-top:6px; max-height:80px; width:auto; border-radius:8px;"
        onerror="this.style.display='none'; document.getElementById('aod-preview-fallback').style.display='block';">
      <div id="aod-preview-fallback" style="font-size:0.72em;">Not captured yet</div>
    </div>
  </div>
</div>

<div class="card section" id="devices-section">
  <div class="card-title">Connected Devices</div>
  <table>
  <tr><th>Device</th><th>IP Address</th><th>MAC Address</th><th>Band</th><th>Usage</th><th>Status</th><th>Actions</th></tr>
  ${ROWS}
  </table>
</div>

<div class="card section" id="sms-section">
  <div class="card-title" style="display:flex; justify-content:space-between; align-items:center;">
    <span>Recent SMS &amp; Alerts</span>
    <div class="sms-tabs" id="sms-tabs">
      <span class="sms-tab active" data-cat="all">All</span>
      <span class="sms-tab" data-cat="jio">Jio</span>
      <span class="sms-tab" data-cat="alerts">Alerts</span>
      <span class="sms-tab" data-cat="otp">OTP</span>
      <span class="sms-tab" data-cat="data">Data Usage</span>
    </div>
  </div>
  ${SMS_ROWS}
</div>

</div>
<!-- /live-data -->

<div class="card section" id="manager-chat-section">
  <div class="card-title">Chat with the Manager</div>
  <div class="chat-window" id="chat-window">
    ${CHAT_HISTORY_HTML}
  </div>
  <div class="chat-input-row">
    <input type="text" id="chat-input" placeholder="Ask about status, alerts, poll interval, ops budget...">
    <button type="button" id="chat-send" class="btn-primary">Send</button>
  </div>
  <div style="color:var(--muted); font-size:0.72em; margin-top:8px;">hotspot_manager.sh is rule-based, not a language model - it answers from its own real state files, not general conversation.</div>
</div>

<div class="card debug-card section" id="debug-section">
  <div class="card-title">Debug / Admin</div>
  <div class="debug-actions">
    <form method="POST" action="/cgi-bin/debug_action.cgi" onsubmit="return confirm('Reboot the router now? This will drop the hotspot for ~1-2 minutes.');">
      <input type="hidden" name="action" value="reboot">
      <button type="submit" class="btn-danger">Reboot Device</button>
    </form>
    <form method="POST" action="/cgi-bin/debug_action.cgi">
      <input type="hidden" name="action" value="restart_aod">
      <button type="submit" class="btn-neutral">Restart AOD</button>
    </form>
    <form method="POST" action="/cgi-bin/debug_action.cgi">
      <input type="hidden" name="action" value="restart_dashboard">
      <button type="submit" class="btn-neutral">Restart Dashboard</button>
    </form>
    <form method="POST" action="/cgi-bin/debug_action.cgi">
      <input type="hidden" name="action" value="restart_failover">
      <button type="submit" class="btn-neutral">Restart Failover</button>
    </form>
    <form method="POST" action="/cgi-bin/debug_action.cgi" onsubmit="return confirm('Restart the hotspot? Connected devices will briefly drop.');">
      <input type="hidden" name="action" value="restart_hotspot">
      <button type="submit" class="btn-neutral">Restart Hotspot</button>
    </form>
    <form method="POST" action="/cgi-bin/pc_command.cgi">
      <input type="hidden" name="action" value="restart_rustdesk">
      <button type="submit" class="btn-neutral">Restart RustDesk (PC)</button>
    </form>
    <form method="POST" action="/cgi-bin/pc_command.cgi" onsubmit="return confirm('Restart the PC? It queues a reboot with a 60s cancelable grace period if someone is at the keyboard - picked up next time pc_command_watcher.py polls (within a minute or two).');">
      <input type="hidden" name="action" value="restart_pc">
      <button type="submit" class="btn-danger">Restart PC</button>
    </form>
  </div>
  <textarea id="console-cmd" placeholder="shell command, runs as root..." rows="2"></textarea>
  <div style="display:flex; gap:8px; margin-top:6px; align-items:center;">
    <button type="button" id="console-run" class="btn-primary">Run</button>
    <span id="console-status" style="color:var(--muted); font-size:0.8em;"></span>
  </div>
  <pre id="console-output"></pre>
</div>

</div>
<script>
// Refreshes all the data cards in place instead of a full page reload -
// fetches the freshly-regenerated page, then swaps in only the
// #live-data container's contents. The Debug/Admin card, Manager Chat
// card (console/chat inputs) live outside that container by design, so
// a background refresh can never wipe something being typed.
setInterval(function() {
  fetch('/index.html?t=' + Date.now(), {cache: 'no-store'})
    .then(function(r) { return r.text(); })
    .then(function(html) {
      var fresh = new DOMParser().parseFromString(html, 'text/html').getElementById('live-data');
      var current = document.getElementById('live-data');
      if (fresh && current) {
        current.innerHTML = fresh.innerHTML;
      }
    })
    .catch(function() {});
}, 10000);

// Polls speed.json (written every 2s by speed_monitor.sh) so Down/Up
// feels live without waiting for the slower page-regeneration cycle.
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
// whatever it prints in the <pre> below.
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

// Manager chat: appends the user's message immediately, then the
// manager's reply once manager_chat.cgi responds. Outside #live-data,
// so this doesn't need any special delegation handling - its own DOM
// node is never replaced by the periodic refresh.
document.getElementById('chat-send').addEventListener('click', function() {
  var input = document.getElementById('chat-input');
  var win = document.getElementById('chat-window');
  var msg = input.value.trim();
  if (!msg) return;
  var userDiv = document.createElement('div');
  userDiv.className = 'chat-msg chat-user';
  userDiv.textContent = msg;
  win.appendChild(userDiv);
  win.scrollTop = win.scrollHeight;
  input.value = '';
  fetch('/cgi-bin/manager_chat.cgi', {
    method: 'POST',
    headers: {'Content-Type': 'application/x-www-form-urlencoded'},
    body: 'message=' + encodeURIComponent(msg)
  })
    .then(function(r) { return r.json(); })
    .then(function(d) {
      var mgrDiv = document.createElement('div');
      mgrDiv.className = 'chat-msg chat-manager';
      mgrDiv.textContent = d.reply || '(no reply)';
      win.appendChild(mgrDiv);
      win.scrollTop = win.scrollHeight;
    })
    .catch(function(e) {
      var errDiv = document.createElement('div');
      errDiv.className = 'chat-msg chat-manager';
      errDiv.textContent = 'Request failed: ' + e;
      win.appendChild(errDiv);
    });
});
document.getElementById('chat-input').addEventListener('keydown', function(e) {
  if (e.key === 'Enter') document.getElementById('chat-send').click();
});
// Scroll chat to bottom on initial load (history rendered server-side).
document.getElementById('chat-window').scrollTop = document.getElementById('chat-window').scrollHeight;

// Speed test: runs on the router itself against Cloudflare's public
// test endpoints. Delegated on document rather than bound directly to
// the button - the button lives inside #live-data, which the periodic
// refresh above replaces wholesale every 10s (fresh DOM nodes, no
// listeners attached). A direct addEventListener would silently stop
// working after the first background refresh.
document.addEventListener('click', function(e) {
  if (e.target.id !== 'speedtest-run') return;
  var statusEl = document.getElementById('speedtest-status');
  var resultEl = document.getElementById('speedtest-result');
  var btn = e.target;
  btn.disabled = true;
  statusEl.textContent = 'testing... (~10s)';
  fetch('/cgi-bin/speedtest.cgi', {method: 'POST'})
    .then(function(r) { return r.text(); })
    .then(function(t) {
      statusEl.textContent = '';
      resultEl.textContent = t || 'No result';
      btn.disabled = false;
    })
    .catch(function(e) {
      statusEl.textContent = '';
      resultEl.textContent = 'Request failed: ' + e;
      btn.disabled = false;
    });
});

// SMS filter tabs - also delegated, same reasoning as speed test above.
document.addEventListener('click', function(e) {
  if (!e.target.classList || !e.target.classList.contains('sms-tab')) return;
  var cat = e.target.getAttribute('data-cat');
  var tabsContainer = e.target.closest('#sms-tabs');
  if (tabsContainer) {
    tabsContainer.querySelectorAll('.sms-tab').forEach(function(t) { t.classList.remove('active'); });
  }
  e.target.classList.add('active');
  document.querySelectorAll('.sms-item').forEach(function(item) {
    item.style.display = (cat === 'all' || item.getAttribute('data-category') === cat) ? '' : 'none';
  });
});

// AOD preview screenshot: cache-bust every ~5s.
setInterval(function() {
  var img = document.getElementById('aod-preview-img');
  if (img) img.src = '/aod_preview.png?t=' + Date.now();
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

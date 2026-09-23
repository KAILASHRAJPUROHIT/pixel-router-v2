#!/system/bin/sh
# Shared WAN-interface detection and byte-counting helpers - extracted
# 2026-07-07 after this exact logic had been copy-pasted into three
# separate long-running scripts (update.sh, speed_monitor.sh, and
# cellular_quota_monitor.sh), each with its own "kept in sync manually"
# comment. All three had stayed byte-for-byte identical so far, but
# that was luck, not a guarantee - cellular_quota_monitor.sh's copy was
# only a few hours old when this was extracted. Source this file
# (`. /data/adb/dashboard/lib_net_common.sh`) instead of redefining
# these locally.

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
# interface being renumbered across reconnects. Cellular-only by
# design (never wlan0/wlan2), so borrowed-hotspot browsing never
# counts against anything tracking cellular-specific usage.
sum_all_rmnet_bytes() {
  awk '
    $1 ~ /^rmnet[0-9]+:/ { rx+=$2; tx+=$10 }
    END { print rx+tx }' /proc/net/dev
}

#!/system/bin/sh
# Switches the AOD over to Google Photos' own built-in screensaver
# (already installed on this Pixel: com.google.android.apps.photos/
# .daydream.PhotosDreamService - configure which album/source it pulls
# from inside the Photos app itself, Settings > Photos screensaver)
# whenever the router is genuinely idle: zero connected hotspot clients
# AND negligible cellular data movement over the last check interval.
# Switches back to our own stats AOD (com.router.aod/.RouterDreamService)
# the moment either condition is no longer true.
#
# Always forces our own AOD during 10pm-6am regardless of idle state -
# keeps night behavior simple/predictable (dim stats, never a bright
# slideshow) rather than layering photo-display logic on top of the
# separate day/night brightness handling already in RouterDreamService.

LOG=/data/adb/dashboard/idle_photo_switcher.log
CHECK_INTERVAL=120
IDLE_THRESHOLD_BYTES=51200
NIGHT_START_HOUR=22
NIGHT_END_HOUR=6

OUR_DREAM="com.router.aod/.RouterDreamService"
PHOTOS_DREAM="com.google.android.apps.photos/.daydream.PhotosDreamService"

log() {
  echo "$(date '+%Y-%m-%d %H:%M:%S') - $1" >> "$LOG"
}

detect_ap_if() {
  if ip addr show ap_br_wlan2 >/dev/null 2>&1; then
    echo ap_br_wlan2
  else
    echo wlan2
  fi
}

get_total_cellular_bytes() {
  awk '$1 ~ /^rmnet[0-9]+:/ { rx+=$2; tx+=$10 } END{print rx+tx+0}' /proc/net/dev
}

get_client_count() {
  AP_IF=$(detect_ap_if)
  ip neigh show dev "$AP_IF" 2>/dev/null | grep "lladdr" | awk '{print $3}' | sort -u | grep -c .
}

while [ "$(getprop sys.boot_completed)" != "1" ]; do
  sleep 2
done
sleep 30

LAST_BYTES=$(get_total_cellular_bytes)

while true; do
  sleep "$CHECK_INTERVAL"

  HOUR=$(date +%H)
  HOUR=$((10#$HOUR))
  NIGHT=0
  if [ "$HOUR" -ge "$NIGHT_START_HOUR" ] || [ "$HOUR" -lt "$NIGHT_END_HOUR" ]; then
    NIGHT=1
  fi

  CLIENT_COUNT=$(get_client_count)
  CUR_BYTES=$(get_total_cellular_bytes)
  DELTA=$(( CUR_BYTES - LAST_BYTES ))
  [ "$DELTA" -lt 0 ] && DELTA=0
  LAST_BYTES=$CUR_BYTES

  CURRENT=$(settings get secure screensaver_components 2>/dev/null)

  if [ "$NIGHT" = "1" ]; then
    if [ "$CURRENT" != "$OUR_DREAM" ]; then
      settings put secure screensaver_components "$OUR_DREAM"
      log "Night hours ($HOUR:00) - forcing Router AOD (was: $CURRENT)"
    fi
  elif [ "$CLIENT_COUNT" = "0" ] && [ "$DELTA" -lt "$IDLE_THRESHOLD_BYTES" ]; then
    if [ "$CURRENT" != "$PHOTOS_DREAM" ]; then
      settings put secure screensaver_components "$PHOTOS_DREAM"
      log "Idle (0 clients, ${DELTA}B/interval) - switched to Photos slideshow"
    fi
  else
    if [ "$CURRENT" != "$OUR_DREAM" ]; then
      settings put secure screensaver_components "$OUR_DREAM"
      log "Activity ($CLIENT_COUNT clients, ${DELTA}B/interval) - switched back to Router AOD"
    fi
  fi
done

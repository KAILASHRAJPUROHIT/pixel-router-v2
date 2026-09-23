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

switch_dream() {
  # $1 = target component. Changing screensaver_components alone does
  # NOT restart whichever dream is already running - DreamManagerService
  # only re-reads it the next time a dream is triggered, so the old one
  # just keeps going indefinitely (confirmed: Photos kept showing well
  # past the 22:00 night cutoff, dumpsys dreams still showed
  # mCurrentDream=Photos even though the setting already said Router
  # AOD). Forcing a wake now - the same trick aod_watchdog.sh already
  # uses when Doze/another dream has taken over - ends the current
  # dream immediately; the existing 15s screen timeout then re-triggers
  # dreaming with the now-updated component, so the switch actually
  # takes effect right away instead of whenever the old dream happens
  # to end on its own.
  settings put secure screensaver_components "$1"
  input keyevent KEYCODE_WAKEUP
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
      switch_dream "$OUR_DREAM"
      log "Night hours ($HOUR:00) - forcing Router AOD (was: $CURRENT)"
    fi
  elif [ "$CLIENT_COUNT" = "0" ] && [ "$DELTA" -lt "$IDLE_THRESHOLD_BYTES" ]; then
    if [ "$CURRENT" != "$PHOTOS_DREAM" ]; then
      switch_dream "$PHOTOS_DREAM"
      log "Idle (0 clients, ${DELTA}B/interval) - switched to Photos slideshow"
    fi
  else
    if [ "$CURRENT" != "$OUR_DREAM" ]; then
      switch_dream "$OUR_DREAM"
      log "Activity ($CLIENT_COUNT clients, ${DELTA}B/interval) - switched back to Router AOD"
    fi
  fi
done

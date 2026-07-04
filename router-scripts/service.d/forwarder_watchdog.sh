#!/system/bin/sh
# Hardened watchdog for the SMS/call forwarding chain. Checks every 3
# minutes: app enabled state, SMS permissions, and Doze whitelist
# membership - re-applying any that have drifted or been revoked.

PKG=cn.ppps.forwarder
LOG=/data/adb/dashboard/forwarder_watchdog.log

log() {
  echo "$(date '+%Y-%m-%d %H:%M:%S') - $1" >> "$LOG"
}

while true; do
  # 1. Package enabled state
  STATE=$(pm dump "$PKG" 2>/dev/null | grep -o "enabled=[01]" | head -1)
  if [ "$STATE" = "enabled=0" ]; then
    pm enable "$PKG"
    log "app was disabled - re-enabled"
  fi

  # 2. SMS permissions
  for PERM in RECEIVE_SMS READ_SMS SEND_SMS; do
    GRANTED=$(dumpsys package "$PKG" 2>/dev/null | grep -A1 "android.permission.$PERM:" | grep -o "granted=true")
    if [ -z "$GRANTED" ]; then
      pm grant "$PKG" "android.permission.$PERM"
      log "permission $PERM was revoked - re-granted"
    fi
  done

  # 3. Doze whitelist membership
  ON_WHITELIST=$(dumpsys deviceidle whitelist 2>/dev/null | grep "$PKG")
  if [ -z "$ON_WHITELIST" ]; then
    dumpsys deviceidle whitelist "+$PKG"
    log "dropped from Doze whitelist - re-added"
  fi

  sleep 180
done

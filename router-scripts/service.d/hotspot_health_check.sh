#!/system/bin/sh
# Catches the two failure modes that combined into a real full lockout
# on 2026-07-08: the laptop and TV are locked to 5GHz-only (denied on
# 2.4GHz via hostapd's ACL, see hotspot_on_boot.sh), so if the 5GHz
# radio (wlan3) isn't actually up for any reason, they have nowhere to
# connect at all - and separately, the hotspot's real password can get
# silently replaced with the CHANGEME_PASSWORD placeholder whenever
# `cmd wifi start-softap` runs while a session is already active
# (confirmed live: it silently no-ops and keeps whatever was already
# running instead of applying the new args - a second start-softap
# call without stop-softap first hit this exact gotcha and locked
# EVERYONE out, not just the 5GHz-only devices, since the broadcast
# password itself was wrong).
#
# Both are now checked here on a real interval rather than only at
# boot, since either a manual restart (the dashboard's own "Restart
# Hotspot" button, hotspot_watchdog.sh's own AP-missing recovery, or a
# person on adb) can retrigger them at any time, not just at boot.
LOG=/data/adb/dashboard/hotspot_health_check.log
CHECK_INTERVAL=60
DENY_2GHZ_FILE=/data/vendor/wifi/hostapd/deny_2ghz.txt
WLAN2_CONF=/data/vendor/wifi/hostapd/hostapd_wlan2.conf
WLAN3_CONF=/data/vendor/wifi/hostapd/hostapd_wlan3.conf
# Same source of truth as hotspot_on_boot.sh - see that script for why
# this can't be a hardcoded/fallback value (that exact pattern is what
# caused the 2026-07-08 lockout this whole script exists to catch).
SECRETS=/data/adb/dashboard/hotspot_secrets.properties

log() {
  echo "$(date '+%Y-%m-%d %H:%M:%S') - $1" >> "$LOG"
}

apply_pmf_and_acl() {
  for f in "$WLAN2_CONF" "$WLAN3_CONF"; do
    [ -f "$f" ] && sed -i "s/ieee80211w=1/ieee80211w=0/" "$f"
  done
  if [ -f "$WLAN2_CONF" ] && ! grep -q macaddr_acl "$WLAN2_CONF"; then
    printf '\nmacaddr_acl=1\ndeny_mac_file=%s\n' "$DENY_2GHZ_FILE" >> "$WLAN2_CONF"
  fi
  HP=$(pgrep -x hostapd)
  [ -n "$HP" ] && kill -HUP "$HP"
}

while [ "$(getprop sys.boot_completed)" != "1" ]; do
  sleep 2
done
sleep 60

while true; do
  ISSUE=0

  # --- Wrong/placeholder broadcast password ---
  if [ -f "$WLAN2_CONF" ] && grep -q "wpa_passphrase=CHANGEME_PASSWORD" "$WLAN2_CONF"; then
    log "hotspot password is the CHANGEME_PASSWORD placeholder, not the real one - every device is locked out right now, fixing"
    ISSUE=1
  fi

  # --- 5GHz-locked devices with no 5GHz radio actually up ---
  # Only a real problem if the deny list is non-empty (i.e. some device
  # actually depends on 5GHz existing) and the bridge interface exists
  # at all (nothing to check if the AP is fully down - hotspot_watchdog.sh
  # already owns that case).
  if [ -s "$DENY_2GHZ_FILE" ] && ip addr show ap_br_wlan2 >/dev/null 2>&1; then
    HAS_5GHZ=$(dumpsys wifi 2>/dev/null | grep -m1 mCurrentSoftApInfoMap | grep -c "wlan3=")
    if [ "$HAS_5GHZ" = "0" ]; then
      log "devices are 5GHz-locked (deny_2ghz.txt non-empty) but wlan3 isn't up - they have nowhere to connect, fixing"
      ISSUE=1
    fi
  fi

  if [ "$ISSUE" = "1" ]; then
    SSID=$(sed -n 's/^HOTSPOT_SSID=//p' "$SECRETS" 2>/dev/null)
    REAL_PASS=$(sed -n 's/^HOTSPOT_PASSWORD=//p' "$SECRETS" 2>/dev/null)
    if [ -z "$SSID" ] || [ -z "$REAL_PASS" ]; then
      log "hotspot_secrets.properties missing/incomplete - cannot safely fix, skipping this cycle"
    else
      cmd wifi stop-softap >/dev/null 2>&1
      sleep 2
      cmd wifi start-softap "$SSID" wpa2 "$REAL_PASS" -b bridged_2_5 >/dev/null 2>&1
      sleep 5
      apply_pmf_and_acl
      log "corrective restart applied (stop+start bridged_2_5, PMF+ACL reapplied)"
    fi
  fi

  sleep "$CHECK_INTERVAL"
done

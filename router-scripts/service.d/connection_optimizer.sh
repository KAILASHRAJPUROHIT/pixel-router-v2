#!/system/bin/sh
# Runs every 4 hours. Re-verifies the settings that keep the router
# healthy (conntrack size, DNS) in case anything drifted, checks
# hotspot is actually up, and only forces a cellular re-scan if signal
# quality is genuinely poor - never a blind/disruptive reset on a timer.

LOG=/data/adb/dashboard/connection_optimizer.log
RSRP_THRESHOLD=-108
SINR_THRESHOLD=5

log() {
  echo "$(date '+%Y-%m-%d %H:%M:%S') - $1" >> "$LOG"
}

check_and_fix_conntrack() {
  CUR=$(cat /proc/sys/net/netfilter/nf_conntrack_max 2>/dev/null)
  if [ "$CUR" != "65536" ]; then
    sysctl -w net.netfilter.nf_conntrack_max=65536 >/dev/null 2>&1
    log "conntrack_max had drifted to $CUR - reset to 65536"
  fi
}

check_and_fix_dns() {
  MODE=$(settings get global private_dns_mode 2>/dev/null)
  SPEC=$(settings get global private_dns_specifier 2>/dev/null)
  if [ "$MODE" != "hostname" ] || [ "$SPEC" != "1dot1dot1dot1.cloudflare-dns.com" ]; then
    settings put global private_dns_mode hostname
    settings put global private_dns_specifier "1dot1dot1dot1.cloudflare-dns.com"
    log "Private DNS had drifted (mode=$MODE spec=$SPEC) - reset to Cloudflare"
  fi
}

check_and_fix_softap_timeout() {
  # Android auto-shuts-off the hotspot after 10 min with no connected
  # clients unless this is disabled - found and fixed 2026-07-04, but
  # re-assert it here defensively in case it ever reverts.
  CUR=$(settings get global soft_ap_timeout_enabled 2>/dev/null)
  if [ "$CUR" != "0" ]; then
    settings put global soft_ap_timeout_enabled 0
    log "soft_ap_timeout_enabled had drifted to '$CUR' - reset to 0"
  fi
}

check_hotspot() {
  if ! ip addr show wlan2 2>/dev/null | grep -q "inet " && ! ip addr show ap_br_wlan2 2>/dev/null | grep -q "inet "; then
    log "Hotspot appears down - restarting"
    # Read the real SSID/password dynamically (same approach as
    # hotspot_on_boot.sh) - this previously hardcoded a stale default
    # SSID/password, which would silently revert the hotspot's real
    # configured name if this ever fired.
    CFG=/data/misc_ce/0/apexdata/com.android.wifi/WifiConfigStoreSoftAp.xml
    SSID=$(cat "$CFG" 2>/dev/null | grep "WifiSsid" | sed -E 's/.*>&quot;(.*)&quot;<.*/\1/')
    PASS=$(cat "$CFG" 2>/dev/null | grep "Passphrase" | sed -E 's/.*>([^<]*)<.*/\1/')
    # NOTE (repo copy): real fallback SSID redacted - on-device version
    # has your actual SSID; this path is a rarely-hit last resort.
    [ -z "$SSID" ] && SSID="CHANGEME_SSID"
    if [ -n "$PASS" ]; then
      # stop-softap first - confirmed 2026-07-06 that start-softap can
      # silently no-op on an already-running session instead of
      # applying the new SSID/password.
      cmd wifi stop-softap >/dev/null 2>&1
      sleep 2
      cmd wifi start-softap "$SSID" wpa2 "$PASS" >/dev/null 2>&1
    else
      log "Could not read real SSID/password from WifiConfigStoreSoftAp.xml - not restarting with a guessed fallback"
    fi
  fi
}

check_signal_and_reselect_if_poor() {
  DUMP=$(dumpsys telephony.registry 2>/dev/null)
  # This SIM is LTE-only (Jio 4G) - the real signal fields live under
  # mLte=CellSignalStrengthLte inside the mSignalStrength= line, not
  # "ssRsrp"/"ssSinr" (those are 5G-NR-specific and always report the
  # 2147483647 "not measured" sentinel on an LTE-only connection - this
  # was silently making every check below into a no-op previously,
  # confirmed via raw dumpsys telephony.registry output). LTE's
  # SINR-equivalent metric is called "rssnr", not "sinr".
  SIG_LINE=$(echo "$DUMP" | grep -m1 "mSignalStrength=")
  RSRP=$(echo "$SIG_LINE" | grep -oE "mLte=CellSignalStrengthLte:[^,]*rsrp=-?[0-9]+" | grep -oE 'rsrp=-?[0-9]+' | cut -d= -f2)
  SINR=$(echo "$SIG_LINE" | grep -oE "rssnr=-?[0-9]+" | head -1 | cut -d= -f2)

  if [ -z "$RSRP" ] || [ -z "$SINR" ]; then
    log "No valid signal reading available yet (sentinel/unknown) - skipping this check"
  elif [ -n "$RSRP" ] && [ -n "$SINR" ]; then
    if [ "$RSRP" -lt "$RSRP_THRESHOLD" ] || [ "$SINR" -lt "$SINR_THRESHOLD" ]; then
      log "Signal poor (RSRP=$RSRP SINR=$SINR) - forcing cellular re-scan via airplane mode toggle"
      settings put global airplane_mode_on 1
      sleep 3
      settings put global airplane_mode_on 0
      sleep 10
      NEW_SIG_LINE=$(dumpsys telephony.registry 2>/dev/null | grep -m1 "mSignalStrength=")
      NEW_RSRP=$(echo "$NEW_SIG_LINE" | grep -oE "mLte=CellSignalStrengthLte:[^,]*rsrp=-?[0-9]+" | grep -oE 'rsrp=-?[0-9]+' | cut -d= -f2)
      log "Re-scan complete. New RSRP=$NEW_RSRP"
    else
      log "Signal OK (RSRP=$RSRP SINR=$SINR) - no action"
    fi
  fi
}

while true; do
  log "--- optimization pass starting ---"
  check_and_fix_conntrack
  check_and_fix_dns
  check_and_fix_softap_timeout
  check_hotspot
  check_signal_and_reselect_if_poor
  log "--- optimization pass complete ---"
  sleep 14400
done

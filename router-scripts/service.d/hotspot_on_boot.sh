#!/system/bin/sh
# Auto-enable the Wi-Fi hotspot after boot so a reboot/power blip
# doesn't require manually re-enabling it via the router phone's screen.
#
# Reads SSID/password dynamically from Android's own persisted SoftAp
# config rather than hardcoding them - so if the name/password is ever
# changed via Settings, this script respects that going forward instead
# of silently reverting it back to whatever was set at build time.

# Wait for the system to fully finish booting.
while [ "$(getprop sys.boot_completed)" != "1" ]; do
  sleep 2
done

# Airtel's carrier config on this SIM sets
# isCarrierConfigAffirmsEntitlementCheckRequired=true (confirmed via
# dumpsys tethering 2026-07-05), meaning Android's official Tethering
# framework is wired to show a carrier provisioning UI when hotspot is
# started via TetheringManager. We never use that path (we call
# `cmd wifi start-softap` directly below, which bypasses it structurally),
# but this is a belt-and-suspenders block in case anything - a stray tap
# on the phone's own Settings > Hotspot toggle - ever goes through the
# official path instead.
resetprop net.tethering.noprovisioning true
pm disable com.google.android.tetheringentitlement >/dev/null 2>&1

# Extra delay to let the Wi-Fi/tethering services fully initialize.
sleep 15

CFG="/data/misc_ce/0/apexdata/com.android.wifi/WifiConfigStoreSoftAp.xml"
SSID=$(cat "$CFG" 2>/dev/null | grep "WifiSsid" | sed -E 's/.*>&quot;(.*)&quot;<.*/\1/')
PASS=$(cat "$CFG" 2>/dev/null | grep "Passphrase" | sed -E 's/.*>([^<]*)<.*/\1/')

# Fallback if the config file is missing/unreadable - confirmed
# 2026-07-06 that WifiConfigStoreSoftAp.xml no longer exists at this
# path on this Android build at all (the OS update appears to have
# restructured Wi-Fi config storage - the merged WifiConfigStore.xml
# that replaced it doesn't expose SoftAp SSID/passphrase in a simple
# greppable plaintext form). Since dynamic reading is now unreliable
# across OS updates, the fallback holds the real persistent values
# directly rather than a stale/wrong placeholder - this had been
# "Pixel_5053" (the stock default) here, silently reverting the actual
# configured hotspot name (PILLYBOO) whenever the dynamic read failed.
# NOTE (repo copy): real password redacted here - the on-device version
# has your actual password.
[ -z "$SSID" ] && SSID="PILLYBOO"
[ -z "$PASS" ] && PASS="CHANGEME_PASSWORD"

# Confirmed 2026-07-06: start-softap can silently no-op (keeps whatever
# config was already active) if a softap session is already running -
# stop-softap first is a harmless no-op if nothing's running, but makes
# sure a fresh start actually applies $SSID/$PASS either way.
#
# -b bridged_2_5 added 2026-07-07: without a -b flag, start-softap's own
# help text says 2.4GHz is the preferred band - confirmed live that this
# was exactly why the hotspot had been running 2.4GHz-only the whole
# time despite the persisted config supporting bridged dual-band.
# 6GHz was tested too (bridged_5_6) and fails outright - "SAP is
# disabled", no fallback - this hardware's driver reports zero 6GHz
# channels for AP mode on this SIM's country code (IN), confirmed via
# `cmd wifi get-softap-supported-features`/dumpsys capability output,
# not just a missing config option.
#
# Note: even with bridged_2_5 requested, the second (5GHz) radio leg
# comes up reactively - only once a client actually negotiates it, not
# immediately at boot - and there's no root/shell-exposed switch found
# to force it eager (BridgedModeInstanceOpportunisticEnabled isn't in
# the persisted XML and has no `cmd wifi` toggle). This is still a real
# improvement over the previous always-2.4-only default though, since
# it makes 5GHz reachable at all instead of never requested.
cmd wifi stop-softap >/dev/null 2>&1
sleep 2
cmd wifi start-softap "$SSID" wpa2 "$PASS" -b bridged_2_5

# Confirmed 2026-07-06: Android regenerates hostapd_wlan2.conf on every
# start-softap with ieee80211w=1 (PMF/802.11w set to "optional") - this
# combination (PMF-optional alongside plain WPA2, not WPA3) broke a
# laptop that had connected fine the day before (a real client
# WiFi-driver incompatibility, not a password/config error - verified
# via the laptop failing with the exact correct password, then
# connecting successfully the instant PMF was disabled). There's no
# `cmd wifi`/Settings toggle for this, so the fix is: wait for hostapd
# to start, patch its live config file, then send SIGHUP to make it
# reload without dropping the AP (a SIGKILL here just kills hostapd
# with no auto-restart - confirmed the hard way).
sleep 5
HOSTAPD_CONF=/data/vendor/wifi/hostapd/hostapd_wlan2.conf
if [ -f "$HOSTAPD_CONF" ]; then
  sed -i "s/ieee80211w=1/ieee80211w=0/" "$HOSTAPD_CONF"
  HOSTAPD_PID=$(pgrep -x hostapd)
  [ -n "$HOSTAPD_PID" ] && kill -HUP "$HOSTAPD_PID"
fi

echo "$(date '+%Y-%m-%d %H:%M:%S') - hotspot started with SSID=$SSID (PMF disabled for client compatibility)" >> /data/adb/dashboard/hotspot_boot.log

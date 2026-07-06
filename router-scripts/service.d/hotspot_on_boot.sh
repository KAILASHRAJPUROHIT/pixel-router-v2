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
cmd wifi stop-softap >/dev/null 2>&1
sleep 2
cmd wifi start-softap "$SSID" wpa2 "$PASS"
echo "$(date '+%Y-%m-%d %H:%M:%S') - hotspot started with SSID=$SSID" >> /data/adb/dashboard/hotspot_boot.log

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

# Extra delay to let the Wi-Fi/tethering services fully initialize.
sleep 15

CFG="/data/misc_ce/0/apexdata/com.android.wifi/WifiConfigStoreSoftAp.xml"
SSID=$(cat "$CFG" 2>/dev/null | grep "WifiSsid" | sed -E 's/.*>&quot;(.*)&quot;<.*/\1/')
PASS=$(cat "$CFG" 2>/dev/null | grep "Passphrase" | sed -E 's/.*>([^<]*)<.*/\1/')

# Fallback only if the config file is somehow missing/unreadable.
# NOTE (repo copy): real fallback SSID/password redacted here - the
# on-device version has your actual values; this path is a rarely-hit
# last resort since SSID/PASS normally come from the real config above.
[ -z "$SSID" ] && SSID="CHANGEME_SSID"
[ -z "$PASS" ] && PASS="CHANGEME_PASSWORD"

cmd wifi start-softap "$SSID" wpa2 "$PASS"
echo "$(date '+%Y-%m-%d %H:%M:%S') - hotspot started with SSID=$SSID" >> /data/adb/dashboard/hotspot_boot.log

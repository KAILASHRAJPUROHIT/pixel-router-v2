#!/system/bin/sh
# Caps charging at 80% using the battery power_supply class's native
# charge_limit node - NOT /sys/devices/platform/google,charger/
# charge_start_level+charge_stop_level (tested 2026-07-05: that pair
# works but fully cuts CHGIN input at the cap, which flips
# BatteryService to status=4/"Not charging" and usb/online=0. Android's
# DreamManagerService requires mIsCharging=true to let a custom Daydream
# (our AOD) win over system Doze by default - so that mechanism silently
# killed the AOD every time the cap engaged.
#
# charge_limit instead tapers charge current down as the cap is
# approached (confirmed: current dropped ~1.7A -> ~0.9A -> ~0.67A while
# level held flat) without ever reporting "not charging" - status stays
# 2/Charging and usb/online stays 1, so mIsCharging stays true and the
# AOD keeps winning over Doze the whole time. This is the only charging
# control mechanism found tonight that protects the battery without
# taking down the AOD as a side effect.
#
# This sysfs node resets to its unset default (-1) on every reboot, same
# as charge_start_level/charge_stop_level did - hence this boot script.

while [ "$(getprop sys.boot_completed)" != "1" ]; do
  sleep 2
done
sleep 5

NODE=/sys/class/power_supply/battery/charge_limit
if [ -w "$NODE" ]; then
  echo 80 > "$NODE"
  echo "$(date '+%Y-%m-%d %H:%M:%S') - charge_limit set to 80 (AOD-compatible charge cap)" >> /data/adb/dashboard/battery_charge_limit.log
fi

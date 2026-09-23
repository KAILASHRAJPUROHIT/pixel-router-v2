#!/system/bin/sh
# One-shot boot script: re-applies every MAC-based iptables DROP rule
# from mac_blocklist.conf, since iptables state doesn't survive a
# reboot on its own the way the persistent conf file does. Pairs with
# toggle_block.cgi, which applies/removes these rules live when you
# click Deny/Allow on the dashboard - this just makes a Deny stick
# across reboots too.
BLOCKLIST_FILE=/data/adb/dashboard/mac_blocklist.conf

while [ "$(getprop sys.boot_completed)" != "1" ]; do
  sleep 2
done
sleep 15

[ -f "$BLOCKLIST_FILE" ] || exit 0

while IFS= read -r MAC; do
  [ -z "$MAC" ] && continue
  iptables -C FORWARD -m mac --mac-source "$MAC" -j DROP 2>/dev/null || iptables -I FORWARD 1 -m mac --mac-source "$MAC" -j DROP 2>/dev/null
  iptables -C INPUT -m mac --mac-source "$MAC" -j DROP 2>/dev/null || iptables -I INPUT 1 -m mac --mac-source "$MAC" -j DROP 2>/dev/null
done < "$BLOCKLIST_FILE"

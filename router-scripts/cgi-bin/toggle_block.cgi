#!/system/bin/sh
# Receives the dashboard's per-device Deny/Allow button POST and
# immediately applies (or removes) a MAC-based iptables DROP rule on
# both FORWARD (internet/LAN traffic) and INPUT (the router's own
# DHCP/DNS service) chains - blocking INPUT specifically means a denied
# device can't even get a new DHCP lease once its current one expires,
# not just "connected but no internet". Confirmed working live
# 2026-07-06: a test DROP rule on FORWARD immediately caught real
# packets from an active device within seconds of being inserted.
#
# mac_blocklist.conf is the persistent record (one MAC per line) -
# mac_blocklist_boot.sh re-applies these same rules on every boot, since
# iptables state doesn't survive a reboot on its own.
BLOCKLIST_FILE=/data/adb/dashboard/mac_blocklist.conf
[ -f "$BLOCKLIST_FILE" ] || : > "$BLOCKLIST_FILE"

BODY=""
if [ "$REQUEST_METHOD" = "POST" ] && [ -n "$CONTENT_LENGTH" ] && [ "$CONTENT_LENGTH" -gt 0 ] 2>/dev/null; then
  BODY=$(dd bs=1 count="$CONTENT_LENGTH" 2>/dev/null)
fi

# The hidden mac field's colons arrive percent-encoded (%3A) in a real
# browser POST - confirmed missing here 2026-07-06 (caught by the format
# validation below safely no-op'ing rather than doing anything
# destructive, but still a real bug: nothing ever actually got blocked).
# \\x (escaped backslash), not \x - sed drops a bare backslash before an
# ordinary replacement character, so \x alone never reaches printf %b as
# a real \xHH escape - same fix as rename_device.cgi/message.cgi.
decode_field() {
  RAW=$(printf '%s' "$BODY" | tr '&' '\n' | sed -n "s/^$1=//p" | head -1)
  DECODED=$(printf '%s' "$RAW" | sed 's/+/ /g; s/%/\\x/g')
  printf '%b' "$DECODED" 2>/dev/null
}

MAC=$(decode_field mac | tr 'A-Z' 'a-z')
ACTION=$(decode_field action)

# Must look like a real MAC - reject anything else outright rather than
# passing a malformed value to iptables.
case "$MAC" in
  [0-9a-f][0-9a-f]:[0-9a-f][0-9a-f]:[0-9a-f][0-9a-f]:[0-9a-f][0-9a-f]:[0-9a-f][0-9a-f]:[0-9a-f][0-9a-f]) ;;
  *) MAC="" ;;
esac

if [ -n "$MAC" ]; then
  if [ "$ACTION" = "block" ]; then
    grep -qi "^${MAC}$" "$BLOCKLIST_FILE" || echo "$MAC" >> "$BLOCKLIST_FILE"
    iptables -C FORWARD -m mac --mac-source "$MAC" -j DROP 2>/dev/null || iptables -I FORWARD 1 -m mac --mac-source "$MAC" -j DROP
    iptables -C INPUT -m mac --mac-source "$MAC" -j DROP 2>/dev/null || iptables -I INPUT 1 -m mac --mac-source "$MAC" -j DROP
  elif [ "$ACTION" = "unblock" ]; then
    # grep -v exits 1 (not 0) when the filtered result is empty (e.g.
    # this was the only MAC in the list) - that's not an error here, so
    # the mv must NOT be gated on grep's exit code via && or an unblock
    # emptying the list would silently leave the old file untouched.
    # Confirmed as a real bug 2026-07-06.
    grep -vi "^${MAC}$" "$BLOCKLIST_FILE" > "$BLOCKLIST_FILE.tmp" 2>/dev/null
    mv "$BLOCKLIST_FILE.tmp" "$BLOCKLIST_FILE"
    iptables -D FORWARD -m mac --mac-source "$MAC" -j DROP 2>/dev/null
    iptables -D INPUT -m mac --mac-source "$MAC" -j DROP 2>/dev/null
  fi
fi

echo "Status: 303 See Other"
echo "Location: /index.html"
echo ""

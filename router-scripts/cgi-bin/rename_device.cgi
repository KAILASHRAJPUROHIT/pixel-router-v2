#!/system/bin/sh
# Receives the dashboard's per-device "rename" form POST and writes/
# updates one line in device_names.conf (mac=name). Lets a device be
# named from the browser instead of an adb/root shell edit each time -
# Android's tethering DHCP server doesn't expose client hostnames here
# (confirmed 2026-07-06), so this manual mapping is the only source of
# device names update.sh can show.
STATE_FILE=/data/adb/dashboard/device_names.conf
[ -f "$STATE_FILE" ] || : > "$STATE_FILE"

BODY=""
if [ "$REQUEST_METHOD" = "POST" ] && [ -n "$CONTENT_LENGTH" ] && [ "$CONTENT_LENGTH" -gt 0 ] 2>/dev/null; then
  BODY=$(dd bs=1 count="$CONTENT_LENGTH" 2>/dev/null)
fi

# Two fields (mac=...&name=...), x-www-form-urlencoded, same decode
# approach as message.cgi - %b/printf as the format arg, not the format
# string, so stray % in the name can't be misread as a conversion.
decode_field() {
  RAW=$(printf '%s' "$BODY" | tr '&' '\n' | sed -n "s/^$1=//p" | head -1)
  # \\x (escaped backslash), not \x - sed drops a bare backslash before
  # an ordinary replacement character, so \x alone never reaches printf
  # %b as a real \xHH escape. Confirmed broken 2026-07-06 - a MAC's
  # colons (%3A) came out as literal "x3A" instead of ":".
  DECODED=$(printf '%s' "$RAW" | sed 's/+/ /g; s/%/\\x/g')
  printf '%b' "$DECODED" 2>/dev/null
}

MAC=$(decode_field mac | tr 'A-Z' 'a-z' | cut -c1-17)
NAME=$(decode_field name | cut -c1-60)

# MAC must look like a MAC (aa:bb:cc:dd:ee:ff) - reject anything else
# outright rather than writing a malformed line into the mapping file.
case "$MAC" in
  [0-9a-f][0-9a-f]:[0-9a-f][0-9a-f]:[0-9a-f][0-9a-f]:[0-9a-f][0-9a-f]:[0-9a-f][0-9a-f]:[0-9a-f][0-9a-f]) ;;
  *) MAC="" ;;
esac

if [ -n "$MAC" ] && [ -n "$NAME" ]; then
  grep -vi "^${MAC}=" "$STATE_FILE" > "$STATE_FILE.tmp" 2>/dev/null
  echo "${MAC}=${NAME}" >> "$STATE_FILE.tmp"
  chmod 644 "$STATE_FILE.tmp"
  mv "$STATE_FILE.tmp" "$STATE_FILE"
fi

echo "Status: 303 See Other"
echo "Location: /index.html"
echo ""

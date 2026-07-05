#!/system/bin/sh
# Receives the dashboard's custom-message form POST and stores it as a
# timed override. update.sh reads this file each regeneration cycle and,
# while unexpired, embeds the text as the "Custom Message" stat -
# RouterDreamService.java picks that up like any other stat and covers
# the whole AOD screen with it instead of the normal layout.
STATE_FILE=/data/adb/dashboard/custom_message.txt
DURATION=300 # 5 minutes

BODY=""
if [ "$REQUEST_METHOD" = "POST" ] && [ -n "$CONTENT_LENGTH" ] && [ "$CONTENT_LENGTH" -gt 0 ] 2>/dev/null; then
  BODY=$(dd bs=1 count="$CONTENT_LENGTH" 2>/dev/null)
fi

# Single-field form (message=...), x-www-form-urlencoded. %b/printf handles
# the escape decoding; using it as the format arg (not the format string
# itself) means stray % in the decoded text can't be misread as a printf
# conversion.
RAW=$(printf '%s' "$BODY" | sed -n 's/^message=//p')
DECODED=$(printf '%s' "$RAW" | sed 's/+/ /g; s/%/\x/g')
MESSAGE=$(printf '%b' "$DECODED" 2>/dev/null | cut -c1-200)

if [ -n "$MESSAGE" ]; then
  EXPIRY=$(( $(date +%s) + DURATION ))
  { echo "$EXPIRY"; echo "$MESSAGE"; } > "$STATE_FILE"
  chmod 644 "$STATE_FILE"
fi

echo "Status: 303 See Other"
echo "Location: /index.html"
echo ""

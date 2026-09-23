#!/system/bin/sh
# Receives the dashboard's custom-message form POST and stores it as a
# timed override. update.sh reads this file each regeneration cycle and,
# while unexpired, embeds the text as the "Custom Message" stat -
# RouterDreamService.java picks that up like any other stat and covers
# the whole AOD screen with it instead of the normal layout.
STATE_FILE=/data/adb/dashboard/custom_message.txt
DEFAULT_DURATION=300 # 5 minutes, used if duration_min is missing/invalid
MAX_DURATION=86400   # 24h cap - matches the form's max="1440" (minutes)

BODY=""
if [ "$REQUEST_METHOD" = "POST" ] && [ -n "$CONTENT_LENGTH" ] && [ "$CONTENT_LENGTH" -gt 0 ] 2>/dev/null; then
  BODY=$(dd bs=1 count="$CONTENT_LENGTH" 2>/dev/null)
fi

# Two fields now (message=...&duration_min=...) - split on '&' first
# rather than assuming the whole body is one field, same pattern as
# rename_device.cgi's decode_field().
decode_field() {
  RAW=$(printf '%s' "$BODY" | tr '&' '\n' | sed -n "s/^$1=//p" | head -1)
  # \\x (escaped backslash), not \x - sed drops a bare backslash before
  # an ordinary replacement character, so \x alone never reaches printf
  # %b as a real \xHH escape. Confirmed broken 2026-07-06.
  DECODED=$(printf '%s' "$RAW" | sed 's/+/ /g; s/%/\\x/g')
  printf '%b' "$DECODED" 2>/dev/null
}

MESSAGE=$(decode_field message | cut -c1-200)
DURATION_MIN=$(decode_field duration_min)

# Must be a plain positive integer within a sane range - reject anything
# else (blank, non-numeric, negative, absurdly large) and fall back to
# the default rather than writing a bogus expiry.
case "$DURATION_MIN" in
  ''|*[!0-9]*) DURATION=$DEFAULT_DURATION ;;
  *)
    DURATION=$((DURATION_MIN * 60))
    [ "$DURATION" -lt 60 ] && DURATION=60
    [ "$DURATION" -gt "$MAX_DURATION" ] && DURATION=$MAX_DURATION
    ;;
esac

if [ -n "$MESSAGE" ]; then
  EXPIRY=$(( $(date +%s) + DURATION ))
  { echo "$EXPIRY"; echo "$MESSAGE"; } > "$STATE_FILE"
  chmod 644 "$STATE_FILE"
fi

echo "Status: 303 See Other"
echo "Location: /index.html"
echo ""

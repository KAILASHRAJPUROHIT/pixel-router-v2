#!/system/bin/sh
# Receives the dashboard's AOD-layout picker form POST and applies it
# directly - no more needing adb/root shell to sed the app's own
# shared_prefs XML by hand the way this was done manually before today.
# busybox httpd (and the CGI scripts it spawns) already runs as root
# (started from a root service.d script), so this can write straight
# into another app's private data dir the same way root shell always
# could - no su wrapper needed here.
PREFS_FILE=/data/data/com.router.aod/shared_prefs/aod_prefs.xml

BODY=""
if [ "$REQUEST_METHOD" = "POST" ] && [ -n "$CONTENT_LENGTH" ] && [ "$CONTENT_LENGTH" -gt 0 ] 2>/dev/null; then
  BODY=$(dd bs=1 count="$CONTENT_LENGTH" 2>/dev/null)
fi

# A <select> value is always a bare digit here (never a free-text field
# that needs percent-decoding) - just extract it directly.
LAYOUT_IDX=$(printf '%s' "$BODY" | sed -n 's/^layout=//p')

# Must be a plain single/double-digit number 0-9 - reject anything else
# outright rather than writing a malformed value into the prefs file.
case "$LAYOUT_IDX" in
  [0-9]|[0-9][0-9]) ;;
  *) LAYOUT_IDX="" ;;
esac

if [ -n "$LAYOUT_IDX" ] && [ -f "$PREFS_FILE" ]; then
  sed -i "s/layout_index\" value=\"[0-9]*\"/layout_index\" value=\"${LAYOUT_IDX}\"/" "$PREFS_FILE"

  # Force a fresh render immediately rather than waiting for the next
  # natural idle-to-dream cycle - same force-stop + screensaver toggle +
  # wake sequence established earlier tonight (a plain wake alone only
  # resumes the cached dream, it doesn't reload the layout resource).
  am force-stop com.router.aod >/dev/null 2>&1
  settings put secure screensaver_enabled 0
  sleep 1
  settings put secure screensaver_enabled 1
  input keyevent 224 >/dev/null 2>&1
fi

echo "Status: 303 See Other"
echo "Location: /index.html"
echo ""

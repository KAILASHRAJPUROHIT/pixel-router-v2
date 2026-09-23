#!/system/bin/sh
# Receives the dashboard's AOD-theme picker form POST and applies it
# directly - same pattern as set_aod_layout.cgi (layout=structure,
# theme=color palette, independent choices per AodTheme.java).
PREFS_FILE=/data/data/com.router.aod/shared_prefs/aod_prefs.xml

BODY=""
if [ "$REQUEST_METHOD" = "POST" ] && [ -n "$CONTENT_LENGTH" ] && [ "$CONTENT_LENGTH" -gt 0 ] 2>/dev/null; then
  BODY=$(dd bs=1 count="$CONTENT_LENGTH" 2>/dev/null)
fi

THEME_IDX=$(printf '%s' "$BODY" | sed -n 's/^theme=//p')

case "$THEME_IDX" in
  [0-9]|[0-9][0-9]) ;;
  *) THEME_IDX="" ;;
esac

if [ -n "$THEME_IDX" ] && [ -f "$PREFS_FILE" ]; then
  sed -i "s/theme_index\" value=\"[0-9]*\"/theme_index\" value=\"${THEME_IDX}\"/" "$PREFS_FILE"
fi

# Headers first, slow work backgrounded after - same reasoning as
# set_aod_layout.cgi (busybox httpd falls back to a bare 200 with no
# real redirect if the CGI takes too long to print its first byte).
echo "Status: 303 See Other"
echo "Location: /index.html"
echo ""

if [ -n "$THEME_IDX" ] && [ -f "$PREFS_FILE" ]; then
  (
    am force-stop com.router.aod >/dev/null 2>&1
    settings put secure screensaver_enabled 0
    sleep 1
    settings put secure screensaver_enabled 1
    input keyevent 224 >/dev/null 2>&1
  ) &
fi

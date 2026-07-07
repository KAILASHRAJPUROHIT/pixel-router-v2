#!/system/bin/sh
# Receives the dashboard's billing-cycle-day dropdown form POST.
DAY_FILE=/data/adb/dashboard/billing_cycle_day

BODY=""
if [ "$REQUEST_METHOD" = "POST" ] && [ -n "$CONTENT_LENGTH" ] && [ "$CONTENT_LENGTH" -gt 0 ] 2>/dev/null; then
  BODY=$(dd bs=1 count="$CONTENT_LENGTH" 2>/dev/null)
fi

DAY=$(printf '%s' "$BODY" | sed -n 's/^day=//p')

case "$DAY" in
  [1-9]|[1-2][0-9]) ;;
  *) DAY="" ;;
esac

[ -n "$DAY" ] && echo "$DAY" > "$DAY_FILE"

echo "Status: 303 See Other"
echo "Location: /index.html"
echo ""

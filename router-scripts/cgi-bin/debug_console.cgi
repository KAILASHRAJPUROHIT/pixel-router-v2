#!/system/bin/sh
# Runs an arbitrary shell command as root and returns its output as
# plain text - added 2026-07-07 as a web-based equivalent of `adb
# shell` for when the phone is reachable via the dashboard but a full
# adb session isn't handy. Deliberately unrestricted (this IS a root
# shell, by design) - the only real gate is the dashboard's existing
# basic-auth layer, same trust boundary as the per-device Deny/Allow
# and custom-message features already exposed here.
#
# Bounded with `timeout` so a hung/blocking command (e.g. something
# waiting on stdin, or a runaway loop) can't wedge the CGI worker
# indefinitely - busybox httpd serves each request from a forked
# process, so a stuck command here doesn't take the whole dashboard
# down, but it would leave that request hanging forever without this.
MAX_SECONDS=20

BODY=""
if [ "$REQUEST_METHOD" = "POST" ] && [ -n "$CONTENT_LENGTH" ] && [ "$CONTENT_LENGTH" -gt 0 ] 2>/dev/null; then
  BODY=$(dd bs=1 count="$CONTENT_LENGTH" 2>/dev/null)
fi

# Same decode_field() pattern as message.cgi/rename_device.cgi/toggle_block.cgi.
decode_field() {
  RAW=$(printf '%s' "$BODY" | tr '&' '\n' | sed -n "s/^$1=//p" | head -1)
  DECODED=$(printf '%s' "$RAW" | sed 's/+/ /g; s/%/\\x/g')
  printf '%b' "$DECODED" 2>/dev/null
}

CMD=$(decode_field cmd)

echo "Content-Type: text/plain; charset=utf-8"
echo ""

if [ -z "$CMD" ]; then
  echo "(empty command)"
  exit 0
fi

timeout "$MAX_SECONDS" su -c "$CMD" 2>&1

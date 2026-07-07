#!/system/bin/sh
# Pulls YouTube/Instagram/Facebook follower counts from the AKD
# project's own always-on analytics API (Railway-hosted, separate
# project - see akd_secrets.properties) and caches them locally so the
# router dashboard/AOD can show them, added 2026-07-07.
#
# Follower counts change slowly - a 5 minute poll is plenty and keeps
# this well clear of the API's own rate limits.
#
# AKD's Instagram/Facebook numbers currently come from a Meta Graph API
# token that expires periodically (confirmed erroring as of
# 2026-07-04) - when a platform's status isn't "ok", this keeps
# whatever was last successfully fetched instead of blanking it out,
# same "last-known-good" pattern used for the dashboard's other
# best-effort stats.
SECRETS=/data/adb/dashboard/akd_secrets.properties
CACHE_FILE=/data/adb/dashboard/akd_followers.txt
LOG=/data/adb/dashboard/akd_followers_fetch.log
POLL_INTERVAL=300

log() {
  echo "$(date '+%Y-%m-%d %H:%M:%S') - $1" >> "$LOG"
}

[ -f "$SECRETS" ] || { log "akd_secrets.properties missing - exiting"; exit 0; }

AKD_API_URL=$(sed -n 's/^AKD_API_URL=//p' "$SECRETS")
AKD_API_KEY=$(sed -n 's/^AKD_API_KEY=//p' "$SECRETS")

while [ "$(getprop sys.boot_completed)" != "1" ]; do
  sleep 2
done
sleep 30

# Seed from any previous cache so a fetch failure right after boot
# still has something to show rather than "--".
YT="--"
IG="--"
FB="--"
if [ -f "$CACHE_FILE" ]; then
  YT=$(sed -n '1p' "$CACHE_FILE")
  IG=$(sed -n '2p' "$CACHE_FILE")
  FB=$(sed -n '3p' "$CACHE_FILE")
fi

while true; do
  # No curl on this device (busybox build doesn't include it, confirmed
  # 2026-07-07) - busybox wget with --header works fine for a direct
  # HTTPS call like this (no redirect involved, so the earlier-known
  # "wget drops custom headers across redirects" gotcha doesn't apply).
  RESP=$(/data/adb/magisk/busybox wget -qO- -T 15 --header="X-API-Key: $AKD_API_KEY" "$AKD_API_URL" 2>/dev/null)

  if [ -n "$RESP" ]; then
    # No jq on this device - pull each platform's own status/count with
    # a small awk/grep pass rather than a full JSON parser, same
    # approach as the rest of this dashboard's shell code.
    YT_STATUS=$(printf '%s' "$RESP" | sed -n 's/.*"youtube":{"status":"\([a-z]*\)".*/\1/p')
    YT_COUNT=$(printf '%s' "$RESP" | sed -n 's/.*"youtube":{[^}]*"subscribers":\([0-9]*\).*/\1/p')
    IG_STATUS=$(printf '%s' "$RESP" | sed -n 's/.*"instagram":{"status":"\([a-z]*\)".*/\1/p')
    IG_COUNT=$(printf '%s' "$RESP" | sed -n 's/.*"instagram":{[^}]*"followers":\([0-9]*\).*/\1/p')
    FB_STATUS=$(printf '%s' "$RESP" | sed -n 's/.*"facebook":{"status":"\([a-z]*\)".*/\1/p')
    FB_COUNT=$(printf '%s' "$RESP" | sed -n 's/.*"facebook":{[^}]*"followers":\([0-9]*\).*/\1/p')

    [ "$YT_STATUS" = "ok" ] && [ -n "$YT_COUNT" ] && YT="$YT_COUNT"
    [ "$IG_STATUS" = "ok" ] && [ -n "$IG_COUNT" ] && IG="$IG_COUNT"
    [ "$FB_STATUS" = "ok" ] && [ -n "$FB_COUNT" ] && FB="$FB_COUNT"

    { echo "$YT"; echo "$IG"; echo "$FB"; } > "$CACHE_FILE"
  else
    log "fetch failed - keeping last-known values (YT=$YT IG=$IG FB=$FB)"
  fi

  sleep "$POLL_INTERVAL"
done

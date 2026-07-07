#!/system/bin/sh
# Pulls YouTube/Instagram/Facebook follower counts from the AKD
# project's own always-on analytics API (Railway-hosted, separate
# project - see akd_secrets.properties) and caches them locally so the
# router dashboard/AOD can show them, added 2026-07-07.
#
# Follower counts change slowly - a 5 minute poll is plenty and keeps
# this well clear of the API's own rate limits.
#
# AKD's Instagram/Facebook numbers come from a Meta Graph API token
# that expires periodically on AKD's own side (confirmed erroring as of
# 2026-07-04, a token-expiry issue on AKD's infrastructure, not
# anything wrong with this router). When a platform's status isn't
# "ok", this keeps whatever was last successfully fetched instead of
# blanking it out, same "last-known-good" pattern used for the
# dashboard's other best-effort stats.
#
# 2026-07-07: added a direct Meta Graph API path for IG/FB specifically
# (see meta_secrets.properties.example) so AKD's own token health stops
# being a single point of failure for what this router's dashboard
# shows - if meta_secrets.properties is present, IG/FB are sourced
# directly from Meta instead of via AKD; YouTube still comes from AKD
# either way (unaffected by the Meta token issue, no reason to touch it).
SECRETS=/data/adb/dashboard/akd_secrets.properties
META_SECRETS=/data/adb/dashboard/meta_secrets.properties
CACHE_FILE=/data/adb/dashboard/akd_followers.txt
LOG=/data/adb/dashboard/akd_followers_fetch.log
POLL_INTERVAL=300

log() {
  echo "$(date '+%Y-%m-%d %H:%M:%S') - $1" >> "$LOG"
}

[ -f "$SECRETS" ] || { log "akd_secrets.properties missing - exiting"; exit 0; }

AKD_API_URL=$(sed -n 's/^AKD_API_URL=//p' "$SECRETS")
AKD_API_KEY=$(sed -n 's/^AKD_API_KEY=//p' "$SECRETS")

USE_META_DIRECT=0
if [ -f "$META_SECRETS" ]; then
  META_ACCESS_TOKEN=$(sed -n 's/^META_ACCESS_TOKEN=//p' "$META_SECRETS")
  META_FB_PAGE_ID=$(sed -n 's/^META_FB_PAGE_ID=//p' "$META_SECRETS")
  META_IG_USER_ID=$(sed -n 's/^META_IG_USER_ID=//p' "$META_SECRETS")
  if [ -n "$META_ACCESS_TOKEN" ] && [ -n "$META_FB_PAGE_ID" ] && [ -n "$META_IG_USER_ID" ]; then
    USE_META_DIRECT=1
    log "meta_secrets.properties present - fetching IG/FB directly from Meta Graph API instead of via AKD"
  fi
fi

# Fetches one field from a Meta Graph API node directly. Prints the
# numeric value on success, prints nothing (and logs the error message)
# on failure - same "keep last-known-good" handling as the AKD path.
fetch_meta_field() {
  NODE_ID="$1"
  FIELD="$2"
  RESP=$(/data/adb/magisk/busybox wget -qO- -T 15 "https://graph.facebook.com/v19.0/${NODE_ID}?fields=${FIELD}&access_token=${META_ACCESS_TOKEN}" 2>/dev/null)
  ERROR_MSG=$(printf '%s' "$RESP" | sed -n 's/.*"message":"\([^"]*\)".*/\1/p')
  if [ -n "$ERROR_MSG" ]; then
    log "Meta Graph API error fetching $FIELD for $NODE_ID: $ERROR_MSG"
    return 1
  fi
  printf '%s' "$RESP" | sed -n "s/.*\"${FIELD}\":\([0-9]*\).*/\1/p"
}

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
    # AKD's actual JSON field for Facebook is "fans", not "followers" -
    # confirmed 2026-07-07 via social_forensics.py's _pull_facebook,
    # which reads fan_count from the Graph API. This grep would have
    # silently never matched even once AKD's own FB permission error
    # (Meta App Review's pages_read_engagement scope) gets fixed.
    FB_COUNT=$(printf '%s' "$RESP" | sed -n 's/.*"facebook":{[^}]*"fans":\([0-9]*\).*/\1/p')

    [ "$YT_STATUS" = "ok" ] && [ -n "$YT_COUNT" ] && YT="$YT_COUNT"
    if [ "$USE_META_DIRECT" = "0" ]; then
      [ "$IG_STATUS" = "ok" ] && [ -n "$IG_COUNT" ] && IG="$IG_COUNT"
      [ "$FB_STATUS" = "ok" ] && [ -n "$FB_COUNT" ] && FB="$FB_COUNT"
    fi
  else
    log "AKD fetch failed - keeping last-known YT value (YT=$YT)"
  fi

  if [ "$USE_META_DIRECT" = "1" ]; then
    DIRECT_IG=$(fetch_meta_field "$META_IG_USER_ID" followers_count)
    [ -n "$DIRECT_IG" ] && IG="$DIRECT_IG"
    DIRECT_FB=$(fetch_meta_field "$META_FB_PAGE_ID" fan_count)
    [ -n "$DIRECT_FB" ] && FB="$DIRECT_FB"
  fi

  { echo "$YT"; echo "$IG"; echo "$FB"; } > "$CACHE_FILE"

  sleep "$POLL_INTERVAL"
done

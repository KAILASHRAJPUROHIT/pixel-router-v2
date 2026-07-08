#!/system/bin/sh
# One-time boot check: compares each real on-device config file's key
# set against its .example template, so a missing/typo'd/incomplete
# copy shows up immediately and clearly instead of surfacing later as a
# silent failure or an unclear error buried deep in some other script's
# log (a scenario that's happened for real - see failover.properties'
# own apostrophe-mismatch bug from 2026-07-06, a different class of
# problem but the same underlying issue: nothing checks these files are
# actually complete/correct until something downstream breaks).
#
# One-shot by design (matches this project's other boot-only scripts,
# not registered in service_supervisor.sh's SCRIPTS list) - config
# files don't change at runtime, so there's nothing to keep watching.
#
# Compares against copies of the .example templates pushed to
# config_templates/ on-device (not the real .example files - those stay
# in the repo only, as reference for setting up a fresh copy of this
# project elsewhere).

STATE_DIR=/data/adb/dashboard
LOG="$STATE_DIR/config_validate.log"
STATUS_FILE="$STATE_DIR/config_validate_status"
TEMPLATE_DIR="$STATE_DIR/config_templates"

log() {
  echo "$(date '+%Y-%m-%d %H:%M:%S') - $1" >> "$LOG"
}

extract_keys() {
  grep -oE '^[A-Za-z0-9_.]+=' "$1" 2>/dev/null | sed 's/=$//' | sort -u
}

check_one() {
  NAME="$1"
  TEMPLATE="$2"
  REAL="$3"

  if [ ! -f "$REAL" ]; then
    log "MISSING: $NAME - $REAL does not exist at all"
    echo "$NAME: MISSING" >> "$STATUS_FILE.tmp"
    return
  fi

  if [ ! -f "$TEMPLATE" ]; then
    log "SKIP: $NAME - no template at $TEMPLATE to compare against"
    return
  fi

  TEMPLATE_KEYS=$(extract_keys "$TEMPLATE")
  REAL_KEYS=$(extract_keys "$REAL")

  MISSING_KEYS=""
  for K in $TEMPLATE_KEYS; do
    echo "$REAL_KEYS" | grep -qx "$K" || MISSING_KEYS="$MISSING_KEYS $K"
  done

  # Templates use CHANGEME/changeme/XXXXXXXXXX-style placeholders -
  # catching those still present in the real file catches "copied the
  # template but forgot to fill in a value" even when the key exists.
  PLACEHOLDER_KEYS=""
  for K in $TEMPLATE_KEYS; do
    VAL=$(grep "^$K=" "$REAL" 2>/dev/null | head -1 | cut -d= -f2-)
    case "$VAL" in
      *CHANGEME*|*changeme*|*XXXXXXXXXX*|your_akd_api_key_here|"") PLACEHOLDER_KEYS="$PLACEHOLDER_KEYS $K" ;;
    esac
  done

  if [ -n "$MISSING_KEYS" ]; then
    log "INCOMPLETE: $NAME is missing key(s):$MISSING_KEYS"
    echo "$NAME: MISSING_KEYS ($MISSING_KEYS )" >> "$STATUS_FILE.tmp"
  elif [ -n "$PLACEHOLDER_KEYS" ]; then
    log "PLACEHOLDER: $NAME still has unfilled placeholder value(s) for:$PLACEHOLDER_KEYS"
    echo "$NAME: PLACEHOLDER ($PLACEHOLDER_KEYS )" >> "$STATUS_FILE.tmp"
  else
    log "OK: $NAME - all $(echo "$TEMPLATE_KEYS" | wc -w) template keys present with real values"
    echo "$NAME: OK" >> "$STATUS_FILE.tmp"
  fi
}

while [ "$(getprop sys.boot_completed)" != "1" ]; do
  sleep 2
done
sleep 15

> "$STATUS_FILE.tmp"
log "=== config validation starting ==="

check_one "failover.properties" "$TEMPLATE_DIR/failover.properties.example" "$STATE_DIR/failover.properties"
check_one "akd_secrets.properties" "$TEMPLATE_DIR/akd_secrets.properties.example" "$STATE_DIR/akd_secrets.properties"
check_one "hotspot_secrets.properties" "$TEMPLATE_DIR/hotspot_secrets.properties.example" "$STATE_DIR/hotspot_secrets.properties"

# Not a check_one() case - the real dashboard basic-auth credentials
# live directly in httpd.conf's own "/:user:pass" line, not in a
# dashboard_auth.properties file under the AOD app's data directory
# (that path was checked here before, found genuinely MISSING live
# 2026-07-08, and turned out to be checking a stale/wrong location -
# the actual live auth mechanism was never broken, this validator was
# just pointed at the wrong file).
HTTPD_CONF="/data/local/tmp/dashboard/httpd.conf"
if [ -f "$HTTPD_CONF" ] && grep -qE '^/:[^:]+:[^:]+$' "$HTTPD_CONF"; then
  log "OK: httpd.conf - basic-auth line present and non-empty"
  echo "httpd.conf: OK" >> "$STATUS_FILE.tmp"
else
  log "MISSING: httpd.conf has no valid \"/:user:pass\" basic-auth line - the dashboard would be unprotected"
  echo "httpd.conf: MISSING" >> "$STATUS_FILE.tmp"
fi

# Optional - akd_followers_fetch.sh falls back to AKD's own relay for
# IG/FB when this isn't present, so a missing file here isn't an error,
# only an incomplete/placeholder-filled one is worth flagging.
if [ -f "$STATE_DIR/meta_secrets.properties" ]; then
  check_one "meta_secrets.properties" "$TEMPLATE_DIR/meta_secrets.properties.example" "$STATE_DIR/meta_secrets.properties"
else
  log "SKIP: meta_secrets.properties not present (optional - AKD relay used for IG/FB instead)"
fi

mv "$STATUS_FILE.tmp" "$STATUS_FILE"
log "=== config validation done ==="

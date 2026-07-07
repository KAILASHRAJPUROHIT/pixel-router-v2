#!/system/bin/sh
# A chat-style window into hotspot_manager.sh's own state - added
# 2026-07-08. This is NOT a language model - hotspot_manager.sh is
# rule-based by explicit choice (see its own header comment), so this
# matches keywords in what's typed against the manager's real state
# files (its status JSON, its log, its daily ops counter) and answers
# from that, honestly, rather than pretending to be a general AI. It
# can only talk about what the manager actually watches.
STATE_DIR=/data/adb/dashboard
HISTORY_FILE="$STATE_DIR/manager_chat_history.jsonl"
STATUS_FILE="$STATE_DIR/hotspot_manager_status"
LOG="$STATE_DIR/hotspot_manager.log"

echo "Content-Type: application/json; charset=utf-8"
echo ""

BODY=""
if [ "$REQUEST_METHOD" = "POST" ] && [ -n "$CONTENT_LENGTH" ] && [ "$CONTENT_LENGTH" -gt 0 ] 2>/dev/null; then
  BODY=$(dd bs=1 count="$CONTENT_LENGTH" 2>/dev/null)
fi

decode_field() {
  RAW=$(printf '%s' "$BODY" | tr '&' '\n' | sed -n "s/^$1=//p" | head -1)
  DECODED=$(printf '%s' "$RAW" | sed 's/+/ /g; s/%/\\x/g')
  printf '%b' "$DECODED" 2>/dev/null
}

MSG=$(decode_field message)
MSG_LC=$(echo "$MSG" | tr 'A-Z' 'a-z')

REPLY=""
case "$MSG_LC" in
  *status*|*"how are you"*|*health*|*"all good"*)
    if [ -f "$STATUS_FILE" ]; then
      HOTSPOT=$(sed -n 's/.*"hotspot":\([01]\).*/\1/p' "$STATUS_FILE")
      DASH=$(sed -n 's/.*"dashboard":\([01]\).*/\1/p' "$STATUS_FILE")
      AODV=$(sed -n 's/.*"aod":\([01]\).*/\1/p' "$STATUS_FILE")
      NET=$(sed -n 's/.*"internet":\([01]\).*/\1/p' "$STATUS_FILE")
      ITYPE=$(sed -n 's/.*"internet_type":"\([^"]*\)".*/\1/p' "$STATUS_FILE")
      INTERVAL=$(sed -n 's/.*"poll_interval_s":\([0-9]*\).*/\1/p' "$STATUS_FILE")
      if [ "$HOTSPOT" = "1" ] && [ "$DASH" = "1" ] && [ "$AODV" = "1" ] && [ "$NET" = "1" ]; then
        REPLY="Everything looks healthy - hotspot, dashboard, and AOD are all up, and I've got real internet. Currently on ${ITYPE} internet. Checking every ${INTERVAL}s."
      else
        H_OK="ok"; [ "$HOTSPOT" != "1" ] && H_OK="DOWN"
        D_OK="ok"; [ "$DASH" != "1" ] && D_OK="DOWN"
        A_OK="ok"; [ "$AODV" != "1" ] && A_OK="DOWN"
        N_OK="ok"; [ "$NET" != "1" ] && N_OK="DOWN"
        REPLY="Something's off: hotspot=${H_OK}, dashboard=${D_OK}, AOD=${A_OK}, internet=${N_OK}."
      fi
    else
      REPLY="I haven't finished my first check since boot yet - give me a minute."
    fi
    ;;
  *alert*|*warn*|*problem*|*issue*|*wrong*)
    if [ -f "$LOG" ]; then
      RECENT=$(grep "ALERT" "$LOG" 2>/dev/null | tail -3 | tr '\n' '|' | sed 's/|/  |  /g; s/  |  $//')
      if [ -n "$RECENT" ]; then
        REPLY="Recent alerts: ${RECENT}"
      else
        REPLY="No alerts in my log - nothing's tripped any of my checks recently."
      fi
    else
      REPLY="No alerts logged yet - I've been quiet, which is a good sign."
    fi
    ;;
  *interval*|*"how often"*|*poll*|*frequency*)
    INTERVAL=$(sed -n 's/.*"poll_interval_s":\([0-9]*\).*/\1/p' "$STATUS_FILE" 2>/dev/null)
    [ -z "$INTERVAL" ] && INTERVAL="60"
    REPLY="I'm checking every ${INTERVAL}s right now. I back off toward 300s when everything's been healthy for a while, and snap back to 60s the instant something looks wrong - that's the heat/network budget in action."
    ;;
  *budget*|*ops*|*heat*|*network*)
    OPS=$(cat "$STATE_DIR/hotspot_manager_ops_today" 2>/dev/null || echo 0)
    REPLY="I've made ${OPS} heavy checks (pings/dumpsys calls) today. I skip the cellular-signal check entirely when we're not on cellular, and stretch my polling interval when things are healthy - both keep my own footprint small."
    ;;
  *flap*|*restart*|*crash*|*loop*)
    FLAPS=$(ls "$STATE_DIR"/manager_alert_flap_* 2>/dev/null | wc -l)
    if [ "$FLAPS" -gt 0 ]; then
      REPLY="I've flagged ${FLAPS} script(s) as restart-looping recently - check my log for which ones."
    else
      REPLY="Nothing's flapping right now - no service has restarted often enough to trip my check."
    fi
    ;;
  *reboot*)
    LASTREBOOT=$(grep "rebooting as last resort" "$LOG" 2>/dev/null | tail -1)
    if [ -n "$LASTREBOOT" ]; then
      REPLY="Last time I triggered a reboot: ${LASTREBOOT}"
    else
      REPLY="I haven't had to reboot the device myself - that only happens if hotspot, dashboard, and AOD are all down at once for 3 checks straight."
    fi
    ;;
  *help*|*"what can you"*|*command*)
    REPLY="Ask me about: status, alerts, poll interval, today's ops budget, flapping services, or reboots. I'm rule-based, not a language model - I match what you type against my real state files, so I can only answer about what I actually watch."
    ;;
  "")
    REPLY="Say something and I'll check my own state for an answer."
    ;;
  *)
    REPLY="I don't have a specific answer for that. Try: status, alerts, poll interval, ops budget, flapping services, or reboots."
    ;;
esac

TS=$(date +%s)
MSG_JSON=$(printf '%s' "$MSG" | sed 's/\\/\\\\/g; s/"/\\"/g')
REPLY_JSON=$(printf '%s' "$REPLY" | sed 's/\\/\\\\/g; s/"/\\"/g')

printf '{"reply":"%s","ts":%s}' "$REPLY_JSON" "$TS"

echo "{\"role\":\"user\",\"text\":\"$MSG_JSON\",\"ts\":$TS}" >> "$HISTORY_FILE"
echo "{\"role\":\"manager\",\"text\":\"$REPLY_JSON\",\"ts\":$TS}" >> "$HISTORY_FILE"
tail -40 "$HISTORY_FILE" > "$HISTORY_FILE.tmp" 2>/dev/null && mv "$HISTORY_FILE.tmp" "$HISTORY_FILE"

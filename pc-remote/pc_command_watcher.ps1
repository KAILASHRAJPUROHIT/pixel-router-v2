# Polls the router dashboard for a queued PC remote-control command
# (restart_rustdesk / restart_pc) and executes it locally.
#
# There's no direct phone->PC connectivity path (the router's WireGuard
# tunnel already occupies this phone's one VpnService slot for the
# Airtel-bypass routing, so Tailscale-on-the-phone isn't an option) - so
# instead this script polls a small JSON endpoint over the same public
# dashboard URL, gated by a static shared key (not the dashboard's own
# TOTP session, which this unattended script can't complete).
#
# restart_pc queues a 60s-delayed shutdown, not an immediate one - gives
# anyone physically at the PC a chance to notice and cancel with
# `shutdown /a`, since this can be triggered remotely from anywhere the
# dashboard is reachable.

$DashboardUrl = "https://turban-tried-snarl.ngrok-free.dev/pc-command.json"
# Read from a gitignored local file rather than hardcoded here - this
# script lives in the pixel-router-v2 repo, which gets pushed to a
# remote; a real secret must never be a literal in tracked source.
# See pc_command_key.txt.example for the expected format.
$KeyFile = Join-Path $PSScriptRoot "pc_command_key.txt"
if (-not (Test-Path $KeyFile)) {
    Write-Error "Missing $KeyFile - copy pc_command_key.txt.example to pc_command_key.txt and fill in the real key from the phone's /data/adb/router_v2/secrets/pc_command_key.txt"
    exit 1
}
$Key = (Get-Content $KeyFile -Raw).Trim()
$PollIntervalSeconds = 60
$StateFile = Join-Path $PSScriptRoot "pc_command_state.json"

function Get-LastProcessedTs {
    if (Test-Path $StateFile) {
        try { return (Get-Content $StateFile -Raw | ConvertFrom-Json).last_processed_ts } catch { return 0 }
    }
    return 0
}

function Set-LastProcessedTs($ts) {
    @{ last_processed_ts = $ts } | ConvertTo-Json | Set-Content $StateFile
}

function Invoke-Command2($command) {
    switch ($command) {
        "restart_rustdesk" {
            Write-Output "$(Get-Date) - restarting RustDesk service"
            try {
                Restart-Service -Name RustDesk -Force -ErrorAction Stop
                Write-Output "$(Get-Date) - RustDesk restarted successfully"
            } catch {
                Write-Output "$(Get-Date) - RustDesk restart FAILED: $_"
            }
        }
        "restart_pc" {
            Write-Output "$(Get-Date) - queuing PC restart (60s grace period, cancel with 'shutdown /a')"
            shutdown /r /t 60 /c "Restart requested from router dashboard"
        }
        default {
            Write-Output "$(Get-Date) - unknown command '$command' - ignoring"
        }
    }
}

Write-Output "$(Get-Date) - pc_command_watcher starting"
$lastProcessedTs = Get-LastProcessedTs

while ($true) {
    try {
        # ngrok's free-tier domains show an HTML abuse-warning
        # interstitial (ERR_NGROK_6024) to requests whose User-Agent
        # looks browser-like - PowerShell's Invoke-RestMethod default
        # UA triggers it, returning that warning text instead of JSON.
        # This header bypasses it, same fix local-router's own API
        # callers already needed against this exact domain.
        $resp = Invoke-RestMethod -Uri "$DashboardUrl`?key=$Key" -Headers @{ "ngrok-skip-browser-warning" = "true" } -TimeoutSec 15 -ErrorAction Stop
        if ($resp.ts -gt $lastProcessedTs) {
            Write-Output "$(Get-Date) - new command queued: $($resp.command) (ts=$($resp.ts))"
            Invoke-Command2 $resp.command
            $lastProcessedTs = $resp.ts
            Set-LastProcessedTs $lastProcessedTs
        }
    } catch {
        Write-Output "$(Get-Date) - poll failed: $_"
    }
    Start-Sleep -Seconds $PollIntervalSeconds
}

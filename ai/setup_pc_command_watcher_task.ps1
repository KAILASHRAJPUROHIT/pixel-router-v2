# Run this once, as Administrator (right-click this file in Explorer >
# "Run with PowerShell" won't ask for elevation on its own - open an
# admin PowerShell window instead and run:
#   D:\AI_PROJECTS\pixel-router\ai\setup_pc_command_watcher_task.ps1
#
# Registers PixelRouter_PCCommandWatcher as a Scheduled Task, matching
# the existing PixelRouter_Analyzer/PixelRouter_Heartbeat pattern but
# with RunLevel Highest - needed because this watcher restarts the
# RustDesk Windows service and queues PC reboots, both of which require
# admin rights that a normal (non-elevated) logon-trigger task doesn't
# have. Registering a Highest-runlevel task itself requires an elevated
# session, which is why this can't just be run directly - confirmed
# live 2026-07-07 ("Access is denied" from a non-elevated shell).

$action = New-ScheduledTaskAction -Execute "C:\Python314\pythonw.exe" -Argument "pc_command_watcher.py" -WorkingDirectory "D:\AI_PROJECTS\pixel-router\ai"
$trigger = New-ScheduledTaskTrigger -AtLogOn
$principal = New-ScheduledTaskPrincipal -UserId "KAILASH" -LogonType Interactive -RunLevel Highest
$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1)

Register-ScheduledTask -TaskName "PixelRouter_PCCommandWatcher" -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Description "Polls the router dashboard for queued PC restart / RustDesk restart commands" -Force

Write-Host "Registered. Starting it now so it's live without needing to log out/in..."
Start-ScheduledTask -TaskName "PixelRouter_PCCommandWatcher"
Start-Sleep -Seconds 2
Get-ScheduledTask -TaskName "PixelRouter_PCCommandWatcher" | Get-ScheduledTaskInfo | Select-Object TaskName, LastRunTime, LastTaskResult

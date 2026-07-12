@echo off
cd /d "%~dp0"
:loop
echo ==== %date% %time% starting pc_command_watcher ==== >> watcher.log
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0pc_command_watcher.ps1" >> watcher.log 2>&1
echo ==== %date% %time% watcher exited, restarting in 5s ==== >> watcher.log
timeout /t 5 /nobreak >nul
goto loop

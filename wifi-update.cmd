@echo off
rem Put the latest margin-init on the tablet over Wi-Fi (no USB). First time: wifi-update.cmd <tablet IP>
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0wifi-update.ps1" %*

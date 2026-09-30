@echo off
rem OwpenGram server -- prebuilt release archive launcher (Windows).
rem
rem Nothing here builds anything: bin\owpengram-server.exe,
rem bin\owpengram-admin-panel.exe and owpengram-ctl.exe are already compiled
rem for this platform. This just forwards to owpengram-ctl.exe, which
rem bootstraps .env on the very first run and otherwise starts/stops/
rem restarts both processes.
rem
rem   start.bat            first run: bootstrap + start. later: just start.
rem   start.bat stop
rem   start.bat status
rem   start.bat restart
rem   start.bat logs
setlocal
cd /d "%~dp0"
owpengram-ctl.exe %*

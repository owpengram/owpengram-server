@echo off
rem OwpenGram server -- prebuilt release archive launcher (Windows).
rem
rem Nothing here builds anything: bin\owpengram-server.exe,
rem bin\owpengram-admin-panel.exe and owpengram-ctl.exe are already compiled
rem for this platform. owpengram-ctl.exe stays in this window and runs the
rem server and its admin panel until it is stopped (Ctrl+C or closing the
rem window). On the very first run it creates .env and prints the admin panel
rem address and password; everything else is set up in the admin panel.
rem
rem   start.bat            run the server and the admin panel.
rem   start.bat stop       stop a running one (from another window).
rem   start.bat status
rem   start.bat restart
rem   start.bat logs
setlocal
cd /d "%~dp0"
owpengram-ctl.exe %*
set "EXITCODE=%errorlevel%"
if not "%EXITCODE%"=="0" pause
exit /b %EXITCODE%

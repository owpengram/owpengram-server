@echo off
setlocal
cd /d "%~dp0"

rem Builds and runs bin\telesrv-ctl.exe (cmd\telesrv-ctl) -- Go is the only
rem prerequisite. telesrv-ctl stays in this window and runs the server and its
rem admin panel until it is stopped (Ctrl+C or closing the window); everything
rem else is set up in the admin panel, whose address it prints.
rem
rem   owpengram-server.bat           run the server and the admin panel.
rem   owpengram-server.bat stop      stop a running one (from another window).
rem   owpengram-server.bat status    show what is running.
rem   owpengram-server.bat restart   restart the server and the admin panel.
rem   owpengram-server.bat update    git pull, rebuild, restart.
rem   owpengram-server.bat logs      print the current run's startup log.

where go >nul 2>&1
if errorlevel 1 (
  echo [ERROR] Go is not installed ^(needed to build owpengram-server / owpengram-admin-panel^)
  echo         Install it from: https://go.dev/dl/ and re-run this script
  pause
  exit /b 1
)
for /f "delims=" %%v in ('go version') do echo [ok] Go found: %%v

echo [cfg] Building telesrv-ctl...
go build -o bin\telesrv-ctl.exe .\cmd\telesrv-ctl
if errorlevel 1 (
  echo.
  echo [ERROR] failed to build telesrv-ctl -- see the messages above
  pause
  exit /b 1
)

echo.
bin\telesrv-ctl.exe %*
set "EXITCODE=%errorlevel%"
if not "%EXITCODE%"=="0" pause
exit /b %EXITCODE%

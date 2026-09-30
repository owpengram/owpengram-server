@echo off
setlocal enabledelayedexpansion
cd /d "%~dp0"

rem Builds and runs bin\telesrv-ctl.exe (cmd\telesrv-ctl, wrapping
rem internal\procctl.Manager) -- Go is the only prerequisite this checks for.
rem Docker is optional and only checked informationally: absent,
rem TELESRV_POSTGRES_DSN must already point at a reachable PostgreSQL
rem instead.
rem
rem   owpengram-server.bat          bootstraps .env on a fresh install,
rem                                 starts everything, prints the admin
rem                                 panel URL, then drops into an interactive
rem                                 menu for stop/restart/status/update/logs/
rem                                 edition.
rem   owpengram-server.bat stop     stops both processes.
rem   owpengram-server.bat status   shows whether each process/container is up.
rem   owpengram-server.bat restart  rebuilds and relaunches both.
rem   owpengram-server.bat update   git pull --ff-only, then restart.
rem   owpengram-server.bat logs     prints the current run's startup log.

echo == Checking prerequisites ==

where go >nul 2>&1
if errorlevel 1 (
  echo [ERROR] Go is not installed ^(needed to build owpengram-server / owpengram-admin-panel^)
  echo         Install it from: https://go.dev/dl/
  rem Hand off to the winget installer rather than stopping at a shopping
  rem list. OWPENGRAM_PREREQS_TRIED bounds this to a single retry, so
  rem something that will not install ends in a message instead of a loop.
  if defined OWPENGRAM_PREREQS_TRIED (
    echo.
    echo [ERROR] Go is still missing after the install attempt -- see the messages above
    pause
    exit /b 1
  )
  if not exist "scripts\install-prereqs.ps1" (
    echo.
    echo [ERROR] install Go and re-run this script
    pause
    exit /b 1
  )
  echo.
  echo == Installing the missing prerequisites ==
  powershell -NoProfile -ExecutionPolicy Bypass -File "scripts\install-prereqs.ps1"
  if errorlevel 1 (
    echo.
    echo [ERROR] could not install the prerequisites -- see the messages above
    pause
    exit /b 1
  )
  rem winget writes the new PATH to the registry, but this console still
  rem holds the one it started with -- reload it so the re-check below can
  rem see what was just installed instead of asking for a fresh terminal.
  for /f "usebackq delims=" %%p in (`powershell -NoProfile -Command "[Environment]::GetEnvironmentVariable('Path','Machine') + ';' + [Environment]::GetEnvironmentVariable('Path','User')"`) do set "PATH=%%p"
  set "OWPENGRAM_PREREQS_TRIED=1"
  echo.
  call "%~f0" %*
  exit /b !errorlevel!
)
for /f "delims=" %%v in ('go version') do echo [ok] Go found: %%v

where docker >nul 2>&1
if errorlevel 1 (
  echo [info] Docker not found -- PostgreSQL/MinIO won't be started automatically.
  echo        Either install Docker Desktop, or point TELESRV_POSTGRES_DSN at an
  echo        already-reachable PostgreSQL instance in .env.
) else (
  echo [ok] Docker found.
)

echo.
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
if "%~1"=="" if not "%EXITCODE%"=="0" pause
exit /b %EXITCODE%

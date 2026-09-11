@echo off
setlocal enabledelayedexpansion
cd /d "%~dp0"

rem Checks prerequisites (Go, Python 3, and its packages), then hands off to
rem tui-panel\server-panel.py. Run this instead of that script directly so
rem missing prerequisites get a clear message instead of a Python traceback.
rem
rem   owpengram-server.bat         bootstraps .env on a fresh install, starts
rem                                everything, prints the admin panel URL,
rem                                and exits -- no prompts. First-time setup
rem                                (branding, SMTP, the admin password) then
rem                                happens in that web panel, not here.
rem   owpengram-server.bat panel   the interactive TUI instead -- stop/
rem                                restart/logs/.env editing from a menu.

echo == Checking prerequisites ==

set "PROBLEMS=0"

rem --- Go ---------------------------------------------------------------
where go >nul 2>&1
if errorlevel 1 (
  echo [WARN] Go is not installed ^(needed to build owpengram-server / owpengram-admin-panel^)
  echo        Install it from: https://go.dev/dl/
  set "PROBLEMS=1"
) else (
  for /f "delims=" %%v in ('go version') do echo [ok] Go found: %%v
)

rem --- Python -------------------------------------------------------------
rem Just checking "where" isn't enough: on Windows, python.exe / python3.exe
rem can resolve to the Microsoft Store app-execution-alias stub, which sits
rem on PATH but fails as soon as it's actually run instead of launching real
rem Python. Confirm each candidate's --version actually succeeds too.
set "PYTHON="
where py >nul 2>&1
if not errorlevel 1 (
  py -3 --version >nul 2>&1
  if not errorlevel 1 set "PYTHON=py -3"
)
if not defined PYTHON (
  where python >nul 2>&1
  if not errorlevel 1 (
    python --version >nul 2>&1
    if not errorlevel 1 set "PYTHON=python"
  )
)

if not defined PYTHON (
  echo [WARN] Python 3 is not installed ^(needed to run the server-panel TUI^)
  echo        Install it from: https://www.python.org/downloads/
  set "PROBLEMS=1"
) else (
  for /f "delims=" %%v in ('!PYTHON! --version 2^>^&1') do echo [ok] Python found: %%v ^(!PYTHON!^)
)

rem --- Python dependencies --------------------------------------------------
if defined PYTHON (
  set "MISSING="
  for /f "delims=" %%m in ('!PYTHON! tui-panel\check_deps.py') do (
    if not defined MISSING (set "MISSING=%%m") else (set "MISSING=!MISSING!, %%m")
  )
  if defined MISSING (
    echo [WARN] Missing or outdated Python packages: !MISSING!
    echo        Install them with: !PYTHON! -m pip install -U -r tui-panel\requirements-panel.txt
    set "PROBLEMS=1"
  ) else (
    echo [ok] Python dependencies OK ^(textual, psutil, cryptography^)
  )
)

if "%PROBLEMS%"=="1" (
  rem Hand off to the winget installer rather than stopping at a shopping list.
  rem OWPENGRAM_PREREQS_TRIED bounds this to a single retry, so something that
  rem will not install ends in a message instead of a loop.
  if defined OWPENGRAM_PREREQS_TRIED (
    echo.
    echo [ERROR] prerequisites are still missing after the install attempt -- see the messages above
    pause
    exit /b 1
  )
  if not exist "scripts\install-prereqs.ps1" (
    echo.
    echo [ERROR] missing prerequisites above -- install them and re-run this script
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
  rem winget writes the new PATH to the registry, but this console still holds
  rem the one it started with -- reload it so the re-check below can see what
  rem was just installed instead of asking for a fresh terminal.
  for /f "usebackq delims=" %%p in (`powershell -NoProfile -Command "[Environment]::GetEnvironmentVariable('Path','Machine') + ';' + [Environment]::GetEnvironmentVariable('Path','User')"`) do set "PATH=%%p"
  set "OWPENGRAM_PREREQS_TRIED=1"
  echo.
  call "%~f0" %*
  exit /b !errorlevel!
)

echo.
echo [cfg] All prerequisites OK.
echo.
!PYTHON! tui-panel\server-panel.py %*

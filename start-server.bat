@echo off
setlocal enabledelayedexpansion
cd /d "%~dp0"

set "ENV_EXAMPLE=.env.example"
set "ENV_FILE=.env"
set "IP_FILE=.public_ip"
set "PREFIX_FILE=.link_prefix"
set "SECRETS_FILE=.secrets"
set "COMPOSE_FILE=deploy\docker-compose.yml"

set "NO_BUILD=false"
:parse_args
if "%~1"=="" goto done_args
if /i "%~1"=="--no-build" set "NO_BUILD=true"
shift
goto parse_args
:done_args

echo [cfg] script started, NO_BUILD=%NO_BUILD%

rem --- Public address (interactive) -----------------------------------------
set "DEFAULT_IP="
if exist "%IP_FILE%" (
  set /p DEFAULT_IP=<"%IP_FILE%"
  echo [cfg] loaded saved IP: !DEFAULT_IP!
)

if not defined DEFAULT_IP (
  echo [cfg] detecting public IP...
  for /f "usebackq delims=" %%i in (`powershell -NoProfile -Command "(Invoke-WebRequest -Uri 'https://api.ipify.org' -TimeoutSec 5 -UseBasicParsing).Content" 2^>nul`) do set "DEFAULT_IP=%%i"
  if defined DEFAULT_IP echo [cfg] detected IP: !DEFAULT_IP!
)

if defined DEFAULT_IP (
  set /p "PUBLIC_IP=Public server IP/host [!DEFAULT_IP!]: "
) else (
  set /p "PUBLIC_IP=Public server IP/host: "
)
if not defined PUBLIC_IP set "PUBLIC_IP=!DEFAULT_IP!"
if not defined PUBLIC_IP (
  echo [ERROR] public IP/host is required.
  pause
  exit /b 1
)
> "%IP_FILE%" echo !PUBLIC_IP!
echo [cfg] public address = !PUBLIC_IP!

rem --- Link prefix / me_url_prefix (interactive) ----------------------------
set "DEFAULT_PREFIX=!PUBLIC_IP!"
if exist "%PREFIX_FILE%" set /p DEFAULT_PREFIX=<"%PREFIX_FILE%"
set /p "LINK_PREFIX=Link prefix [!DEFAULT_PREFIX!]: "
if not defined LINK_PREFIX set "LINK_PREFIX=!DEFAULT_PREFIX!"
for /f "usebackq delims=" %%i in (`powershell -NoProfile -Command "$p='!LINK_PREFIX!'; $p=$p -replace '^https?://','' -replace '/+$',''; Write-Output $p"`) do set "LINK_PREFIX=%%i"
if not defined LINK_PREFIX (
  echo [ERROR] link prefix is required.
  pause
  exit /b 1
)
> "%PREFIX_FILE%" echo !LINK_PREFIX!
echo [cfg] link prefix = !LINK_PREFIX!

rem --- Secrets (generated once, cached) -------------------------------------
set "ADMIN_TOKEN="
set "ADMIN_PASSWORD="
set "SESSION_KEY="
set "SECRETS_CHANGED="
if exist "%SECRETS_FILE%" (
  echo [cfg] loading secrets from %SECRETS_FILE%
  for /f "usebackq tokens=1,* delims==" %%A in (`type "%SECRETS_FILE%"`) do (
    if "%%A"=="ADMIN_TOKEN" set "ADMIN_TOKEN=%%B"
    if "%%A"=="ADMIN_PASSWORD" set "ADMIN_PASSWORD=%%B"
    if "%%A"=="SESSION_KEY" set "SESSION_KEY=%%B"
  )
)
if not defined ADMIN_TOKEN (
  echo [cfg] generating ADMIN_TOKEN...
  for /f "usebackq delims=" %%i in (`powershell -NoProfile -Command "$bytes=New-Object byte[] 32; [System.Security.Cryptography.RandomNumberGenerator]::Fill($bytes); ($bytes | ForEach-Object { $_.ToString('x2') }) -join ''"`) do set "ADMIN_TOKEN=%%i"
  set "SECRETS_CHANGED=1"
)
if not defined ADMIN_PASSWORD (
  echo [cfg] generating ADMIN_PASSWORD...
  for /f "usebackq delims=" %%i in (`powershell -NoProfile -Command "$chars='abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789'; -join (1..24 | ForEach-Object { $chars[(Get-Random -Maximum $chars.Length)] })"`) do set "ADMIN_PASSWORD=%%i"
  set "SECRETS_CHANGED=1"
)
if not defined SESSION_KEY (
  echo [cfg] generating SESSION_KEY...
  for /f "usebackq delims=" %%i in (`powershell -NoProfile -Command "$chars='abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789'; -join (1..48 | ForEach-Object { $chars[(Get-Random -Maximum $chars.Length)] })"`) do set "SESSION_KEY=%%i"
  set "SECRETS_CHANGED=1"
)
if defined SECRETS_CHANGED (
  > "%SECRETS_FILE%" echo ADMIN_TOKEN=!ADMIN_TOKEN!
  >> "%SECRETS_FILE%" echo ADMIN_PASSWORD=!ADMIN_PASSWORD!
  >> "%SECRETS_FILE%" echo SESSION_KEY=!SESSION_KEY!
  echo [cfg] secrets written to %SECRETS_FILE%
) else (
  echo [cfg] secrets loaded from %SECRETS_FILE%
)

rem --- Generate .env from .env.example --------------------------------------
if not exist "%ENV_EXAMPLE%" (
  echo [ERROR] template file %ENV_EXAMPLE% not found
  pause
  exit /b 1
)
copy /Y "%ENV_EXAMPLE%" "%ENV_FILE%" >nul
echo [cfg] copied %ENV_EXAMPLE% to %ENV_FILE%

echo [cfg] patching .env values...
powershell -NoProfile -ExecutionPolicy Bypass -Command ^
  "$f='%ENV_FILE%'; " ^
  "$enc=New-Object System.Text.UTF8Encoding($false); " ^
  "$t=[System.IO.File]::ReadAllText($f,$enc); " ^
  "$map=[ordered]@{ " ^
  "  'TELESRV_ADVERTISE_IP'='!PUBLIC_IP!'; " ^
  "  'TELESRV_TURN_ADVERTISE_IP'='!PUBLIC_IP!'; " ^
  "  'TELESRV_SFU_ADVERTISE_IP'='!PUBLIC_IP!'; " ^
  "  'TELESRV_PUBLIC_BASE_URL'='https://!LINK_PREFIX!'; " ^
  "  'TELESRV_PASSKEY_RP_ID'='!LINK_PREFIX!'; " ^
  "  'TELESRV_PUBLIC_APP_SCHEME'='owpg'; " ^
  "  'TELESRV_PUBLIC_APP_NAME'='OwpenGram'; " ^
  "  'TELESRV_ADMIN_API_TOKEN'='!ADMIN_TOKEN!'; " ^
  "  'TELESRV_ADMIN_UI_PASSWORD'='!ADMIN_PASSWORD!'; " ^
  "  'TELESRV_ADMIN_SESSION_KEY'='!SESSION_KEY!'; " ^
  "  'TELESRV_ADMIN_UI_ADDR'='127.0.0.1:2600'; " ^
  "  'TELESRV_ADMIN_API_ADDR'='127.0.0.1:2399'; " ^
  "  'TELESRV_PUBLIC_LINK_WEB_ADDR'='127.0.0.1:2401' " ^
  "}; " ^
  "foreach ($k in $map.Keys) { " ^
  "  $pat='(?m)^' + [regex]::Escape($k) + '=.*$'; " ^
  "  $rep=$k + '=' + $map[$k]; " ^
  "  if ($t -match $pat) { $t=[regex]::Replace($t,$pat,$rep) } " ^
  "  else { $t+=\"`r`n\"+$rep } " ^
  "}; " ^
  "[System.IO.File]::WriteAllText($f,$t,$enc); " ^
  "Write-Output 'ok'"
if %ERRORLEVEL% neq 0 (
  echo [ERROR] failed to patch .env
  pause
  exit /b 1
)
echo [cfg] .env written

rem --- Start infrastructure (PostgreSQL + Redis) ----------------------------
echo.
echo == [1/4] Starting infrastructure (PostgreSQL + Redis) ==
docker compose -f "%COMPOSE_FILE%" up -d
if %ERRORLEVEL% neq 0 (
  echo [ERROR] docker compose failed
  pause
  exit /b 1
)

rem --- Wait for PostgreSQL --------------------------------------------------
echo.
echo == [2/4] Waiting for PostgreSQL ==
set /a "_pgw=0"
:wait_pg
docker exec telesrv-postgres pg_isready -U telesrv -d telesrv >nul 2>&1
if not errorlevel 1 goto pg_ready
set /a "_pgw+=1"
if !_pgw! gtr 30 (
  echo [ERROR] PostgreSQL not ready after 60s
  pause
  exit /b 1
)
echo [cfg] waiting for PostgreSQL... !_pgw!/30
timeout /t 2 >nul
goto wait_pg
:pg_ready
echo [ok] PostgreSQL is ready

rem --- Build ------------------------------------------------------------------
echo.
echo == [3/4] Building server binaries ==
if /i "%NO_BUILD%"=="true" (
  echo [cfg] skipping build (--no-build)
  if not exist "bin\telesrv.exe" (
    if not exist "bin\telesrv-admin.exe" (
      echo [ERROR] no binaries found in bin\ - run without --no-build first
      pause
      exit /b 1
    )
  )
) else (
  if not exist bin mkdir bin
  echo [cfg] building telesrv...
  go build -o bin\telesrv.exe .\cmd\telesrv
  if %ERRORLEVEL% neq 0 (
    echo [ERROR] failed to build telesrv
    pause
    exit /b 1
  )
  echo [cfg] building telesrv-admin...
  go build -o bin\telesrv-admin.exe .\cmd\telesrv-admin
  if %ERRORLEVEL% neq 0 (
    echo [ERROR] failed to build telesrv-admin
    pause
    exit /b 1
  )
  echo [ok] binaries built
)

rem --- Start servers ----------------------------------------------------------
echo.
echo == [4/4] Starting telesrv + telesrv-admin ==

start "telesrv" /B bin\telesrv.exe
echo [ok] telesrv started

start "telesrv-admin" /B bin\telesrv-admin.exe
echo [ok] telesrv-admin started

echo.
echo ============================================
echo  OwpenGram server is running
echo ============================================
echo.
echo  MTProto:   !PUBLIC_IP!:2398
echo  Admin UI:  http://127.0.0.1:2600
echo  Admin API: http://127.0.0.1:2399
echo.
echo  Admin login password: !ADMIN_PASSWORD!
echo.
echo  Ports to open in firewall:
echo    TCP 2398          - MTProto (login / chats / media)
echo    TCP 12400         - TURN/STUN control (calls)
echo    UDP 12500-12999   - TURN media relay (calls)
echo    UDP 12399         - SFU group calls
echo    TCP 2400          - RTMP livestream ingest
echo.
echo ============================================

exit /b 0

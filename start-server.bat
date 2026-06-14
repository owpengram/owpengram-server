@echo off
setlocal enabledelayedexpansion
cd /d "%~dp0"

set "BFF=teamgramd\etc2\bff.yaml"
set "IP_FILE=.public_ip"
set "SECRET_FILE=.turn_secret"
set "ENV_FILE=.env"

rem --- Public address (interactive) -------------------------------------
rem Public IP/host that remote clients use to reach this server. Baked into
rem the MTProto + VoIP/TURN config so chats AND calls work globally.
set "DEFAULT_IP="
if exist "%IP_FILE%" set /p DEFAULT_IP=<"%IP_FILE%"
if defined DEFAULT_IP (
  set /p "PUBLIC_IP=Public server IP/host [%DEFAULT_IP%]: "
) else (
  set /p "PUBLIC_IP=Public server IP/host: "
)
if not defined PUBLIC_IP set "PUBLIC_IP=%DEFAULT_IP%"
if not defined PUBLIC_IP (
  echo [ERROR] public IP/host is required.
  pause
  exit /b 1
)
> "%IP_FILE%" echo %PUBLIC_IP%

rem --- TURN secret (generated once, reused) -----------------------------
set "TURN_SECRET="
if exist "%SECRET_FILE%" set /p TURN_SECRET=<"%SECRET_FILE%"
if not defined TURN_SECRET (
  for /f "usebackq delims=" %%i in (`powershell -NoProfile -Command "[guid]::NewGuid().ToString('N')"`) do set "TURN_SECRET=%%i"
  > "%SECRET_FILE%" echo !TURN_SECRET!
)

rem --- compose env (consumed by the coturn service) ---------------------
> "%ENV_FILE%" echo PUBLIC_IP=%PUBLIC_IP%
>> "%ENV_FILE%" echo TURN_SECRET=!TURN_SECRET!

rem --- bake public address + TURN secret into the server config ---------
powershell -NoProfile -ExecutionPolicy Bypass -Command "$ip='%PUBLIC_IP%'; $sec='!TURN_SECRET!'; $f=(Resolve-Path '%BFF%').Path; $t=(Get-Content $f) -replace '^(\s*Ip:\s*).*$', ('${1}'+$ip) -replace '^(\s*Password:\s*).*$', ('${1}\"'+$sec+'\"'); $enc=New-Object System.Text.UTF8Encoding($false); [IO.File]::WriteAllLines($f,$t,$enc)"
if %ERRORLEVEL% neq 0 (
  echo [ERROR] failed to update %BFF%
  pause
  exit /b %ERRORLEVEL%
)
echo [cfg] public address = %PUBLIC_IP%; TURN relay configured.

rem --- Windows firewall (best-effort; requires admin) ------------------
for %%R in ("owpengram 10443" "owpengram turn 3478 udp" "owpengram turn 3478 tcp" "owpengram turn media") do netsh advfirewall firewall delete rule name=%%R >nul 2>&1
netsh advfirewall firewall add rule name="owpengram 10443" dir=in action=allow protocol=TCP localport=10443 >nul 2>&1
netsh advfirewall firewall add rule name="owpengram turn 3478 udp" dir=in action=allow protocol=UDP localport=3478 >nul 2>&1
netsh advfirewall firewall add rule name="owpengram turn 3478 tcp" dir=in action=allow protocol=TCP localport=3478 >nul 2>&1
netsh advfirewall firewall add rule name="owpengram turn media" dir=in action=allow protocol=UDP localport=49160-49200 >nul 2>&1

echo.
echo [1/2] docker compose -f docker-compose-env.yaml up -d
docker compose -f docker-compose-env.yaml up -d
if %ERRORLEVEL% neq 0 (
  echo [ERROR] env stack failed
  pause
  exit /b %ERRORLEVEL%
)

echo.
echo [2/3] docker compose up -d --build  (core server)
rem --build so the edited config (public address) is baked into the image.
docker compose up -d --build
if %ERRORLEVEL% neq 0 (
  echo [ERROR] app stack failed
  pause
  exit /b %ERRORLEVEL%
)

echo.
echo [3/3] coturn (calls relay) - best-effort, never blocks the core server
docker compose -f docker-compose-turn.yaml up -d
if %ERRORLEVEL% neq 0 (
  echo [WARN] coturn failed to start - calls relay unavailable; core server is fine.
)

echo.
echo [OK] Server started (public address: %PUBLIC_IP%).
echo      Also open these ports in the VPS PROVIDER firewall:
echo        TCP 10443        - MTProto (login / chats / media)
echo        UDP+TCP 3478     - TURN/STUN control (calls)
echo        UDP 49160-49200  - TURN media relay (calls)
exit /b 0

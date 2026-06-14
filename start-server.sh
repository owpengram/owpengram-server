#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"

BFF="teamgramd/etc2/bff.yaml"
IP_FILE=".public_ip"
SECRET_FILE=".turn_secret"
ENV_FILE=".env"

# --- Public address (interactive) ---------------------------------------
# Public IP/host that remote clients use to reach this server. Baked into the
# MTProto + VoIP/TURN config so chats AND calls work globally (not only LAN).
DEFAULT_IP=""
[[ -f "$IP_FILE" ]] && DEFAULT_IP="$(tr -d '[:space:]' < "$IP_FILE")"
[[ -z "$DEFAULT_IP" ]] && DEFAULT_IP="$(curl -fsS --max-time 3 https://api.ipify.org 2>/dev/null || true)"

if [[ -n "$DEFAULT_IP" ]]; then
  read -rp "Public server IP/host [${DEFAULT_IP}]: " PUBLIC_IP
else
  read -rp "Public server IP/host: " PUBLIC_IP
fi
PUBLIC_IP="${PUBLIC_IP:-$DEFAULT_IP}"
[[ -z "$PUBLIC_IP" ]] && { echo "[ERROR] public IP/host is required."; exit 1; }
echo "$PUBLIC_IP" > "$IP_FILE"

# --- TURN secret (generated once, reused) -------------------------------
TURN_SECRET=""
[[ -f "$SECRET_FILE" ]] && TURN_SECRET="$(tr -d '[:space:]' < "$SECRET_FILE")"
if [[ -z "$TURN_SECRET" ]]; then
  TURN_SECRET="$(openssl rand -hex 24 2>/dev/null || head -c 24 /dev/urandom | base64 | tr -dc 'a-zA-Z0-9')"
  echo "$TURN_SECRET" > "$SECRET_FILE"
fi

# --- compose env (consumed by the coturn service) -----------------------
{
  echo "PUBLIC_IP=${PUBLIC_IP}"
  echo "TURN_SECRET=${TURN_SECRET}"
} > "$ENV_FILE"

# --- bake public address + TURN secret into the server config -----------
sed -i -E "s|^([[:space:]]*Ip:[[:space:]]*).*$|\1${PUBLIC_IP}|" "$BFF"
sed -i -E "s|^([[:space:]]*Password:[[:space:]]*).*$|\1\"${TURN_SECRET}\"|" "$BFF"
echo "[cfg] public address = ${PUBLIC_IP}; TURN relay configured."

echo
echo "[1/2] docker compose -f docker-compose-env.yaml up -d"
docker compose -f docker-compose-env.yaml up -d

echo
echo "[2/3] docker compose up -d --build  (core server)"
# --build so the edited config (public address) is baked into the image.
docker compose up -d --build

echo
echo "[3/3] coturn (calls relay) — best-effort, never blocks the core server"
docker compose -f docker-compose-turn.yaml up -d \
  || echo "[WARN] coturn failed to start — calls relay unavailable; the core server is fine."

echo
echo "[OK] Server started (public address: ${PUBLIC_IP})."
echo "     Open these ports in the VPS PROVIDER firewall (and OS firewall):"
echo "       TCP 10443        - MTProto (login / chats / media)"
echo "       UDP+TCP 3478     - TURN/STUN control (calls)"
echo "       UDP 49160-49200  - TURN media relay (calls)"

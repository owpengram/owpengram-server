#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"

ENV_EXAMPLE=".env.example"
ENV_FILE=".env"
IP_FILE=".public_ip"
PREFIX_FILE=".link_prefix"
SECRETS_FILE=".secrets"
COMPOSE_FILE="deploy/docker-compose.yml"
LOG_DIR="logs"

NO_BUILD=false
for arg in "$@"; do
  case "$arg" in
    --no-build) NO_BUILD=true ;;
    -h|--help)
      echo "Usage: $0 [--no-build]"
      echo ""
      echo "  --no-build   Skip Go compilation (reuse existing binaries in bin/)"
      exit 0
      ;;
  esac
done

# --- Helpers ----------------------------------------------------------------
log()  { echo "[cfg] $*"; }
step() { echo; echo "== $* =="; }
die()  { echo "[ERROR] $*" >&2; exit 1; }

random_hex() {
  local bytes="${1:-32}"
  openssl rand -hex "$bytes" 2>/dev/null || head -c "$bytes" /dev/urandom | xxd -p | tr -d '\n' | head -c $((bytes * 2))
}

random_string() {
  local len="${1:-24}"
  openssl rand -base64 "$((len * 2))" 2>/dev/null | tr -dc 'a-zA-Z0-9' | head -c "$len" \
    || head -c 256 /dev/urandom | base64 | tr -dc 'a-zA-Z0-9' | head -c "$len"
}

# --- Public address (interactive) ------------------------------------------
DEFAULT_IP=""
[[ -f "$IP_FILE" ]] && DEFAULT_IP="$(tr -d '[:space:]' < "$IP_FILE")"
if [[ -z "$DEFAULT_IP" ]]; then
  DEFAULT_IP="$(curl -fsS --max-time 3 https://api.ipify.org 2>/dev/null || true)"
fi

if [[ -n "$DEFAULT_IP" ]]; then
  read -rp "Public server IP/host [${DEFAULT_IP}]: " PUBLIC_IP
else
  read -rp "Public server IP/host: " PUBLIC_IP
fi
PUBLIC_IP="${PUBLIC_IP:-$DEFAULT_IP}"
[[ -z "$PUBLIC_IP" ]] && die "public IP/host is required."
echo "$PUBLIC_IP" > "$IP_FILE"
log "public address = ${PUBLIC_IP}"

# --- Link prefix / me_url_prefix (interactive) -----------------------------
DEFAULT_PREFIX="$PUBLIC_IP"
[[ -f "$PREFIX_FILE" ]] && DEFAULT_PREFIX="$(tr -d '[:space:]' < "$PREFIX_FILE")"
read -rp "Link prefix (me_url_prefix, e.g. ${PUBLIC_IP} or chat.example.com) [${DEFAULT_PREFIX}]: " LINK_PREFIX
LINK_PREFIX="${LINK_PREFIX:-$DEFAULT_PREFIX}"
LINK_PREFIX="$(printf '%s' "$LINK_PREFIX" | sed -E 's#^https?://##; s#/+$##')"
[[ -z "$LINK_PREFIX" ]] && die "link prefix is required."
echo "$LINK_PREFIX" > "$PREFIX_FILE"
log "link prefix (me_url_prefix) = ${LINK_PREFIX}"

# --- Secrets (generated once, cached) --------------------------------------
load_or_gen_secrets() {
  local admin_token="" admin_password="" session_key=""

  if [[ -f "$SECRETS_FILE" ]]; then
    # shellcheck disable=SC1090
    source "$SECRETS_FILE" 2>/dev/null || true
  fi

  if [[ -z "${ADMIN_TOKEN:-}" ]]; then
    ADMIN_TOKEN="$(random_hex 32)"
  fi
  if [[ -z "${ADMIN_PASSWORD:-}" ]]; then
    ADMIN_PASSWORD="$(random_string 24)"
  fi
  if [[ -z "${SESSION_KEY:-}" ]]; then
    SESSION_KEY="$(random_string 48)"
  fi

  cat > "$SECRETS_FILE" <<EOF
ADMIN_TOKEN=${ADMIN_TOKEN}
ADMIN_PASSWORD=${ADMIN_PASSWORD}
SESSION_KEY=${SESSION_KEY}
EOF
  chmod 600 "$SECRETS_FILE"
  log "secrets loaded from ${SECRETS_FILE}"
}

load_or_gen_secrets

# --- Generate .env from .env.example --------------------------------------
if [[ ! -f "$ENV_EXAMPLE" ]]; then
  die "template file ${ENV_EXAMPLE} not found"
fi

cp "$ENV_EXAMPLE" "$ENV_FILE"

set_env() {
  local key="$1" value="$2"
  if grep -qE "^${key}=" "$ENV_FILE"; then
    sed -i -E "s|^${key}=.*|${key}=${value}|" "$ENV_FILE"
  else
    echo "${key}=${value}" >> "$ENV_FILE"
  fi
}

set_env "TELESRV_ADVERTISE_IP"           "$PUBLIC_IP"
set_env "TELESRV_TURN_ADVERTISE_IP"      "$PUBLIC_IP"
set_env "TELESRV_SFU_ADVERTISE_IP"       "$PUBLIC_IP"
set_env "TELESRV_PUBLIC_BASE_URL"        "https://${LINK_PREFIX}"
set_env "TELESRV_PASSKEY_RP_ID"          "$LINK_PREFIX"
set_env "TELESRV_PUBLIC_APP_SCHEME"      "owpg"
set_env "TELESRV_PUBLIC_APP_NAME"        "OwpenGram"
set_env "TELESRV_ADMIN_API_TOKEN"        "$ADMIN_TOKEN"
set_env "TELESRV_ADMIN_UI_PASSWORD"      "$ADMIN_PASSWORD"
set_env "TELESRV_ADMIN_SESSION_KEY"      "$SESSION_KEY"
set_env "TELESRV_ADMIN_UI_ADDR"          "127.0.0.1:2600"
set_env "TELESRV_ADMIN_API_ADDR"         "127.0.0.1:2399"
set_env "TELESRV_PUBLIC_LINK_WEB_ADDR"   "127.0.0.1:2401"

log ".env written (${ENV_FILE})"

# --- Start infrastructure (PostgreSQL + Redis) -----------------------------
step "[1/4] Starting infrastructure (PostgreSQL + Redis)"
docker compose -f "$COMPOSE_FILE" up -d

# --- Wait for PostgreSQL ---------------------------------------------------
step "[2/4] Waiting for PostgreSQL"
for i in $(seq 1 30); do
  if docker exec telesrv-postgres pg_isready -U telesrv -d telesrv >/dev/null 2>&1; then
    echo "[ok] PostgreSQL is ready"
    break
  fi
  if [ "$i" -eq 30 ]; then
    die "PostgreSQL not ready after 60s"
  fi
  sleep 2
done

# --- Build ------------------------------------------------------------------
step "[3/4] Building server binaries"
if [ "$NO_BUILD" = true ]; then
  log "skipping build (--no-build)"
  if [[ ! -f "bin/telesrv" ]] && [[ ! -f "bin/telesrv.exe" ]]; then
    die "no binaries found in bin/ — run without --no-build first"
  fi
else
  mkdir -p bin
  echo "  building telesrv ..."
  go build -o bin/telesrv ./cmd/telesrv
  echo "  building telesrv-admin ..."
  go build -o bin/telesrv-admin ./cmd/telesrv-admin
  echo "[ok] binaries built in bin/"
fi

# --- Start servers ----------------------------------------------------------
step "[4/4] Starting telesrv + telesrv-admin"

mkdir -p "$LOG_DIR"
TELESRV_LOG="$LOG_DIR/telesrv.log"
ADMIN_LOG="$LOG_DIR/telesrv-admin.log"

cleanup() {
  echo
  echo "[stop] stopping telesrv and telesrv-admin ..."
  kill "$TELESRV_PID" 2>/dev/null || true
  kill "$ADMIN_PID" 2>/dev/null || true
  wait "$TELESRV_PID" 2>/dev/null || true
  wait "$ADMIN_PID" 2>/dev/null || true
  echo "[ok] stopped."
}
trap cleanup EXIT INT TERM

# Start telesrv (main server)
BIN="./bin/telesrv"
[[ -f "bin/telesrv.exe" ]] && BIN="./bin/telesrv.exe"
$BIN >>"$TELESRV_LOG" 2>&1 &
TELESRV_PID=$!
echo "[ok] telesrv started (PID ${TELESRV_PID}), logs -> ${TELESRV_LOG}"

# Start telesrv-admin (admin panel)
ADMIN_BIN="./bin/telesrv-admin"
[[ -f "bin/telesrv-admin.exe" ]] && ADMIN_BIN="./bin/telesrv-admin.exe"
$ADMIN_BIN >>"$ADMIN_LOG" 2>&1 &
ADMIN_PID=$!
echo "[ok] telesrv-admin started (PID ${ADMIN_PID}), logs -> ${ADMIN_LOG}"

echo
echo "============================================"
echo " OwpenGram server is running"
echo "============================================"
echo ""
echo " MTProto:   ${PUBLIC_IP}:2398"
echo " Admin UI:  http://127.0.0.1:2600"
echo " Admin API: http://127.0.0.1:2399"
echo ""
echo " Admin login password: ${ADMIN_PASSWORD}"
echo ""
echo " Logs:"
echo "   telesrv:        tail -f ${TELESRV_LOG}"
echo "   telesrv-admin:  tail -f ${ADMIN_LOG}"
echo "============================================"

# --- Interactive menu -------------------------------------------------------
show_menu() {
  echo
  echo "  [1] View telesrv logs (last 50 lines)"
  echo "  [2] View telesrv-admin logs (last 50 lines)"
  echo "  [3] View both logs (last 50 lines)"
  echo "  [4] Tail telesrv logs (live)"
  echo "  [5] Tail telesrv-admin logs (live)"
  echo "  [q] Stop server and exit"
  echo
}

while true; do
  # Check if processes are still alive
  if ! kill -0 "$TELESRV_PID" 2>/dev/null; then
    echo "[WARN] telesrv (PID ${TELESRV_PID}) exited unexpectedly"
    echo "       Check ${TELESRV_LOG} for details"
    kill "$ADMIN_PID" 2>/dev/null || true
    break
  fi
  if ! kill -0 "$ADMIN_PID" 2>/dev/null; then
    echo "[WARN] telesrv-admin (PID ${ADMIN_PID}) exited unexpectedly"
    echo "       Check ${ADMIN_LOG} for details"
    kill "$TELESRV_PID" 2>/dev/null || true
    break
  fi

  show_menu
  read -rp "  Choice: " choice
  case "$choice" in
    1) tail -n 50 "$TELESRV_LOG" 2>/dev/null || echo "  (no logs yet)" ;;
    2) tail -n 50 "$ADMIN_LOG" 2>/dev/null || echo "  (no logs yet)" ;;
    3) echo "  --- telesrv ---" ; tail -n 50 "$TELESRV_LOG" 2>/dev/null || echo "  (no logs yet)"
       echo "  --- telesrv-admin ---" ; tail -n 50 "$ADMIN_LOG" 2>/dev/null || echo "  (no logs yet)" ;;
    4) echo "  Press Ctrl+C to stop tailing"; tail -f "$TELESRV_LOG" ;;
    5) echo "  Press Ctrl+C to stop tailing"; tail -f "$ADMIN_LOG" ;;
    q|Q) break ;;
    *) echo "  Invalid choice" ;;
  esac
done

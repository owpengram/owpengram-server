#!/usr/bin/env bash
# Builds and runs bin/telesrv-ctl (cmd/telesrv-ctl, wrapping
# internal/procctl.Manager) -- Go is the only prerequisite this checks for.
# Docker is optional and only checked informationally: absent,
# TELESRV_POSTGRES_DSN must already point at a reachable PostgreSQL instead.
# Python and tui-panel/server-panel.py remain available as a richer,
# optional interactive alternative (see README) -- nothing in this default
# path depends on them.
#
#   ./owpengram-macos.sh          bootstraps .env on a fresh install, starts
#                                  everything, prints the admin panel URL,
#                                  then (from a real terminal) drops into an
#                                  interactive menu for stop/restart/status/
#                                  update/logs/edition.
#   ./owpengram-macos.sh stop     stops both processes.
#   ./owpengram-macos.sh status   shows whether each process/container is up.
#   ./owpengram-macos.sh restart  rebuilds and relaunches both.
#   ./owpengram-macos.sh update   git pull --ff-only, then restart.
#   ./owpengram-macos.sh logs     prints the current run's startup log.
set -uo pipefail
cd "$(dirname "$0")"

ok()   { echo "[ok] $*"; }
info() { echo "[info] $*"; }
die()  { echo "[ERROR] $*" >&2; exit 1; }

echo "== Checking prerequisites =="

# --- Go (mandatory) ---------------------------------------------------------
if command -v go >/dev/null 2>&1; then
  ok "Go found: $(go version)"
else
  echo "[ERROR] Go is not installed (needed to build owpengram-server / owpengram-admin-panel)"
  echo "        Install it from: https://go.dev/dl/ (or brew install go)"
  if [[ -n "${OWPENGRAM_PREREQS_TRIED:-}" ]]; then
    die "Go is still missing after the install attempt -- see the messages above"
  fi
  if ! command -v brew >/dev/null 2>&1; then
    die "Homebrew is not installed. Install Homebrew (https://brew.sh/) or Go manually, then re-run this script."
  fi
  echo "== Installing Go via Homebrew =="
  if ! brew install go; then
    die "could not install Go -- see the messages above"
  fi
  echo
  OWPENGRAM_PREREQS_TRIED=1 exec "$0" "$@"
fi

# --- Docker (optional, informational only) ----------------------------------
if command -v docker >/dev/null 2>&1; then
  ok "Docker found."
else
  info "Docker not found -- PostgreSQL/MinIO won't be started automatically."
  echo "       Install Docker Desktop for Mac (https://docs.docker.com/desktop/install/mac-install/),"
  echo "       or point TELESRV_POSTGRES_DSN at an already-reachable PostgreSQL instance in .env."
fi

echo
echo "[cfg] Building telesrv-ctl..."
if ! go build -o bin/telesrv-ctl ./cmd/telesrv-ctl; then
  die "failed to build telesrv-ctl -- see the messages above"
fi

echo
exec bin/telesrv-ctl "$@"

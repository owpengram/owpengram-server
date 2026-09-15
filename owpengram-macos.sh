#!/usr/bin/env bash
# Checks prerequisites (Go, Python 3, and its packages), then hands off to
# tui-panel/server-panel.py. Run this instead of that script directly so
# missing prerequisites get a clear message instead of a Python traceback.
#
#   ./owpengram-macos.sh         bootstraps .env on a fresh install, starts
#                                  everything, prints the admin panel URL,
#                                  and exits -- no prompts. First-time setup
#                                  (branding, SMTP, the admin password) then
#                                  happens in that web panel, not here.
#   ./owpengram-macos.sh panel   the interactive TUI instead -- stop/
#                                  restart/logs/.env editing from a menu.
set -uo pipefail
cd "$(dirname "$0")"

ok()   { echo "[ok] $*"; }
warn() { echo "[WARN] $*"; }
die()  { echo "[ERROR] $*" >&2; exit 1; }

echo "== Checking prerequisites =="

PROBLEMS=0

# --- Go -----------------------------------------------------------------
if command -v go >/dev/null 2>&1; then
  ok "Go found: $(go version)"
else
  warn "Go is not installed (needed to build owpengram-server / owpengram-admin-panel)"
  echo "       Install it from: https://go.dev/dl/"
  PROBLEMS=1
fi

# --- Python ---------------------------------------------------------------
PYTHON=""
PYTHON_VERSION=""
if [[ -x ".venv/bin/python" ]]; then
  if VER_OUT="$(.venv/bin/python --version 2>&1)" && [[ "$VER_OUT" == Python\ 3* ]]; then
    PYTHON=".venv/bin/python"
    PYTHON_VERSION="$VER_OUT"
  fi
fi
if [[ -z "$PYTHON" ]]; then
  for cand in python3 python; do
    if command -v "$cand" >/dev/null 2>&1; then
      if VER_OUT="$("$cand" --version 2>&1)" && [[ "$VER_OUT" == Python\ 3* ]]; then
        PYTHON="$cand"
        PYTHON_VERSION="$VER_OUT"
        break
      fi
    fi
  done
fi

if [[ -z "$PYTHON" ]]; then
  warn "Python 3 is not installed (needed to run the server-panel TUI)"
  echo "       Install it from: https://www.python.org/downloads/ (or brew install python)"
  PROBLEMS=1
else
  ok "Python found: ${PYTHON_VERSION} (${PYTHON})"
fi

# --- Python dependencies ----------------------------------------------------
if [[ -n "$PYTHON" ]]; then
  MISSING="$("$PYTHON" tui-panel/check_deps.py)"
  if [[ -n "$MISSING" ]]; then
    warn "Missing or outdated Python packages: $(echo "$MISSING" | tr '\n' ' ')"
    PROBLEMS=1
  else
    ok "Python dependencies OK (textual, psutil, cryptography)"
  fi
fi

# --- Docker Desktop ---------------------------------------------------------
if ! command -v docker >/dev/null 2>&1; then
  warn "Docker is not installed (needed to run PostgreSQL, Redis and MinIO)"
  echo "       Install Docker Desktop for Mac: https://docs.docker.com/desktop/install/mac-install/"
  PROBLEMS=1
fi

if [[ "$PROBLEMS" -ne 0 ]]; then
  echo
  if [[ -n "${OWPENGRAM_PREREQS_TRIED:-}" ]]; then
    die "Prerequisites are still missing after the install attempt -- see the messages above."
  fi

  echo "== Installing missing prerequisites via Homebrew =="
  if ! command -v brew >/dev/null 2>&1; then
    die "Homebrew is not installed. Please install Homebrew (https://brew.sh/) or install the prerequisites manually."
  fi

  if ! command -v go >/dev/null 2>&1; then
    echo "[..] brew install go"
    brew install go
  fi

  if [[ -z "$PYTHON" ]]; then
    echo "[..] brew install python3"
    brew install python3
  fi

  # Attempt to create venv and install Python dependencies if needed
  if [[ -n "$PYTHON" || -x "$(command -v python3)" ]]; then
    ACTUAL_PYTHON="${PYTHON:-python3}"
    MISSING="$("$ACTUAL_PYTHON" tui-panel/check_deps.py 2>/dev/null || echo "missing")"
    if [[ -n "$MISSING" ]]; then
      echo "[..] Setting up Python virtual environment in .venv/"
      "$ACTUAL_PYTHON" -m venv .venv
      .venv/bin/pip install --quiet --upgrade pip
      .venv/bin/pip install --quiet -r tui-panel/requirements-panel.txt
    fi
  fi

  echo
  OWPENGRAM_PREREQS_TRIED=1 exec "$0" "$@"
fi

echo
echo "[cfg] All prerequisites OK."
echo
exec "$PYTHON" tui-panel/server-panel.py "$@"

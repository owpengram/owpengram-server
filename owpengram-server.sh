#!/usr/bin/env bash
# Checks prerequisites (Go, Python 3, and its packages), then hands off to
# tui-panel/server-panel.py. Run this instead of that script directly so
# missing prerequisites get a clear message instead of a Python traceback.
#
#   ./owpengram-server.sh         bootstraps .env on a fresh install, starts
#                                  everything, prints the admin panel URL,
#                                  and exits -- no prompts. First-time setup
#                                  (branding, SMTP, the admin password) then
#                                  happens in that web panel, not here.
#   ./owpengram-server.sh panel   the interactive TUI instead -- stop/
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
# Just checking `command -v` isn't enough: on Windows, `python3` (and
# sometimes `python`) can resolve to the Microsoft Store app-execution-alias
# stub, which exists on PATH but fails as soon as it's actually run instead
# of launching real Python. Validate the version output too.
PYTHON=""
PYTHON_VERSION=""
# A local .venv/ (created per this script's own advice below, to install
# tui-panel/requirements-panel.txt without touching a Debian/Ubuntu
# system Python) takes priority over PATH -- once it exists, every later run
# should keep using it instead of silently falling back to an older/
# unrelated system interpreter.
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
  echo "       Install it from: https://www.python.org/downloads/"
  PROBLEMS=1
else
  ok "Python found: ${PYTHON_VERSION} (${PYTHON})"
fi

# --- Python dependencies ----------------------------------------------------
if [[ -n "$PYTHON" ]]; then
  MISSING="$("$PYTHON" tui-panel/check_deps.py)"
  if [[ -n "$MISSING" ]]; then
    warn "Missing or outdated Python packages: $(echo "$MISSING" | tr '\n' ' ')"
    echo "       Install them with: $PYTHON -m pip install -U -r tui-panel/requirements-panel.txt"
    echo "       On Debian/Ubuntu this usually fails with 'externally-managed-environment'"
    echo "       against the system Python -- use a venv instead, then just re-run this script"
    echo "       (it prefers .venv/ automatically once one exists):"
    echo "         python3 -m venv .venv && .venv/bin/pip install -r tui-panel/requirements-panel.txt"
    PROBLEMS=1
  else
    ok "Python dependencies OK (textual, psutil, cryptography)"
  fi
fi

if [[ "$PROBLEMS" -ne 0 ]]; then
  echo
  # Hand off to the installer rather than stopping at a shopping list. It asks
  # for confirmation once, takes root once, and installs only what is missing.
  # The re-exec is what makes the freshly installed tools count: this shell
  # resolved `go`/`python3` before any of them existed. OWPENGRAM_PREREQS_TRIED
  # bounds it to a single retry, so a package that still will not install ends
  # in a message instead of a loop.
  if [[ -n "${OWPENGRAM_PREREQS_TRIED:-}" ]]; then
    die "prerequisites are still missing after the install attempt -- see the messages above"
  fi
  if [[ ! -x scripts/install-prereqs.sh ]]; then
    die "missing prerequisites above -- install them and re-run this script"
  fi
  echo "== Installing the missing prerequisites =="
  if ! scripts/install-prereqs.sh; then
    die "could not install the prerequisites -- see the messages above"
  fi
  # /etc/profile.d/go.sh only applies to shells started later, and the venv is
  # not on PATH at all; both are picked up by the re-exec below because the
  # re-check looks for .venv/bin/python first.
  [[ -d /usr/local/go/bin ]] && export PATH="$PATH:/usr/local/go/bin"
  echo
  OWPENGRAM_PREREQS_TRIED=1 exec "$0" "$@"
fi

# --- Docker group ------------------------------------------------------------
# usermod -aG docker only changes what a *new* login gets: the shell that just
# ran the installer still has the old group set, so the panel's first
# `docker compose up` dies with "permission denied while trying to connect to
# the docker API at unix:///var/run/docker.sock". sg re-enters this script with
# the group applied and needs no logout, so the install-then-start run works in
# one go. Matched on the exact symptom -- a daemon that is simply not running is
# a different problem and must not be swallowed here.
if [[ -z "${OWPENGRAM_SG_DOCKER:-}" && $EUID -ne 0 ]] && command -v docker >/dev/null 2>&1; then
  DOCKER_ERR="$(docker version 2>&1 >/dev/null || true)"
  if [[ "$DOCKER_ERR" == *"permission denied"* ]] &&
     getent group docker 2>/dev/null | grep -qE "[:,]${USER}(,|$)"; then
    # The marker travels inside the command rather than the environment: sg and
    # newgrp are setgid and may sanitise what they pass on, and losing it is the
    # one failure that would loop instead of stopping. It goes through env
    # because the newgrp branch below runs this after `exec`, and exec takes no
    # assignment prefix -- it would look for a command called
    # "OWPENGRAM_SG_DOCKER=1".
    RELAUNCH="$(printf '%q ' env OWPENGRAM_SG_DOCKER=1 "$0" "$@")"
    if command -v sg >/dev/null 2>&1; then
      echo "[..] Applying your new 'docker' group membership for this run"
      exec sg docker -c "$RELAUNCH"
    fi
    # Arch ships newgrp but not sg. newgrp takes no command: it execs a shell
    # that reads stdin, so the command has to arrive that way -- which leaves
    # stdin a pipe, and the panel is a full-screen TUI that needs a terminal.
    # Reopening /dev/tty inside hands it back a real one.
    if command -v newgrp >/dev/null 2>&1 && [[ -e /dev/tty ]]; then
      echo "[..] Applying your new 'docker' group membership for this run"
      exec newgrp docker <<< "exec ${RELAUNCH} < /dev/tty"
    fi
    echo
    die "you were added to the 'docker' group, but this shell still has the old one --
       log out and back in (or run 'newgrp docker'), then re-run this script"
  fi
fi

echo
echo "[cfg] All prerequisites OK."
echo
exec "$PYTHON" tui-panel/server-panel.py "$@"

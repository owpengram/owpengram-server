#!/usr/bin/env bash
# Builds and runs bin/telesrv-ctl (cmd/telesrv-ctl, wrapping
# internal/procctl.Manager) -- Go is the only prerequisite this checks for.
# Docker is optional and only checked informationally: absent,
# TELESRV_POSTGRES_DSN must already point at a reachable PostgreSQL instead.
# Python and tui-panel/server-panel.py remain available as a richer,
# optional interactive alternative (see README) -- nothing in this default
# path depends on them.
#
#   ./owpengram-server.sh          bootstraps .env on a fresh install, starts
#                                   everything, prints the admin panel URL,
#                                   and exits -- no prompts.
#   ./owpengram-server.sh stop     stops both processes.
#   ./owpengram-server.sh status   shows whether each process/container is up.
#   ./owpengram-server.sh restart  rebuilds and relaunches both.
#   ./owpengram-server.sh update   git pull --ff-only, then restart.
#   ./owpengram-server.sh logs     prints the current run's startup log.
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
  echo "        Install it from: https://go.dev/dl/"
  # Hand off to the installer rather than stopping at a shopping list.
  # OWPENGRAM_PREREQS_TRIED bounds it to a single retry, so a package that
  # still will not install ends in a message instead of a loop.
  if [[ -n "${OWPENGRAM_PREREQS_TRIED:-}" ]]; then
    die "Go is still missing after the install attempt -- see the messages above"
  fi
  if [[ ! -x scripts/install-prereqs.sh ]]; then
    die "install Go and re-run this script"
  fi
  echo "== Installing the missing prerequisites =="
  if ! scripts/install-prereqs.sh; then
    die "could not install the prerequisites -- see the messages above"
  fi
  [[ -d /usr/local/go/bin ]] && export PATH="$PATH:/usr/local/go/bin"
  echo
  OWPENGRAM_PREREQS_TRIED=1 exec "$0" "$@"
fi

# --- Docker (optional, informational only) ----------------------------------
if command -v docker >/dev/null 2>&1; then
  ok "Docker found."
else
  info "Docker not found -- PostgreSQL/MinIO won't be started automatically."
  echo "       Either install Docker, or point TELESRV_POSTGRES_DSN at an"
  echo "       already-reachable PostgreSQL instance in .env."
fi

# --- Docker group ------------------------------------------------------------
# usermod -aG docker only changes what a *new* login gets: the shell that just
# ran the installer still has the old group set, so the first
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
    # Absolute: this script cd'd to its own directory at the top, so a relative
    # $0 that carried a directory component no longer resolves from here.
    SELF="$PWD/$(basename "$0")"
    RELAUNCH="$(printf '%q ' env OWPENGRAM_SG_DOCKER=1 "$SELF" "$@")"
    if command -v sg >/dev/null 2>&1; then
      echo "[..] Applying your new 'docker' group membership for this run"
      exec sg docker -c "$RELAUNCH"
    fi
    # Arch ships newgrp but not sg. newgrp takes no command: it execs a shell
    # that reads stdin, so the command has to arrive that way -- which leaves
    # stdin a pipe, and telesrv-ctl start prints things worth seeing on a real
    # terminal. Reopening /dev/tty inside hands it back one.
    if command -v newgrp >/dev/null 2>&1 && [[ -e /dev/tty ]]; then
      echo "[..] Applying your new 'docker' group membership for this run"
      exec newgrp docker <<< "exec ${RELAUNCH} < /dev/tty"
    fi
    # Neither helper is guaranteed: Arch has no sg at all, Debian moved it into
    # util-linux-extra, and recent Ubuntu images were seen shipping neither it
    # nor newgrp. sudo is the one thing that is certainly here by now -- the
    # installer just used it to install Docker -- and `sudo -g` sets the group
    # directly, with no login shell and no extra package. Last rather than first
    # because it can prompt for a password, which sg and newgrp do not.
    if command -v sudo >/dev/null 2>&1; then
      echo "[..] Applying your new 'docker' group membership for this run"
      exec sudo -u "$USER" -g docker env OWPENGRAM_SG_DOCKER=1 \
        PATH="$PATH" "$SELF" "$@"
    fi
    # Nothing left to try: logging out is the one instruction that always works.
    echo
    if command -v newgrp >/dev/null 2>&1; then
      die "you were added to the 'docker' group, but this shell still has the old one --
       log out and back in (or run 'newgrp docker'), then re-run this script"
    fi
    die "you were added to the 'docker' group, but this shell still has the old one --
       log out and back in, then re-run this script"
  fi
fi

echo
echo "[cfg] Building telesrv-ctl..."
if ! go build -o bin/telesrv-ctl ./cmd/telesrv-ctl; then
  die "failed to build telesrv-ctl -- see the messages above"
fi

echo
exec bin/telesrv-ctl "$@"

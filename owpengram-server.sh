#!/usr/bin/env bash
# Builds and runs bin/telesrv-ctl (cmd/telesrv-ctl) -- Go is the only
# prerequisite. telesrv-ctl stays in the foreground and runs the server and
# its admin panel until it is stopped (Ctrl+C); everything else is set up in
# the admin panel, whose address it prints.
#
#   ./owpengram-server.sh           run the server and the admin panel.
#   ./owpengram-server.sh stop      stop a running one (from another terminal).
#   ./owpengram-server.sh status    show what is running.
#   ./owpengram-server.sh restart   restart the server and the admin panel.
#   ./owpengram-server.sh update    git pull, rebuild, restart.
#   ./owpengram-server.sh logs      print the current run's startup log.
set -uo pipefail
cd "$(dirname "$0")"

ok()  { echo "[ok] $*"; }
die() { echo "[ERROR] $*" >&2; exit 1; }

if command -v go >/dev/null 2>&1; then
  ok "Go found: $(go version)"
else
  echo "[ERROR] Go is not installed (needed to build owpengram-server / owpengram-admin-panel)"
  die "install Go from https://go.dev/dl/ and re-run this script"
fi

echo "[cfg] Building telesrv-ctl..."
if ! go build -o bin/telesrv-ctl ./cmd/telesrv-ctl; then
  die "failed to build telesrv-ctl -- see the messages above"
fi

echo
exec bin/telesrv-ctl "$@"

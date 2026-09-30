#!/usr/bin/env bash
# OwpenGram server -- prebuilt release archive launcher (Linux).
#
# Unlike owpengram-server.sh in a git clone, nothing here builds anything:
# bin/owpengram-server, bin/owpengram-admin-panel and owpengram-ctl are
# already compiled for this platform. This script just forwards to
# owpengram-ctl, which bootstraps .env on the very first run (generating
# admin credentials, asking once whether to use Docker for PostgreSQL/MinIO
# -- "standard" edition -- or run fully portable with an embedded PostgreSQL
# and no Docker at all -- "portable" edition) and otherwise starts/stops/
# restarts both processes.
#
#   ./start.sh           bootstrap + start, then (from a real terminal) an
#                        interactive menu for stop/restart/status/update/
#                        logs/edition.
#   ./start.sh stop
#   ./start.sh status
#   ./start.sh restart
#   ./start.sh logs
#
# "standard" edition still needs Docker installed separately (only the
# binaries are prebuilt here, not a container runtime). "portable" edition
# needs nothing else at all.
set -uo pipefail
cd "$(dirname "$0")"
chmod +x ./owpengram-ctl 2>/dev/null || true
exec ./owpengram-ctl "$@"

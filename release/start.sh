#!/usr/bin/env bash
# OwpenGram server -- prebuilt release archive launcher (Linux).
#
# Unlike owpengram-server.sh in a git clone, nothing here builds anything:
# bin/owpengram-server, bin/owpengram-admin-panel and owpengram-ctl are
# already compiled for this platform. owpengram-ctl stays in the foreground
# and runs the server and its admin panel until it is stopped (Ctrl+C). On the
# very first run it creates .env and prints the admin panel address and
# password; where the data lives (the built-in PostgreSQL or your own, local
# disk or S3) is chosen in the admin panel. Nothing else needs installing.
#
#   ./start.sh            run the server and the admin panel.
#   ./start.sh stop       stop a running one (from another terminal).
#   ./start.sh status
#   ./start.sh restart
#   ./start.sh logs
set -uo pipefail
cd "$(dirname "$0")"
chmod +x ./owpengram-ctl 2>/dev/null || true
exec ./owpengram-ctl "$@"

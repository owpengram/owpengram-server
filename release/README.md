# OwpenGram server -- prebuilt release

Downloaded, not `git clone`d -- this archive already has everything the
server needs to run.

## Quick start

**Linux:**

```bash
./start.sh
```

**Windows:** double-click `start.bat`, or from a terminal:

```bat
start.bat
```

The first run bootstraps `.env` (generates admin credentials) and asks you
to pick an edition:

- **portable (recommended)** -- embedded PostgreSQL, local disk for media
  storage. No Docker, no external database, nothing else to install. This
  is what "download and run" actually means; pick this unless you already
  run Docker infrastructure.
- **classic** -- PostgreSQL + MinIO via `deploy/docker-compose.yml`
  (included in this archive). Requires Docker installed separately.

Once it's up, the admin panel URL and generated password are printed to the
terminal -- open that URL to finish setup (branding, SMTP, etc.).

Run from a real terminal (not piped/redirected), `start.sh`/`start.bat`
then drops into an interactive menu -- Start, Stop, Restart, Status, Update,
Logs, Change edition -- so you don't need to remember any of the commands
below. Closing that window later stops responding to it, but does not stop
the server: it keeps running detached in the background regardless.

## Commands

Each of these also works as a one-shot, non-interactive call (e.g. from
another script), without the menu:

```
./start.sh status     (or: start.bat status)
./start.sh stop
./start.sh restart
./start.sh logs
```

## What's in here

```
bin/                       owpengram-server, owpengram-admin-panel
owpengram-ctl(.exe)        process control CLI -- start.sh/.bat just wraps it
start.sh / start.bat       the launcher you actually run
.env.example               config template (owpengram-ctl copies from this)
data/langpack/              language pack strings shipped with every install
data/sticker-seed/          default sticker packs seeded on first start
data/pgcache/                pre-bundled embedded-PostgreSQL binaries (portable
                            edition's first start needs no network access)
deploy/docker-compose.yml  PostgreSQL + MinIO, only used by "classic" edition
```

## Updating

This archive has no git history, so `owpengram-ctl update` doesn't apply
here -- download the next release archive instead, and copy your `.env` and
`data/` (except `data/pgcache`, `data/postgres`, `data/blobs` if you're on
"classic" edition with its own storage) into it.

## Full documentation

https://github.com/owpengram/owpengram-server

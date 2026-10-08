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

The program stays in the foreground and runs the server and its admin panel
until you stop it (Ctrl+C, or closing the window). The first run creates
`.env` (generating admin credentials) and prints the admin panel URL and
password -- open that URL: the admin panel walks you through the rest. The
first thing it asks is where to keep data:

- **Database** -- the built-in PostgreSQL (the default; nothing to install,
  it runs and stops with the server) or your own PostgreSQL, given as a
  connection string.
- **Media** -- local disk (a folder) or S3-compatible storage (AWS S3,
  MinIO, ...).

Everything is checked before it is saved, then the server starts. After that
come the server's name, network address, bot API and your own admin account.

Everything else -- changing those choices later, restarting the server,
updating -- is done in the admin panel too. The server stops when this
program does.

From another terminal these also work:

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
data/pgcache/                pre-bundled built-in PostgreSQL binaries (its first
                            start needs no network access)
deploy/docker-compose.yml  optional: PostgreSQL + MinIO in Docker, for anyone
                            who would rather run those themselves (see below)
```

## Updating

This archive has no git history, so `owpengram-ctl update` doesn't apply
here -- download the next release archive instead, and copy your `.env` and
`data/` (except `data/pgcache`) into it. Stop the old one first.

## Using your own PostgreSQL / S3 (Docker, for example)

The server never starts Docker. If you want PostgreSQL or MinIO in
containers, start them yourself (`deploy/docker-compose.yml` is an example:
`docker compose -f deploy/docker-compose.yml up -d`) and, in the admin
panel's setup, choose "My own PostgreSQL" / S3 and enter their addresses.
They must be running before the server is.

## Full documentation

https://github.com/owpengram/owpengram-server

# Local setup

This guide shows the shortest safe path for running gramsrv on a development
machine or a small test server.

## 1. Prepare local configuration

The repository intentionally tracks only `.env.example`. Your real `.env` is
ignored by Git and must not be committed.

Linux / macOS:

```bash
cp .env.example .env
${EDITOR:-nano} .env
```

Windows PowerShell:

```powershell
Copy-Item .env.example .env
notepad .env
```

If you prefer a different config filename, set `TELESRV_CONFIG` as a process
environment variable before starting the server.

## 2. Set the network values

Review at least these values in `.env`:

- `TELESRV_LISTEN` is the MTProto bind address. Use `0.0.0.0:2398` when
  external clients must connect to this host, or `127.0.0.1:2398` for
  same-machine testing only.
- `TELESRV_ADVERTISE_IP` must be a client-reachable IPv4 or IPv6 address, not a
  DNS name. Use `127.0.0.1` only when the patched client runs on the same
  machine. Use a LAN or public IP for phones, other computers, or remote tests.
- `TELESRV_PUBLIC_BASE_URL` and `TELESRV_PUBLIC_WEB_BASE_URL` are HTTP(S) URLs
  used in generated public links. Put hostnames here, not in
  `TELESRV_ADVERTISE_IP`.
- `TELESRV_DEV_AUTH_CODE=12345` is convenient for local development but must not
  be exposed as a production login code.

## 3. Start Postgres and Redis

The development compose file exposes Postgres on `127.0.0.1:5432` and Redis on
`127.0.0.1:6399`, matching the defaults in `.env.example`.

```bash
docker compose -f deploy/docker-compose.yml up -d
```

If you use external Postgres or Redis, update `TELESRV_POSTGRES_DSN` and
`TELESRV_REDIS_ADDR` in `.env`.

## 4. Build and run the server

Linux / macOS:

```bash
go build -o bin/gramsrv ./cmd/telesrv
./bin/gramsrv
```

Windows PowerShell:

```powershell
go build -o bin/gramsrv.exe ./cmd/telesrv
.\bin\gramsrv.exe
```

## 5. First-start checklist

After startup, confirm:

- migrations completed successfully;
- `data/server_rsa.pem` was created if it did not already exist;
- MTProto is listening on `TELESRV_LISTEN`;
- Postgres and Redis connections are healthy;
- patched clients use the matching DC address, port, and server RSA key.

For the complete configuration reference, see
[`docs/configuration.en.md`](configuration.en.md).

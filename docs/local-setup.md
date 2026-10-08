# Local setup

This guide shows the shortest safe path for running owpengram-server on a
development machine or a small test server.

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

## 3. Choose the database

Set `TELESRV_POSTGRES_MODE` in `.env`:

- `embedded` (the default for a new install) uses the built-in PostgreSQL.
  `telesrv-ctl` starts it before the server, so run the server through
  `./owpengram-server.sh` / `owpengram-server.bat` rather than directly.
- `external` uses a PostgreSQL you run, at `TELESRV_POSTGRES_DSN`. For a
  throwaway one in Docker, the example compose file exposes it on
  `127.0.0.1:5432` with the DSN from `.env.example`:

  ```bash
  docker compose -f deploy/docker-compose.yml up -d
  ```

  The server never starts it; keep it running.

## 4. Build and run the server

With the built-in PostgreSQL, use the launcher (it builds and runs everything):
`./owpengram-server.sh` or `.\owpengram-server.bat`. To run the server binary by
hand, use an external PostgreSQL:

Linux / macOS:

```bash
go build -o bin/owpengram-server ./cmd/telesrv
./bin/owpengram-server
```

Windows PowerShell:

```powershell
go build -o bin/owpengram-server.exe ./cmd/telesrv
.\bin\owpengram-server.exe
```

## 5. First-start checklist

After startup, confirm:

- migrations completed successfully;
- `data/server_rsa.pem` was created if it did not already exist;
- MTProto is listening on `TELESRV_LISTEN`;
- the Postgres connection is healthy;
- patched clients use the matching DC address, port, and server RSA key.

For the complete configuration reference, see
[`docs/configuration.en.md`](configuration.en.md).

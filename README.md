<p align="center">
  <img src="media/readme/owpengram_splash.png" alt="OwpenGram" width="440">
</p>

# 🚀 OwpenGram Server

**Your own private messaging server — self-hosted, protocol-compatible, fully yours.**

OwpenGram Server is an open-source, Telegram-compatible MTProto backend written
in Go. Run it on your own network for a private, closed setup, or on a VPS to
be reachable anywhere in the world. Your data, your keys, your rules — no
cloud, no lock-in, no censorship.

> 🔗 Implements **MTProto API layer 227**.

`gramsrv` is independent and unofficial. It is not affiliated with, endorsed by,
or sponsored by Telegram or the official Telegram team.

---

## ✨ Why OwpenGram?

- 🔒 **Private & self-hosted** — messages live on infrastructure you control.
- 🧩 **Telegram-compatible** — works with the OwpenGram Android and Desktop clients.
- 🌍 **Reachable anywhere** — host it globally, or keep it on your own network.
- 🛡️ **Censorship-resistant** — no central authority can shut you down.
- ⚙️ **Single binary** — one Go program prepares keys, runs migrations, serves
  MTProto, and dispatches updates and background workers.
- 🆓 **Free & open source** — Apache-2.0, audit and extend it freely.

## 🎯 What works today

- 💬 Private chats, groups, supergroups & channels
- 📞 Voice & group calls, live streams, SFU/TURN building blocks
- 🖼️ Media & files — photos, videos, documents, stickers, reactions
- 🤖 Bots and mini apps, with a minimal Bot API gateway
- 🌐 Message translation and AI-assisted compose
- 📇 Contacts, dialogs sync, chat folders, public link landing pages
- 🖥️ Admin API and web UI for operations

<details>
<summary><b>📋 Full feature checklist (click to expand)</b></summary>

| Status | Feature | What works today |
|---|---|---|
| ✅ | MTProto server edge | TCP transport, RSA key exchange, auth keys, encrypted sessions, salts, ack/resend, bad messages, RPC dispatch, and layer compatibility helpers. |
| ✅ | Login and accounts | Development login code, sign-in, sign-up, log-out, authorizations, account settings, SRP/password state, email/passkey-oriented paths. |
| ✅ | Users and contacts | User profiles, usernames, profile photos, contact import/search, blocked/privacy state, presence, and last-seen style status. |
| ✅ | Dialogs and sync | Dialog list, pinned dialogs, manual unread, folders/filters, drafts, read boundaries, durable updates, online fan-out, and offline difference recovery. |
| ✅ | Chatlists and public links | Chat folder sharing, exported chatlist invite links, join/import flows, revoked invite handling, public username landing pages, and shared public link landing pages. |
| ✅ | Private chats | Send, history, read receipts, edit, delete, forward, reply, rich entities, grouped/media messages, reactions, scheduled/TTL-oriented paths. |
| ✅ | Rich messages | Telegram Desktop rich text messages, rich content conversion, send/edit/scheduled flows, dialog/history projections, and memory/PostgreSQL persistence. |
| ✅ | AI compose and ChatBot | Input-box rewrite/polish, default and custom tones, addstyle previews, local and external provider chains, streamed `@ChatBot` draft replies, and Business AI reply hooks. |
| ✅ | Message translation | Telegram `messages.translateText`, provider-backed batch translation, peer language settings, per-account rate limits, and privacy-conscious logging defaults. |
| ✅ | Supergroups and channels | Create, join, leave, invite links, participants, admins, forum topics, linked discussion guests, history, send/edit/delete/read, reactions, public search, and previews. |
| ✅ | Media and files | Upload, download, local blob storage, photos, documents, thumbnails, canonical GIFv conversion, external media fetch, web page previews, map tile cache hooks, profile/channel photos. |
| ✅ | Stickers and reactions | Sticker/reaction catalog, seed support, saved GIFs, recent reactions, top reactions, default reactions, and moderation-oriented reaction paths. |
| ✅ | Gifts and stars | Star gifts and local stars ledger foundations for compatibility and future feature work. |
| ✅ | Bots and mini apps | Bot service foundations, callbacks, inline helpers, webview/mini-app paths, a minimal Bot API gateway for libraries such as `python-telegram-bot`, persistent `getUpdates` delivery, and demo tools. |
| ✅ | Calls and live streams | Private call signaling foundations, group call state, RTMP live streaming, scheduled video chats, channel `join_as`, SFU/TURN building blocks, liveness, and expiry workers. |
| ✅ | Admin and operations | Admin API/UI backend, PostgreSQL migrations, Redis volatile state, retention workers, pprof/debug hooks, and load-test helpers. |
| ✅ | Desktop, Android, iOS, and Web focus | Telegram Desktop is the primary target, with Android, iOS, and Web compatibility paths actively covered by the same server. |

Some items are compatibility-first or experimental, but they are real open
server code, not hidden product-only features.
</details>

## ⚡ Quick Start

Requirements:

- **Go 1.25+**
- **Docker** (or Docker Desktop), for PostgreSQL and Redis
- OpenSSL, to export the server's RSA public key for the client's "Add Server" dialog

**1. Clone the repository**

```bash
git clone https://github.com/owpengram/owpengram-server.git
cd owpengram-server
```

**2. Start the infrastructure** (PostgreSQL + Redis)

```powershell
docker compose -f deploy/docker-compose.yml up -d
```

**3. Build and run the server**

Windows (PowerShell):

```powershell
go build -o bin/gramsrv.exe ./cmd/telesrv
.\bin\gramsrv.exe
```

Linux / macOS:

```bash
go build -o bin/gramsrv ./cmd/telesrv
./bin/gramsrv
```

On first start, the server creates `data/server_rsa.pem`, applies database
migrations, seeds bundled language packs, prepares optional media resources,
starts MTProto on `0.0.0.0:2398`, and brings up the update/media/background
workers in the same process.

> **Default local login code:** `12345` — change it before any real use!

### ⚙️ Configuration

See the complete [English configuration reference](docs/configuration.en.md) or
the [Chinese configuration reference](docs/configuration.zh-CN.md).
`.env.example` is a copyable development template, not an exhaustive parameter
dictionary. Most commonly used variables:

| Variable | Default | Meaning |
|---|---:|---|
| `TELESRV_LISTEN` | `0.0.0.0:2398` | MTProto listen address |
| `TELESRV_ADVERTISE_IP` | `127.0.0.1` | client-reachable fallback IP for media and calls |
| `TELESRV_DC` | `2` | self-hosted DC id |
| `TELESRV_DEV_AUTH_CODE` | `12345` | fixed login code for local development |
| `TELESRV_AUTH_CODE_MAX_ATTEMPTS` | `5` | wrong-code attempts before the code hash is deleted |
| `TELESRV_POSTGRES_DSN` | local Compose DSN | PostgreSQL connection string |
| `TELESRV_REDIS_ADDR` | `127.0.0.1:6399` | Redis address |
| `TELESRV_BLOB_DIR` | `data/blobs` | local media blob directory |
| `TELESRV_PUBLIC_LINK_WEB_ADDR` | empty | optional public link landing listener, for example `127.0.0.1:2401` |
| `TELESRV_BOT_API_ADDR` | empty | optional HTTP Bot API gateway listen address, for example `127.0.0.1:8081` |
| `TELESRV_AI_ENABLED` | `true` | enable AI compose entry points |
| `TELESRV_TRANSLATION_ENABLED` | `true` | enable Telegram message translation RPCs |

Optional OpenAI-compatible, Kimi/Moonshot, Gemini, and Anthropic AI provider
variables, login email/SMTP settings, and Business AI settings are documented
in `.env.example` and the configuration reference.

## 🔌 Ports to open

When deploying on a public server, open the following according to the
features you enable.

**Minimal (chat only)**

| Port | Protocol | Purpose | Required |
|---|---|---|---|
| 2398 | TCP | MTProto main port; also handles WebSocket when `TELESRV_WEBSOCKET_ENABLE=true` | Yes |

**With admin backend**

| Port | Protocol | Purpose | Notes |
|---|---|---|---|
| 2399 | TCP | Admin REST API | Restrict to trusted IPs or put behind VPN |
| 2600 | TCP | Admin Web UI | Use Nginx/reverse proxy + HTTPS in production |

**Optional features**

| Port | Protocol | Purpose | When needed |
|---|---|---|---|
| 2400 | TCP | RTMP live stream ingest | Live streaming |
| 12399 | UDP | SFU/WebRTC conferencing | Voice/video group calls |
| 12400 | UDP | TURN/STUN server | P2P/call relay |
| 12500-12999 | UDP | TURN relay port range | TURN relay |
| configurable | TCP | Bot API | When `TELESRV_BOT_API_ADDR` is set |
| 2401 example | TCP | Public username/sticker/chatlist landing pages | When `TELESRV_PUBLIC_LINK_WEB_ADDR=127.0.0.1:2401` is set |

**Internal/debug (do not expose publicly)**

| Port | Default bind | Purpose |
|---|---|---|
| 6060 | `127.0.0.1:6060` | pprof debugging endpoint |
| 5432 | `127.0.0.1:5432` | PostgreSQL |
| 6399 | `127.0.0.1:6399` | Redis |

Make sure `TELESRV_LISTEN=0.0.0.0:2398` is set, and `TELESRV_ADVERTISE_IP`
points to your public IP so clients can connect.

## 🌐 Public link landing pages

The server can serve public landing pages for `/<username>`, profile avatars,
`/addstickers/<shortName>`, `/addemoji/<shortName>`, and `/addlist/<slug>`.

```env
TELESRV_PUBLIC_LINK_WEB_ADDR=127.0.0.1:2401
TELESRV_PUBLIC_BASE_URL=https://your-domain.example
TELESRV_PUBLIC_APP_SCHEME=yourapp
TELESRV_PUBLIC_WEB_BASE_URL=https://web.your-domain.example
TELESRV_PUBLIC_APP_NAME=YourApp
```

In production, keep `TELESRV_PUBLIC_LINK_WEB_ADDR` on loopback and reverse-proxy
the public routes to it with HTTPS.

## 📱 Connect a client

Use the OwpenGram clients, which have a built-in **Add Server** option on the
server-selection screen at login — no source patching or custom build needed:

- 🤖 [Android client](https://github.com/owpengram/owpengram-android-client)
- 💻 [Desktop client](https://github.com/owpengram/owpengram-desktop-client)

A stock Telegram client will not connect, since it only trusts Telegram's own
DC list and RSA keys.

**1. Export your server's public key**

After the server generates `data/server_rsa.pem`, export the matching public
key as PEM:

```powershell
openssl rsa -in data/server_rsa.pem -RSAPublicKey_out -out data/server_rsa.pub
```

**2. Add the server in the client**

On the login screen, open server selection → **Add Server**, and fill in:

- **Host** — your server's address (e.g. `192.168.1.50` or `chat.example.com`)
- **Port** — `2398` by default
- **Main data center** — the DC id from `TELESRV_DC` (`2` by default)
- **RSA Public Key** — paste the full contents of `data/server_rsa.pub`
  (the `-----BEGIN RSA PUBLIC KEY-----...` PEM block) into the key field

Current baseline: TL layer `227`.

## 🧪 Development: multi-device smoke test

Use separate client working directories so sessions do not share local `tdata`:

```powershell
$tdesktop = "C:\path\to\tdesktop\out\Debug\Telegram.exe"
Start-Process $tdesktop -ArgumentList @("-workdir", "$PWD\.tdata-alice")
Start-Process $tdesktop -ArgumentList @("-workdir", "$PWD\.tdata-bob")
```

Log in with different phone numbers — the local login code is `12345` unless
you changed `TELESRV_DEV_AUTH_CODE`. Recommended checks:

- Send private messages, stickers, media, replies, forwards, edits, deletes,
  and read receipts between two users.
- Keep one device online and restart another device to verify offline
  `updates.getDifference` recovery.
- Open the same account from multiple sessions and confirm current-session
  echoes are not duplicated while other online sessions receive updates.
- Check server logs for no new `NOT_IMPLEMENTED`, `Unhandled RPC`, `bad_msg`,
  panic, or internal errors.

## 📂 Repository layout

```text
cmd/telesrv/              server entrypoint
cmd/telesrv-admin/        admin backend and web UI
deploy/                   docker-compose, migrations, deploy helpers
data/                     bundled language packs and optional seed data
internal/mtprotoedge/     MTProto transport, auth key, session, ack/resend
internal/rpc/             TL router and client compatibility handlers
internal/app/             domain services
internal/domain/          protocol-independent domain models
internal/store/           memory/postgres/redis storage backends
internal/seed/            bundled seed catalog loaders
internal/sfu/             real-time SFU experiments
internal/turnsrv/         TURN/STUN building blocks
```

## 🤝 Contributing

This server gets better fastest with real usage and focused fixes:

- Telegram Desktop and Android compatibility reports with reproducible steps.
- RPC traces for startup, sync, chat, media, calls, bots, or edge cases.
- Focused fixes for implemented paths instead of broad rewrites.
- Tests for online/offline updates, multi-device sessions, read state, media,
  and channel behavior.
- Performance work on hot paths such as fan-out, pagination, storage queries,
  media upload/download, and connection handling.

If a change affects visible client behavior, please include the client
version/commit, the RPC path you tested, and whether server logs stayed free
of new `NOT_IMPLEMENTED`, `Unhandled RPC`, `bad_msg`, panic, or internal errors.

## 💬 Community

- 📢 Channel: [@owpengram](https://t.me/owpengram)
- 💬 Chat: [Join the discussion](https://t.me/+sVB6Ymv70jEwNTAy)

OwpenGram Server builds on the open-source
[gramsrv](https://github.com/iamxvbaba/gramsrv) project.

## 📄 License

[Apache License 2.0](LICENSE)

---

⭐ If OwpenGram is useful to you, a star on GitHub helps the project grow.

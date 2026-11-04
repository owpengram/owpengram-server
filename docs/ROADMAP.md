# OwpenGram roadmap

OwpenGram is an independent platform, not a copy of another server. Work falls into three tracks.
Telegram compatibility is one of them: it keeps stock and forked Telegram clients working, but it
is not the goal by itself.

## 1. Telegram compatibility

Generated from code: **[COMPATIBILITY.md](COMPATIBILITY.md)** (`go run ./cmd/tl-coverage`).

- What is served, what answers statically, what is missing, and what is not planned on purpose.
- Priorities and "not planned, because" live in [compat/decisions.yaml](compat/decisions.yaml).
- Regenerate it with every release; `go run ./cmd/tl-coverage -check` fails when it is stale.
- New Telegram Layers come straight from Telegram Desktop through
  [owpengram-td](https://github.com/owpengram/owpengram-td) (`cmd/layerimport`), not from other
  servers. Other implementations (gramsrv, testgram) are references to read, never sources to
  merge.

Rule of thumb: implement a method when real clients call it in normal use (P1) or a feature we
want depends on it (P2). Telegram-only infrastructure (Passport, SMS jobs, ads, Firebase, the
custodial wallet) stays out unless it becomes part of the platform.

## 2. Platform features (OwpenGram's own)

Things Telegram does not have, or that only make sense with many independent servers.

| Feature | State | Notes |
|---|---|---|
| Multi-server clients (one app, many servers) | ✅ shipped | Android/Desktop/Web, `owpg://addserver` links |
| Server identity (name, icon, description refresh) | ✅ shipped | cosmetic only, never host/port/key |
| Embedded web client on the server | ✅ shipped | same port as MTProto |
| One-tap account on a new server | 📝 planned | create from the existing profile (name, avatar, contact) + one code |
| Key fingerprint in `owpg://addserver` | 📝 planned | closes trust-on-first-use when a server is added |
| Federation between servers | 💡 idea | accounts stay separate; profile links, forwarding between servers |
| Signed server catalog | 💡 designed, deferred | Ed25519-signed list of public servers |
| Telegram backup channel | 💡 vision | keep talking to contacts already on OwpenGram when Telegram is down; opt-in only |
| Real TON / Fragment | 💡 idea | today Stars, gifts and auctions are local; on-chain is a separate layer |

## 3. Infrastructure

| Item | State | Notes |
|---|---|---|
| Offsite backups (Postgres + media) | 📝 planned | first step after the 2026-11-04 hosting outage |
| Website on static hosting | 📝 planned | owpengram.org must survive a server outage |
| Standby server at a second provider | 📝 planned | Postgres replication; auth keys live in the DB, sessions survive |
| Client fallback addresses | 📝 planned | server-info / addserver carry a list; clients rotate on failure |
| Push without Google (UnifiedPush) | 💡 later | only if background delivery becomes a real complaint |
| Release process | ✅ | archives per platform, `ON` / `ON.M` tags, update notices in the admin panel |

Legend: ✅ done · 📝 planned · 💡 idea / later.

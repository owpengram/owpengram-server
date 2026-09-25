# Crypto donations

Deposit crypto (native currency, or USDT/USDC) to a personal, permanent
address and have it automatically credited to your Stars balance once the
deposit reaches the chain's required confirmation depth. This is separate
from "support the project" (a single operator wallet shown on the site and
in the admin panel, not wired into any per-user crediting) -- this document
covers the per-user deposit-to-Stars pipeline only.

## Address model

Every EVM-compatible chain (Ethereum, Base, Polygon, BSC, Sepolia, Ganache,
...) shares the same secp256k1 curve and address derivation, so **one
address per user works, unmodified, on every enabled chain at once**. There
is never a per-chain address.

The server holds a single BIP39 mnemonic and derives each user's address at
`m/44'/60'/0'/0/{index}`, where `index` is a compact sequence value
(`donation_address_index_seq`), not the raw Telegram user ID -- user IDs
could someday exceed BIP32's unhardened `uint32` index range, and keeping
the two decoupled avoids ever having to think about that again.

## Custody

Full custodial: the server holds the mnemonic, encrypted at rest with
AES-256-GCM. **Works out of the box, no manual setup step:**

- The encryption key is a local file, `data/donation_wallet.key` by default
  (`TELESRV_DONATION_WALLET_KEY_PATH`), generated automatically the first
  time the server ever runs -- the exact same pattern
  `internal/mtprotoedge.LoadOrGenerateRSAKey` already uses for the server's
  MTProto RSA key. An operator who wants to supply their own key instead
  (e.g. from a secrets manager) can set `TELESRV_DONATION_WALLET_KEY` to a
  literal 64-hex-char value, which overrides the file entirely.
- The wallet mnemonic itself is generated automatically the first time
  `cmd/telesrv` starts against a database with no wallet row yet
  (`donationsService.EnsureWallet`, called right after construction iff
  `!Ready()`). The freshly generated recovery phrase is logged once, at
  `Warn` level, with an explicit "back this up now" message -- that log line
  is the only place it is ever shown. Every later restart loads the
  already-encrypted wallet from the database instead.

Losing the key file (and not having backed up the mnemonic from that first
log line) is exactly like losing a hardware wallet's seed phrase -- there is
no recovery path. Back up `data/donation_wallet.key` the same way you'd back
up `data/server_rsa.pem`.

**Deriving addresses and reading balances never touches the private key.**
The only code that can produce a private key at all is
`Wallet.PrivateKeyHex`, and nothing in the automatic watcher/crediting path
calls it. Sweeping funds out of a user's deposit address into an operator
treasury is a deliberately separate, manual, operator-triggered action --
not implemented yet, and never automatic by design (see "Not yet built"
below).

## Chains

`donation_chains` ships **empty**: migration
`20260925120000_donations_empty_defaults` deleted the original seed rows
(all except any that already had deposit history) once the admin panel could
add chains itself. Nothing is watched, and nothing is offered in
`/deposit`, until an operator adds a network on the Donations page -- either
from a curated preset (Ethereum, BSC, Base, Polygon, Arbitrum, Optimism,
Sepolia; chain id, native currency, a public RPC endpoint, the block
explorer and the price-source id pre-filled, all still editable) or from a
blank custom form. Ganache is deliberately **not** a preset: it is a local
dev node, and the tests that need one create their chain row themselves.

`donation_tokens` holds the stablecoin contracts per chain, also added by
hand from the same page. Contract addresses differ per network for the same
token, so nothing is ever guessed or pre-filled -- a wrong address would
mean watching the wrong contract. A token row with an empty
`contract_address` is listed but skipped by the watcher rather than watched
at the zero address.

## Watching

One goroutine per enabled, watchable chain (`Service.WatchChain`), polling
(no WebSocket subscription -- HTTP polling is simpler and works identically
against Ganache, Sepolia, and every future chain without needing `ws_url`
wired up):

1. **Native currency**: fetch each new block, scan every transaction's `to`
   address against known donation addresses.
2. **USDT/USDC**: `eth_getLogs` filtered to the token contract and the
   standard `Transfer(address,address,uint256)` topic, `to` = the address --
   a targeted, cheap query, not a full block scan.
3. Every still-pending deposit's confirmation count is refreshed against the
   chain's latest block each pass; once it reaches
   `donation_chains.confirmations_required` (per chain; the presets ship 10
   for testnets and 12 for mainnets) it flips to `confirmed`.
4. Confirmed deposits are priced and credited to Stars in the same pass.

A deposit is uniquely identified by `(chain_key, tx_hash, log_index)`
(`log_index = -1` for a native transfer, which has no log). Re-scanning a
block range that was already covered is a safe no-op: `RecordDonationDeposit`
is `ON CONFLICT DO NOTHING`, and `CreditDonationDeposit` only fires from a
`WHERE status = 'confirmed'` guard, so nothing can ever be credited twice --
proven by `internal/store/postgres/donations_ganache_integration_test.go`,
which sends a real 1 ETH transaction on a local Ganache node and asserts the
resulting Stars credit end to end.

## Pricing

Stablecoins are priced 1:1 to USD. Native currency uses
`donation_chains.manual_usd_rate_micros` (USD per one whole unit, x1e6),
which is either typed in by an operator or refreshed automatically:

- `price_source = 'coingecko'` plus a `price_source_id` (the coin's id on
  CoinGecko, e.g. `ethereum`, `binancecoin`,
  `polygon-ecosystem-token` -- note POL is *not* `matic-network` any more)
  makes `internal/app/donations/pricefeed.go` refresh that chain's rate
  every `TELESRV_DONATION_PRICE_REFRESH_INTERVAL` (default 10m) from the
  free, keyless `simple/price` endpoint. Every auto-priced chain is fetched
  in one request. A failed or missing refresh keeps the previous rate
  rather than zeroing it, and `price_updated_at` makes staleness visible in
  the panel.
- `price_source = 'manual'` (the default) is never touched by the
  refresher. Testnets belong here: Sepolia ETH has no market price, so the
  operator picks whatever rate makes testing meaningful.

Both are proven against the real API and real Postgres by
`TestCoinGeckoLivePrices` and `TestDonationsPriceRefreshWritesRate` (opt-in
with `TELESRV_TEST_LIVE_PRICES=1`, skipped offline) -- a coin id that
quietly stops resolving is otherwise invisible, since the refresher just
keeps the old rate forever.

USD converts to Stars at `TELESRV_STARS_USD_PRICE_MICROS`, default
`5000` = **$0.005 per Star** (200 Stars per dollar), roughly four times
cheaper than Telegram's own Star packs. Conversion rounds down -- a donor is
never credited more Stars than their deposit was actually worth. The same
price drives `/deposit`'s quoted rate and the admin panel's conversions, so
there is exactly one number to change.

## Notifications

Once the watcher credits a deposit, `donations.Service` calls its
`CreditNotifier` port (`app/bots.Service.NotifyDonationCredited`, wired via
`donationsService.SetNotifier(botsService)` in `cmd/telesrv/main.go`), which
sends the donor a `@premiumbot` chat message naming the amount, asset, chain
and Stars credited. Proven end to end (not mocked) by
`internal/store/postgres/donations_ganache_integration_test.go`, which
asserts the actual chat message lands after a real Ganache deposit.

`/deposit` answers with the address, then every enabled network as a
bulleted line naming its watched assets and what one whole unit of its
native currency is currently worth in Stars, then the headline rate -- so a
donor can see what they'll get before sending anything
(`TestPremiumBotDepositListsNetworksAndRate`).

All chat text from `@premiumbot` (the `/deposit` reply included) uses
`domain.MessageEntity` spans for formatting -- the server has no markdown
parser on the send path, so a literal backtick/asterisk in `botReply.Text`
is sent and shown verbatim rather than rendered.

## Admin panel

No chain ships pre-configured (see `20260925120000_donations_empty_defaults`
-- the migration that emptied the original seed rows once this page could
add its own). The Donations page (`permission: donations.manage`) shows
wallet provisioning status and address count, an **Add chain** menu (a
curated preset -- chain id, native currency and a public RPC endpoint
pre-filled, still fully editable -- or a blank custom form), every
configured chain with an editable RPC/WS endpoint, confirmation depth,
price source (manual rate or CoinGecko), a toggle switch, its live on-chain
balance (native + every watchable token, with a combined USD estimate --
`internal/app/donations.Service.ChainBalance`, one RPC round trip per
address per asset, so it's read fresh every time rather than cached or
derived from `donation_deposits`), a **Sweep** action per chain, and the
deposit ledger across every user. It never
exposes the wallet mnemonic or a private key -- no admin route returns
either; a sweep uses `Wallet.PrivateKeyHex` to sign in server memory only,
once per transfer, never over the wire. Writes go through
`internal/admin.Service` (`UpdateDonationChain`/`CreateDonationChain`/
`DeleteDonationChain`/`SweepDonationChain`; audit trail, actor/reason
required), the same pattern as Premium plan edits. Deleting a chain is
refused once it has real deposit history -- disable it instead.

Adding a chain, or flipping its Enabled toggle, starts (or stops) that
chain's watcher goroutine immediately -- `Service.StartWatchers` (called
once at server boot) remembers its own ctx/poll interval/logger, and
`CreateChain`/`UpdateChainConfig` call back into it (`ensureWatcher`/
`stopWatcher`). No restart is needed. Before this, a chain added while the
server was already running was silently never watched at all -- worth
knowing if you're chasing why a real, on-chain-confirmed deposit never got
credited on a chain that was added or re-enabled without a restart on an
older build. See `TestDonationsCreateChainStartsWatcherWithoutRestart`.

A chain enabled with `manual_usd_rate_micros` still at 0 (or too low to be
worth a single Star) has every deposit price to zero Stars, which the
watcher silently refuses to credit -- it sits at `confirmed` forever. Three
things guard that now: `CreateChain`/`UpdateChainConfig` refuse to enable a
chain without a positive rate; the admin form takes the price in **plain
dollars per whole coin** and shows the resulting "1 ETH = N Stars"
conversion, because the stored unit (micro-dollars) is impossible to enter
correctly by eye -- typing `20000` meaning $20,000 silently means $0.02;
and both the network card and any uncredited deposit row flag a price that
rounds ordinary deposits down to zero. Once the price is corrected, the
already-stuck `confirmed` deposit credits itself on the watcher's next
poll -- see `TestDonationsWatcherPicksUpRateFixWithoutRestart`.

Each network card carries its price source: **Manual** (a plain-dollar
rate) or **CoinGecko** (a coin id, with a **Check price** button that
fetches the live price into the form before saving, so a typo'd id is
caught immediately instead of silently never refreshing). Chain addresses
and every deposit's transaction hash link out to that chain's block
explorer (`explorer_url`, `domain.DonationChain.TxURL`/`AddressURL`), so
verifying a deposit on Etherscan is one click rather than a copy-paste.

Stablecoins are managed per network from the same page (**Tokens**):
symbol, contract address and decimals, through
`UpsertDonationToken`/`DeleteDonationToken`. A token row with no contract
address is listed but not watched. Contracts differ per network for the
same token, so they are never guessed or pre-filled.

### Sweep

`internal/app/donations/sweep.go` moves every deposit address's balance on
one chain -- native currency and any watchable token (USDT/USDC) -- to a
single operator-supplied destination. It is manual and operator-triggered
only; nothing in the automatic deposit-watching path ever calls it. A
dry-run (built into every admin action's confirm flow) previews exactly
what would move via `PreviewSweep`, signing and broadcasting nothing; only
confirming calls `Sweep`, which actually signs (deriving each address's key
on demand from the wallet mnemonic, never persisting it) and broadcasts.

Gas for a token transfer always comes out of that same address's own
native balance -- never the destination's, never another address's, never
auto-funded from anywhere -- so an address holding only a stablecoin and no
native currency for gas is reported as skipped, not silently dropped or
top-up-funded (auto-funding would move more money through more
transactions than the operator asked for). Tokens are swept before native
on each address, since a token transfer's gas is spent out of the native
balance the trailing native sweep would otherwise take all of. Proven
end to end against a real Ganache node in
`internal/store/postgres/donations_sweep_integration_test.go`.

## Not yet built

- **`@premiumbot` `/deposit` QR code.** The command answers with the raw
  address today; ideally it'd also send a QR image via `botReply.Media`.
- **"Support the project"**, the separate single-operator-wallet flow for
  the site and admin panel -- unrelated to per-user crediting, not started.
- **Reorg handling.** A deposit currently only moves forward
  (`pending → confirmed → credited`); there is no `orphaned` transition yet
  if a block gets reorganized out before reaching depth. Ganache/Sepolia
  testing at low confirmation counts makes this more likely to matter in
  practice than it will once mainnet chains (12+ confirmations) are enabled.

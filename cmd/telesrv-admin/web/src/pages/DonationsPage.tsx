import { AlertTriangle, Coins, ExternalLink, Loader2, Pencil, Plus, RefreshCw, Send, Trash2, X } from "lucide-react";
import { useState, useEffect } from "react";
import { createPortal } from "react-dom";
import { api, errorMessage } from "../api";
import { ActionButton } from "../components/ActionButton";
import { Alert, Badge, Metric, PageFrame } from "../components/ui";
import { DONATION_CHAIN_PRESETS, chainLogoColor, chainLogoShort, type DonationChainPreset } from "../donationChainPresets";
import type { DonationChain, DonationChainBalance, DonationDepositRow, DonationToken, DonationWalletStatus } from "../types";

// DEFAULT_STAR_PRICE_MICROS is only the fallback used before
// /api/donations/settings answers: the real price comes from the server
// (internal/app/donations/pricing.go), so this page can never quote a rate
// the crediting path disagrees with.
const DEFAULT_STAR_PRICE_MICROS = 5000;

// Rates are stored and sent as micro-dollars (1e6 = $1) because the server
// does all pricing in integers. Operators type plain dollars; these two
// helpers are the only place that distinction exists in the UI.
function microsToUsd(micros: number): string {
  if (!micros) return "";
  return String(Math.round(micros) / 1_000_000);
}

function usdToMicros(usd: string): number {
  const parsed = Number(usd);
  if (!isFinite(parsed) || parsed <= 0) return 0;
  return Math.round(parsed * 1_000_000);
}

// en-US explicitly: this panel is English-only, and a browser-locale
// number would render a dollar amount as "$2 400,00" next to English
// labels.
function formatUsd(micros: number): string {
  return `$${(micros / 1_000_000).toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

function starsPerWholeUnit(rateMicros: number, starPriceMicros: number): number {
  if (rateMicros <= 0 || starPriceMicros <= 0) return 0;
  return Math.floor(rateMicros / starPriceMicros);
}

function formatStarPrice(starPriceMicros: number): string {
  return `$${String(starPriceMicros / 1_000_000)}`;
}

function formatAge(iso: string): string {
  const t = Date.parse(iso);
  if (!isFinite(t) || t <= 0) return "never";
  const mins = Math.max(0, Math.round((Date.now() - t) / 60000));
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.round(mins / 60);
  if (hours < 48) return `${hours}h ago`;
  return `${Math.round(hours / 24)}d ago`;
}

function formatStars(stars: number): string {
  return `${stars.toLocaleString("en-US")} ${stars === 1 ? "Star" : "Stars"}`;
}

// LOW_RATE_STARS flags a price that is almost certainly a units mistake
// rather than a real coin price: below this, ordinary fractional deposits
// round down to zero Stars and are never credited, which is invisible
// until someone donates and nothing happens.
const LOW_RATE_STARS = 10;

function statusTone(status: DonationDepositRow["Status"]): "good" | "warn" | "danger" | "neutral" {
  if (status === "credited") return "good";
  if (status === "confirmed") return "warn";
  if (status === "orphaned") return "danger";
  return "neutral";
}

function formatAmount(amountRaw: string, decimals: number): string {
  if (decimals <= 0 || !amountRaw) return amountRaw;
  const neg = amountRaw.startsWith("-");
  let digits = neg ? amountRaw.slice(1) : amountRaw;
  while (digits.length <= decimals) digits = "0" + digits;
  const intPart = digits.slice(0, digits.length - decimals);
  const fracPart = digits.slice(digits.length - decimals).replace(/0+$/, "");
  const out = fracPart ? `${intPart}.${fracPart}` : intPart;
  return neg ? `-${out}` : out;
}

// estimateDeposit re-does the server's own pricing for a deposit that
// hasn't been credited yet, so a row showing $0.00 / 0 Stars explains
// itself instead of looking like a silent failure.
function estimateDeposit(deposit: DonationDepositRow, chain: DonationChain | undefined, starPriceMicros: number): { usdMicros: number; stars: number } {
  if (!chain) return { usdMicros: 0, stars: 0 };
  const isToken = deposit.TokenSymbol !== "";
  const decimals = isToken ? 6 : chain.NativeDecimals;
  const rateMicros = isToken ? 1_000_000 : chain.ManualUSDRateMicros;
  const amount = Number(deposit.AmountRaw) / Math.pow(10, decimals);
  const usdMicros = Math.floor(amount * rateMicros);
  return { usdMicros, stars: Math.floor(usdMicros / Math.max(1, starPriceMicros)) };
}

// ChainLogo is a colored badge standing in for a real brand logo (no bundled
// artwork): a preset chain gets its curated color, a custom one a neutral
// gray derived from its own name.
function ChainLogo({ chainKey, name, size = 34 }: { chainKey: string; name: string; size?: number }) {
  return (
    <span
      className="chain-logo"
      style={{ background: chainLogoColor(chainKey), width: size, height: size }}
      aria-hidden="true"
    >
      {chainLogoShort(chainKey, name)}
    </span>
  );
}

// Crypto donations: custodial-wallet status, per-chain watcher config, live
// on-chain balances, the deposit ledger, and manual sweeps. The wallet is
// auto-provisioned at server startup (docs/donations.md) -- nothing here
// creates or regenerates one, and the mnemonic/private keys are never
// exposed by any admin route; a sweep signs with them in server memory
// only, once per transfer.
export function DonationsPage() {
  const [wallet, setWallet] = useState<DonationWalletStatus | null>(null);
  const [chains, setChains] = useState<DonationChain[]>([]);
  const [tokens, setTokens] = useState<DonationToken[]>([]);
  const [deposits, setDeposits] = useState<DonationDepositRow[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [editingChain, setEditingChain] = useState<DonationChain | null>(null);
  const [tokensChain, setTokensChain] = useState<DonationChain | null>(null);
  const [sweepingChain, setSweepingChain] = useState<DonationChain | null>(null);
  const [addingChain, setAddingChain] = useState(false);
  const [balances, setBalances] = useState<Record<string, DonationChainBalance>>({});
  const [balanceErrors, setBalanceErrors] = useState<Record<string, string>>({});
  const [balancesLoading, setBalancesLoading] = useState<Set<string>>(new Set());
  const [starPriceMicros, setStarPriceMicros] = useState(DEFAULT_STAR_PRICE_MICROS);

  async function load() {
    setBusy(true);
    setError("");
    try {
      const [walletStatus, chainsResp, depositsResp] = await Promise.all([
        api.donationWalletStatus(),
        api.donationChains(),
        api.donationDeposits(new URLSearchParams({ limit: "100" }))
      ]);
      // Best-effort: an older server without the settings endpoint just
      // leaves the default in place rather than breaking the whole page.
      api.donationSettings()
        .then((settings) => setStarPriceMicros(settings.star_price_micros || DEFAULT_STAR_PRICE_MICROS))
        .catch(() => undefined);
      setWallet(walletStatus);
      setChains(chainsResp.chains);
      setTokens(chainsResp.tokens);
      setDeposits(depositsResp.rows);
      void loadBalances(chainsResp.chains);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  // loadBalances hits the live chain over RPC for each chain in parallel --
  // slower and less reliable than the rest of this page's plain Postgres
  // reads, so it never blocks the page and each chain's own failure (RPC
  // down, no RPC URL set) only affects that one card.
  async function loadBalances(chainList: DonationChain[]) {
    const withRPC = chainList.filter((c) => c.RPCURL.trim() !== "");
    setBalancesLoading(new Set(withRPC.map((c) => c.Key)));
    await Promise.all(withRPC.map(async (chain) => {
      try {
        const balance = await api.donationChainBalance(chain.Key);
        setBalances((prev) => ({ ...prev, [chain.Key]: balance }));
        setBalanceErrors((prev) => {
          if (!(chain.Key in prev)) return prev;
          const next = { ...prev };
          delete next[chain.Key];
          return next;
        });
      } catch (err) {
        setBalanceErrors((prev) => ({ ...prev, [chain.Key]: errorMessage(err) }));
      } finally {
        setBalancesLoading((prev) => {
          if (!prev.has(chain.Key)) return prev;
          const next = new Set(prev);
          next.delete(chain.Key);
          return next;
        });
      }
    }));
  }

  useEffect(() => { void load(); }, []);

  const totalHeldMicros = Object.values(balances).reduce((sum, b) => sum + b.TotalUSDValueMicros, 0);

  return (
    <PageFrame
      title={"Crypto donations"}
      eyebrow={"Networks, balances and payouts for /deposit donations"}
      actions={
        <>
          <button className="btn primary icon-text" type="button" onClick={() => setAddingChain(true)}>
            <Plus size={15} /> {"Add network"}
          </button>
          <button className="btn" type="button" onClick={() => void load()} disabled={busy}>
            {busy ? <Loader2 size={15} className="spin" /> : <RefreshCw size={15} />} {"Refresh"}
          </button>
        </>
      }
    >
      {error && <Alert>{error}</Alert>}

      <div className="metric-row">
        <Metric
          label={"Wallet"}
          value={wallet?.HasWallet ? "Provisioned" : "Not yet provisioned"}
          tone={wallet?.HasWallet ? "good" : "warn"}
        />
        <Metric label={"Deposit addresses"} value={String(wallet?.AddressCount ?? 0)} mono />
        <Metric label={"Held across networks"} value={formatUsd(totalHeldMicros)} mono />
        <Metric label={"Networks"} value={`${chains.filter((c) => c.Enabled).length} of ${chains.length} watching`} />
        <Metric label={"Star price"} value={`${formatStarPrice(starPriceMicros)} · ${formatStars(Math.floor(1_000_000 / Math.max(1, starPriceMicros)))} per $1`} />
      </div>
      {!wallet?.HasWallet && (
        <p className="muted">
          {"A wallet is generated automatically the next time the server starts -- there is no manual step, and no admin action creates one. The recovery phrase is logged once at that startup and never stored anywhere retrievable through this panel."}
        </p>
      )}

      <section className="section-block">
        <h2>{"Networks"}</h2>
        {chains.length === 0 && !busy && (
          <p className="muted">{"No networks yet. Add one to start accepting deposits -- pick a preset with a public RPC endpoint, or enter a custom chain."}</p>
        )}
        <div className="chain-card-grid">
          {chains.map((chain) => (
            <ChainCard
              key={chain.Key}
              chain={chain}
              tokens={tokens.filter((t) => t.ChainKey === chain.Key)}
              balance={balances[chain.Key]}
              balanceError={balanceErrors[chain.Key]}
              balanceLoading={balancesLoading.has(chain.Key)}
              starPriceMicros={starPriceMicros}
              onEdit={() => setEditingChain(chain)}
              onTokens={() => setTokensChain(chain)}
              onSweep={() => setSweepingChain(chain)}
              onChanged={() => void load()}
            />
          ))}
        </div>
      </section>

      <section className="section-block">
        <h2>{"Deposits"}</h2>
        <div className="table-wrap">
          <table className="data-table">
            <thead>
              <tr>
                <th>{"User"}</th>
                <th>{"Network"}</th>
                <th>{"Amount"}</th>
                <th>{"Value"}</th>
                <th>{"Stars"}</th>
                <th>{"Status"}</th>
                <th>{"Detected"}</th>
                <th>{"Tx"}</th>
              </tr>
            </thead>
            <tbody>
              {deposits.map((d) => {
                const chain = chains.find((c) => c.Key === d.ChainKey);
                const decimals = d.TokenSymbol ? 6 : (chain?.NativeDecimals ?? 0);
                const asset = d.TokenSymbol || chain?.NativeSymbol || "";
                const credited = d.Status === "credited";
                const estimate = credited ? null : estimateDeposit(d, chain, starPriceMicros);
                const wouldNotCredit = !credited && estimate !== null && estimate.stars <= 0;
                return (
                  <tr key={d.ID}>
                    <td>{d.UserFirstName || d.UserPhone || d.UserID}</td>
                    <td>{chain?.Name ?? d.ChainKey}</td>
                    <td className="mono">{formatAmount(d.AmountRaw, decimals)} {asset}</td>
                    <td className="mono">
                      {credited ? formatUsd(d.USDValueMicros) : (
                        <span className="muted" title={"Estimated at the network's current rate -- not yet credited"}>
                          {`≈ ${formatUsd(estimate?.usdMicros ?? 0)}`}
                        </span>
                      )}
                    </td>
                    <td className="mono">
                      {credited ? d.StarsCredited : (
                        <span className={wouldNotCredit ? "chain-warn" : "muted"}
                          title={wouldNotCredit
                            ? "Prices below one Star at this network's current rate, so the watcher will not credit it. Raise the network's USD rate and it credits on the next poll."
                            : "Estimated -- credits once it reaches the required confirmations"}>
                          {wouldNotCredit && <AlertTriangle size={12} />} {`≈ ${estimate?.stars ?? 0}`}
                        </span>
                      )}
                    </td>
                    <td><Badge tone={statusTone(d.Status)}>{d.Status}</Badge></td>
                    <td>{new Date(d.DetectedAt).toLocaleString()}</td>
                    <td className="mono">
                      {chain?.ExplorerURL
                        ? (
                          <a className="chain-link" href={`${chain.ExplorerURL.replace(/\/$/, "")}/tx/${d.TxHash}`}
                            target="_blank" rel="noreferrer noopener" title={d.TxHash}>
                            {`${d.TxHash.slice(0, 8)}…`} <ExternalLink size={11} />
                          </a>
                        )
                        : <span title={d.TxHash}>{`${d.TxHash.slice(0, 8)}…`}</span>}
                    </td>
                  </tr>
                );
              })}
              {deposits.length === 0 && !busy && (
                <tr><td colSpan={8} className="muted">{"No deposits yet."}</td></tr>
              )}
            </tbody>
          </table>
        </div>
      </section>

      {editingChain && (
        <ChainEditModal
          chain={editingChain}
          starPriceMicros={starPriceMicros}
          onClose={() => setEditingChain(null)}
          onSaved={() => { setEditingChain(null); void load(); }}
        />
      )}
      {tokensChain && (
        <ChainTokensModal
          chain={tokensChain}
          tokens={tokens.filter((t) => t.ChainKey === tokensChain.Key)}
          onClose={() => setTokensChain(null)}
          onChanged={() => void load()}
        />
      )}
      {sweepingChain && (
        <SweepChainModal chain={sweepingChain} onClose={() => setSweepingChain(null)} />
      )}
      {addingChain && (
        <AddChainModal
          existingKeys={new Set(chains.map((c) => c.Key))}
          starPriceMicros={starPriceMicros}
          onClose={() => setAddingChain(false)}
          onAdded={() => { setAddingChain(false); void load(); }}
        />
      )}
    </PageFrame>
  );
}

function ChainCard({
  chain, tokens, balance, balanceError, balanceLoading, starPriceMicros, onEdit, onTokens, onSweep, onChanged
}: {
  chain: DonationChain;
  tokens: DonationToken[];
  balance?: DonationChainBalance;
  balanceError?: string;
  balanceLoading: boolean;
  starPriceMicros: number;
  onEdit: () => void;
  onTokens: () => void;
  onSweep: () => void;
  onChanged: () => void;
}) {
  const noRPC = chain.RPCURL.trim() === "";
  const stars = starsPerWholeUnit(chain.ManualUSDRateMicros, starPriceMicros);
  const rateBroken = chain.Enabled && stars < LOW_RATE_STARS;
  const nonZeroAssets = (balance?.Assets ?? []).filter((a) => a.TotalRaw !== "0");

  return (
    <article className={`chain-card ${chain.Enabled ? "on" : ""}`}>
      <header className="chain-card-head">
        <ChainLogo chainKey={chain.Key} name={chain.Name} />
        <div className="chain-card-title">
          <strong>{chain.Name}</strong>
          <span className="muted mono">
            {`${chain.Key} · chain ${chain.ChainID} · ${chain.NativeSymbol}`}
            {chain.ExplorerURL !== "" && (
              <>
                {" · "}
                <a className="chain-link" href={chain.ExplorerURL} target="_blank" rel="noreferrer noopener">{"explorer"}</a>
              </>
            )}
          </span>
        </div>
        <ActionButton
          label={chain.Enabled ? "Disable" : "Enable"}
          path="/api/actions/donation-chain-update"
          payload={() => ({
            chain_key: chain.Key,
            rpc_url: chain.RPCURL,
            ws_url: chain.WSURL,
            enabled: !chain.Enabled,
            confirmations_required: chain.ConfirmationsRequired,
            price_feed_address: chain.PriceFeedAddress,
            manual_usd_rate_micros: chain.ManualUSDRateMicros
          })}
          onDone={onChanged}
          renderTrigger={(onClick) => (
            <button
              type="button"
              className={`chain-toggle ${chain.Enabled ? "on" : ""}`}
              role="switch"
              aria-checked={chain.Enabled}
              aria-label={chain.Enabled ? `Disable ${chain.Name}` : `Enable ${chain.Name}`}
              onClick={onClick}
            >
              <span />
            </button>
          )}
        />
      </header>

      <div className="chain-card-status">
        {chain.Enabled && !noRPC && <Badge tone="good">{"Watching"}</Badge>}
        {chain.Enabled && noRPC && <Badge tone="danger">{"No RPC endpoint"}</Badge>}
        {!chain.Enabled && <Badge tone="neutral">{"Disabled"}</Badge>}
        {rateBroken && (
          <span className="chain-warn" title={`At this price 1 ${chain.NativeSymbol} is only ${formatStars(stars)}, so ordinary fractional deposits round down to zero Stars and are never credited. Check the price -- it is entered in plain dollars per whole coin.`}>
            <AlertTriangle size={13} /> {stars <= 0 ? "Price too low — deposits won't credit" : `Price looks wrong — 1 ${chain.NativeSymbol} = ${formatStars(stars)}`}
          </span>
        )}
      </div>

      <div className="chain-card-stats">
        <div>
          <span className="muted">{"Balance"}</span>
          {balanceLoading && <strong><Loader2 size={14} className="spin" /></strong>}
          {!balanceLoading && balanceError && <strong className="muted" title={balanceError}>{"unavailable"}</strong>}
          {!balanceLoading && !balanceError && !balance && <strong className="muted">{"—"}</strong>}
          {!balanceLoading && !balanceError && balance && (
            <>
              <strong className="mono">{formatUsd(balance.TotalUSDValueMicros)}</strong>
              {nonZeroAssets.length === 0
                ? <span className="muted">{"empty"}</span>
                : nonZeroAssets.map((a) => (
                  <span key={a.Symbol} className="muted mono">{`${formatAmount(a.TotalRaw, a.Decimals)} ${a.Symbol}`}</span>
                ))}
            </>
          )}
        </div>
        <div>
          <span className="muted">
            {"Rate"}
            {chain.PriceSource === "coingecko" && chain.PriceSourceID !== "" && (
              <span className="rate-auto" title={`Refreshed automatically from CoinGecko (${chain.PriceSourceID}) -- last update ${formatAge(chain.PriceUpdatedAt)}`}>
                {`auto · ${formatAge(chain.PriceUpdatedAt)}`}
              </span>
            )}
          </span>
          <strong className="mono">{`${formatUsd(chain.ManualUSDRateMicros)} / ${chain.NativeSymbol}`}</strong>
          <span className="muted">{`1 ${chain.NativeSymbol} ≈ ${formatStars(stars)}`}</span>
        </div>
        <div>
          <span className="muted">{"Confirmations"}</span>
          <strong className="mono">{chain.ConfirmationsRequired}</strong>
          <span className="muted">{tokens.length === 0 ? "no tokens" : `${tokens.filter((t) => t.ContractAddress).length}/${tokens.length} tokens watched`}</span>
        </div>
      </div>

      {tokens.length > 0 && (
        <div className="chain-card-tokens">
          {tokens.map((t) => (
            <span key={t.Symbol} className={`token-chip ${t.ContractAddress ? "" : "pending"}`} title={t.ContractAddress || "No contract address set -- not watched"}>
              {t.Symbol}{t.ContractAddress ? "" : " ?"}
            </span>
          ))}
        </div>
      )}

      <footer className="chain-card-actions">
        <button className="btn compact-btn" type="button" onClick={onEdit}><Pencil size={13} /> {"Edit"}</button>
        <button className="btn compact-btn" type="button" onClick={onTokens}><Coins size={13} /> {"Tokens"}</button>
        <button className="btn compact-btn" type="button" onClick={onSweep}><Send size={13} /> {"Sweep"}</button>
        <ActionButton
          compact
          tone="danger"
          icon={<Trash2 size={13} />}
          label={"Delete"}
          path="/api/actions/donation-chain-delete"
          payload={() => ({ chain_key: chain.Key })}
          onDone={onChanged}
        />
      </footer>
    </article>
  );
}

// RateField is the one place an operator types a price: plain dollars per
// whole coin, with the Stars conversion spelled out underneath, because the
// stored unit (micro-dollars) is impossible to enter correctly by eye --
// typing "20000" meaning $20,000 silently means $0.02 and quietly breaks
// crediting for every deposit on that network.
function RateField({ symbol, usd, onChange, required, starPriceMicros, disabled }: {
  symbol: string; usd: string; onChange: (v: string) => void; required: boolean; starPriceMicros: number; disabled?: boolean;
}) {
  const micros = usdToMicros(usd);
  const stars = starsPerWholeUnit(micros, starPriceMicros);
  return (
    <>
      <label className="form-field wide">
        <span>{`USD price of 1 ${symbol || "coin"}`}</span>
        <div className="rate-input">
          <span className="rate-prefix">{"$"}</span>
          <input type="number" min="0" step="any" value={usd} placeholder={"2000"} disabled={disabled} onChange={(event) => onChange(event.target.value)} />
        </div>
      </label>
      {micros > 0 && (
        <p className={stars > 0 ? "muted" : "chain-warn"}>
          {stars > 0
            ? `1 ${symbol || "coin"} = ${formatStars(stars)} · 1 Star = ${formatStarPrice(starPriceMicros)}`
            : `At this price 1 ${symbol || "coin"} is worth less than a single Star, so deposits will never be credited.`}
        </p>
      )}
      {required && micros <= 0 && (
        <Alert>{"A price is required to enable this network: without it every deposit is worth $0 / 0 Stars and is never credited."}</Alert>
      )}
    </>
  );
}

// PriceSourceFields picks between a hand-typed rate and an automatic feed.
// The "Check" button resolves the coin id live, because a wrong id would
// otherwise fail only in the background refresher, leaving the network
// quietly priced off whatever number was last saved.
function PriceSourceFields({ source, sourceID, onSource, onSourceID, onFetched, symbol }: {
  source: string;
  sourceID: string;
  onSource: (v: string) => void;
  onSourceID: (v: string) => void;
  onFetched: (usdMicros: number) => void;
  symbol: string;
}) {
  const [checking, setChecking] = useState(false);
  const [checkResult, setCheckResult] = useState("");
  const [checkError, setCheckError] = useState("");

  async function check() {
    setChecking(true);
    setCheckError("");
    setCheckResult("");
    try {
      const preview = await api.donationPricePreview(sourceID.trim());
      setCheckResult(`${symbol || "coin"} = ${formatUsd(preview.usd_rate_micros)}`);
      onFetched(preview.usd_rate_micros);
    } catch (err) {
      setCheckError(errorMessage(err));
    } finally {
      setChecking(false);
    }
  }

  return (
    <>
      <div className="bot-create-fields">
        <label className="duration-field">
          <span>{"Price source"}</span>
          <select value={source} onChange={(event) => onSource(event.target.value)}>
            <option value="">{"Manual (I set it myself)"}</option>
            <option value="coingecko">{"CoinGecko (auto-refresh)"}</option>
          </select>
        </label>
        {source === "coingecko" && (
          <label className="duration-field">
            <span>{"CoinGecko coin id"}</span>
            <input value={sourceID} placeholder={"ethereum"} onChange={(event) => onSourceID(event.target.value.trim().toLowerCase())} />
          </label>
        )}
      </div>
      {source === "coingecko" && (
        <div className="price-check">
          <button className="btn compact-btn" type="button" disabled={checking || sourceID.trim() === ""} onClick={() => void check()}>
            {checking ? <Loader2 size={13} className="spin" /> : <RefreshCw size={13} />} {"Check price"}
          </button>
          {checkResult && <span className="muted mono">{`1 ${checkResult}`}</span>}
          {checkError && <span className="chain-warn">{checkError}</span>}
          <span className="muted">{"The id is CoinGecko's, e.g. ethereum, binancecoin, polygon-ecosystem-token."}</span>
        </div>
      )}
    </>
  );
}

function ChainEditModal({ chain, starPriceMicros, onClose, onSaved }: { chain: DonationChain; starPriceMicros: number; onClose: () => void; onSaved: () => void }) {
  const [rpcURL, setRpcURL] = useState(chain.RPCURL);
  const [wsURL, setWsURL] = useState(chain.WSURL);
  const [confirmations, setConfirmations] = useState(String(chain.ConfirmationsRequired));
  const [rateUsd, setRateUsd] = useState(microsToUsd(chain.ManualUSDRateMicros));
  const [priceFeed, setPriceFeed] = useState(chain.PriceFeedAddress);
  const [explorerURL, setExplorerURL] = useState(chain.ExplorerURL);
  const [priceSource, setPriceSource] = useState(chain.PriceSource);
  const [priceSourceID, setPriceSourceID] = useState(chain.PriceSourceID);
  const [enabled, setEnabled] = useState(chain.Enabled);

  const rateMicros = usdToMicros(rateUsd);
  const valid = rpcURL.trim() !== "" && Number(confirmations) > 0 && (!enabled || rateMicros > 0);

  return createPortal(
    <div className="modal-backdrop" role="presentation">
      <section className="modal command-modal" role="dialog" aria-modal="true" aria-label={`Edit ${chain.Name}`}>
        <div className="modal-head">
          <div className="chain-modal-title">
            <ChainLogo chainKey={chain.Key} name={chain.Name} size={28} />
            <div>
              <div className="eyebrow">{"Network"}</div>
              <h2>{chain.Name}</h2>
            </div>
          </div>
          <button className="icon-btn" type="button" onClick={onClose} aria-label={"Close"}><X size={15} /></button>
        </div>
        <div className="command-body">
          <p className="muted">{`Chain ID ${chain.ChainID} · ${chain.NativeSymbol} (${chain.NativeDecimals} decimals) -- fixed when the network was added.`}</p>
          <label className="form-field wide">
            <span>{"RPC URL"}</span>
            <input value={rpcURL} placeholder={"https://…"} onChange={(event) => setRpcURL(event.target.value)} />
          </label>
          <label className="form-field wide">
            <span>{"WS URL (optional)"}</span>
            <input value={wsURL} placeholder={"wss://…"} onChange={(event) => setWsURL(event.target.value)} />
          </label>
          <label className="form-field wide">
            <span>{"Block explorer URL (optional)"}</span>
            <input value={explorerURL} placeholder={"https://etherscan.io"} onChange={(event) => setExplorerURL(event.target.value)} />
          </label>
          <PriceSourceFields
            source={priceSource} sourceID={priceSourceID} symbol={chain.NativeSymbol}
            onSource={setPriceSource} onSourceID={setPriceSourceID}
            onFetched={(micros) => setRateUsd(microsToUsd(micros))}
          />
          <RateField symbol={chain.NativeSymbol} usd={rateUsd} onChange={setRateUsd} required={enabled} starPriceMicros={starPriceMicros} />
          <div className="bot-create-fields">
            <label className="duration-field">
              <span>{"Confirmations required"}</span>
              <input type="number" min="1" value={confirmations} onChange={(event) => setConfirmations(event.target.value)} />
            </label>
            <label className="duration-field">
              <span>{"Price feed address (optional)"}</span>
              <input value={priceFeed} placeholder={"0x…"} onChange={(event) => setPriceFeed(event.target.value)} />
            </label>
          </div>
          <label className="gift-switch">
            <input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} />
            <span className="gift-switch-track" aria-hidden="true"><span /></span>
            <span>{"Enabled"}</span>
          </label>
        </div>
        <div className="modal-actions">
          <button className="btn" type="button" onClick={onClose}>{"Close"}</button>
          <ActionButton
            label={"Save"}
            tone="neutral"
            path="/api/actions/donation-chain-update"
            disabled={!valid}
            payload={() => ({
              chain_key: chain.Key,
              rpc_url: rpcURL.trim(),
              ws_url: wsURL.trim(),
              enabled,
              confirmations_required: Number(confirmations),
              price_feed_address: priceFeed.trim(),
              manual_usd_rate_micros: rateMicros,
              explorer_url: explorerURL.trim(),
              price_source: priceSource,
              price_source_id: priceSource === "coingecko" ? priceSourceID.trim() : ""
            })}
            onDone={onSaved}
          />
        </div>
      </section>
    </div>,
    document.body
  );
}

// ChainTokensModal manages the stablecoin contracts a network watches.
// Without this the @premiumbot /deposit message promises USDT/USDC support
// that no operator could actually configure.
function ChainTokensModal({ chain, tokens, onClose, onChanged }: {
  chain: DonationChain;
  tokens: DonationToken[];
  onClose: () => void;
  onChanged: () => void;
}) {
  const [symbol, setSymbol] = useState("");
  const [contract, setContract] = useState("");
  const [decimals, setDecimals] = useState("6");
  const [drafts, setDrafts] = useState<Record<string, { contract: string; decimals: string }>>(
    Object.fromEntries(tokens.map((t) => [t.Symbol, { contract: t.ContractAddress, decimals: String(t.Decimals) }]))
  );

  const symbolValid = /^[A-Za-z][A-Za-z0-9]{1,11}$/.test(symbol.trim());
  const contractValid = contract.trim() === "" || /^0x[0-9a-fA-F]{40}$/.test(contract.trim());
  const canAdd = symbolValid && contractValid && Number(decimals) > 0 &&
    !tokens.some((t) => t.Symbol.toUpperCase() === symbol.trim().toUpperCase());

  return createPortal(
    <div className="modal-backdrop" role="presentation">
      <section className="modal command-modal" role="dialog" aria-modal="true" aria-label={`${chain.Name} tokens`}>
        <div className="modal-head">
          <div className="chain-modal-title">
            <ChainLogo chainKey={chain.Key} name={chain.Name} size={28} />
            <div>
              <div className="eyebrow">{"Tokens"}</div>
              <h2>{chain.Name}</h2>
            </div>
          </div>
          <button className="icon-btn" type="button" onClick={onClose} aria-label={"Close"}><X size={15} /></button>
        </div>
        <div className="command-body">
          <p className="muted">
            {"Stablecoins this network watches, priced 1:1 to USD. A token with no contract address is listed but not watched -- paste the contract from a block explorer for this exact network, since the same token has a different address on every chain."}
          </p>

          {tokens.length === 0 && <p className="muted">{"No tokens configured yet."}</p>}
          {tokens.map((token) => {
            const draft = drafts[token.Symbol] ?? { contract: token.ContractAddress, decimals: String(token.Decimals) };
            const draftValid = (draft.contract.trim() === "" || /^0x[0-9a-fA-F]{40}$/.test(draft.contract.trim())) && Number(draft.decimals) > 0;
            return (
              <div key={token.Symbol} className="token-row">
                <span className={`token-chip ${token.ContractAddress ? "" : "pending"}`}>{token.Symbol}</span>
                <input
                  className="small-input token-contract"
                  value={draft.contract}
                  placeholder={"0x… contract address"}
                  onChange={(event) => setDrafts((p) => ({ ...p, [token.Symbol]: { ...draft, contract: event.target.value } }))}
                />
                <input
                  className="small-input token-decimals"
                  type="number"
                  min="1"
                  value={draft.decimals}
                  onChange={(event) => setDrafts((p) => ({ ...p, [token.Symbol]: { ...draft, decimals: event.target.value } }))}
                />
                <ActionButton
                  compact
                  tone="neutral"
                  label={"Save"}
                  path="/api/actions/donation-token-upsert"
                  disabled={!draftValid}
                  payload={() => ({
                    chain_key: chain.Key,
                    symbol: token.Symbol,
                    contract_address: draft.contract.trim(),
                    decimals: Number(draft.decimals)
                  })}
                  onDone={onChanged}
                />
                <ActionButton
                  compact
                  tone="danger"
                  icon={<Trash2 size={13} />}
                  label={"Remove"}
                  path="/api/actions/donation-token-delete"
                  payload={() => ({ chain_key: chain.Key, symbol: token.Symbol })}
                  onDone={onChanged}
                />
              </div>
            );
          })}

          <h3 className="token-add-head">{"Add token"}</h3>
          <div className="bot-create-fields">
            <label className="duration-field">
              <span>{"Symbol"}</span>
              <input value={symbol} placeholder={"USDT"} onChange={(event) => setSymbol(event.target.value.toUpperCase())} />
            </label>
            <label className="duration-field">
              <span>{"Decimals"}</span>
              <input type="number" min="1" value={decimals} onChange={(event) => setDecimals(event.target.value)} />
            </label>
          </div>
          <label className="form-field wide">
            <span>{"Contract address"}</span>
            <input value={contract} placeholder={"0x… (leave empty to add it later)"} onChange={(event) => setContract(event.target.value)} />
          </label>
          {!contractValid && <Alert>{"That doesn't look like a valid 0x contract address."}</Alert>}
        </div>
        <div className="modal-actions">
          <button className="btn" type="button" onClick={onClose}>{"Close"}</button>
          <ActionButton
            label={"Add token"}
            icon={<Plus size={15} />}
            tone="primary"
            path="/api/actions/donation-token-upsert"
            disabled={!canAdd}
            payload={() => ({
              chain_key: chain.Key,
              symbol: symbol.trim().toUpperCase(),
              contract_address: contract.trim(),
              decimals: Number(decimals)
            })}
            onDone={() => { setSymbol(""); setContract(""); onChanged(); }}
          />
        </div>
      </section>
    </div>,
    document.body
  );
}

// SweepChainModal moves every deposit address's balance on this network
// (native currency and any watched token) to one operator-supplied
// destination. The dry-run step (built into ActionButton) previews exactly
// what would move -- signing and broadcasting nothing -- before the
// operator confirms the real, irreversible transfer.
function SweepChainModal({ chain, onClose }: { chain: DonationChain; onClose: () => void }) {
  const [destination, setDestination] = useState("");
  const validAddress = /^0x[0-9a-fA-F]{40}$/.test(destination.trim());

  return createPortal(
    <div className="modal-backdrop" role="presentation">
      <section className="modal command-modal" role="dialog" aria-modal="true" aria-label={`Sweep ${chain.Name}`}>
        <div className="modal-head">
          <div className="chain-modal-title">
            <ChainLogo chainKey={chain.Key} name={chain.Name} size={28} />
            <div>
              <div className="eyebrow">{"Payout"}</div>
              <h2>{`Sweep ${chain.Name}`}</h2>
            </div>
          </div>
          <button className="icon-btn" type="button" onClick={onClose} aria-label={"Close"}><X size={15} /></button>
        </div>
        <div className="command-body">
          <p>
            {"Moves the balance of every deposit address on this network -- native "}
            <strong>{chain.NativeSymbol}</strong>
            {" and any watched stablecoin -- to one destination address. Gas for each transfer comes out of that same address's own balance; an address without enough native currency to cover its own gas is reported as skipped, never auto-funded."}
          </p>
          <label className="form-field wide">
            <span>{"Destination address"}</span>
            <input value={destination} placeholder={"0x…"} onChange={(event) => setDestination(event.target.value)} />
          </label>
          {destination.trim() !== "" && !validAddress && <Alert>{"That doesn't look like a valid 0x address."}</Alert>}
        </div>
        <div className="modal-actions">
          <button className="btn" type="button" onClick={onClose}>{"Close"}</button>
          <ActionButton
            label={"Sweep"}
            tone="danger"
            icon={<Send size={15} />}
            path="/api/actions/donation-sweep"
            disabled={!validAddress}
            payload={() => ({ chain_key: chain.Key, destination: destination.trim() })}
          />
        </div>
      </section>
    </div>,
    document.body
  );
}

type AddChainMode = "presets" | "custom";

function AddChainModal({ existingKeys, starPriceMicros, onClose, onAdded }: { existingKeys: Set<string>; starPriceMicros: number; onClose: () => void; onAdded: () => void }) {
  const [mode, setMode] = useState<AddChainMode>("presets");
  const [selectedPreset, setSelectedPreset] = useState<DonationChainPreset | null>(null);
  const [key, setKey] = useState("");
  const [name, setName] = useState("");
  const [chainId, setChainId] = useState("");
  const [nativeSymbol, setNativeSymbol] = useState("");
  const [nativeDecimals, setNativeDecimals] = useState("18");
  const [rpcUrl, setRpcUrl] = useState("");
  const [wsUrl, setWsUrl] = useState("");
  const [confirmations, setConfirmations] = useState("12");
  const [rateUsd, setRateUsd] = useState("");
  const [explorerUrl, setExplorerUrl] = useState("");
  const [priceSource, setPriceSource] = useState("");
  const [priceSourceId, setPriceSourceId] = useState("");
  const [enabled, setEnabled] = useState(true);

  function pickPreset(preset: DonationChainPreset) {
    setSelectedPreset(preset);
    setKey(preset.key);
    setName(preset.name);
    setChainId(String(preset.chainId));
    setNativeSymbol(preset.nativeSymbol);
    setNativeDecimals(String(preset.nativeDecimals));
    setRpcUrl(preset.rpcUrl);
    setWsUrl(preset.wsUrl);
    setConfirmations(String(preset.confirmationsRequired));
    setExplorerUrl(preset.explorerUrl);
    // A preset with a real coin behind it starts on the automatic feed, so
    // the operator never has to hand-maintain a market price; a testnet
    // (no priceSourceId) stays manual.
    setPriceSource(preset.priceSourceId ? "coingecko" : "");
    setPriceSourceId(preset.priceSourceId);
  }

  function startCustom() {
    setMode("custom");
    setSelectedPreset(null);
    setKey(""); setName(""); setChainId(""); setNativeSymbol(""); setNativeDecimals("18");
    setRpcUrl(""); setWsUrl(""); setConfirmations("12"); setRateUsd("");
    setExplorerUrl(""); setPriceSource(""); setPriceSourceId("");
  }

  const reviewing = mode === "custom" || selectedPreset !== null;
  const keyTaken = existingKeys.has(key.trim().toLowerCase());
  const rateMicros = usdToMicros(rateUsd);
  const valid = reviewing && /^[a-z][a-z0-9_]{1,31}$/.test(key.trim().toLowerCase()) && !keyTaken &&
    name.trim() !== "" && Number(chainId) > 0 && nativeSymbol.trim() !== "" && Number(nativeDecimals) > 0 &&
    Number(confirmations) > 0 && rpcUrl.trim() !== "" && (!enabled || rateMicros > 0);

  return createPortal(
    <div className="modal-backdrop" role="presentation">
      <section className="modal command-modal" role="dialog" aria-modal="true" aria-label={"Add network"}>
        <div className="modal-head">
          <div>
            <div className="eyebrow">{"Networks"}</div>
            <h2>{"Add network"}</h2>
          </div>
          <button className="icon-btn" type="button" onClick={onClose} aria-label={"Close"}><X size={15} /></button>
        </div>
        <div className="command-body">
          {!reviewing && (
            <>
              <p className="muted">{"Pick a network to pre-fill its chain id, native currency and a public RPC endpoint -- everything stays editable before you add it."}</p>
              <div className="chain-preset-grid">
                {DONATION_CHAIN_PRESETS.map((preset) => {
                  const taken = existingKeys.has(preset.key);
                  return (
                    <button
                      key={preset.key}
                      type="button"
                      className="chain-preset-card"
                      disabled={taken}
                      onClick={() => pickPreset(preset)}
                      title={taken ? `${preset.name} is already configured` : `Add ${preset.name}`}
                    >
                      <span className="chain-logo" style={{ background: preset.color }}>{preset.short}</span>
                      <span className="chain-preset-name">{preset.name}</span>
                      <span className="muted mono">{`chain ${preset.chainId}`}</span>
                      {taken && <Badge tone="neutral">{"Added"}</Badge>}
                    </button>
                  );
                })}
              </div>
              <button className="btn" type="button" onClick={startCustom}>{"Or add a custom network…"}</button>
            </>
          )}
          {reviewing && (
            <>
              <p className="muted">
                {selectedPreset
                  ? `Review ${selectedPreset.name} before adding -- the suggested RPC endpoint is a public one, swap it for your own provider if you have it.`
                  : "Fill in every field for your custom EVM-compatible network."}
              </p>
              <div className="bot-create-fields">
                <label className="duration-field">
                  <span>{"Key (internal id)"}</span>
                  <input value={key} placeholder={"e.g. arbitrum"} disabled={!!selectedPreset} onChange={(event) => setKey(event.target.value.toLowerCase())} />
                </label>
                <label className="duration-field">
                  <span>{"Display name"}</span>
                  <input value={name} placeholder={"e.g. Arbitrum One"} onChange={(event) => setName(event.target.value)} />
                </label>
              </div>
              {keyTaken && <Alert>{"That key is already in use by another network."}</Alert>}
              <div className="bot-create-fields">
                <label className="duration-field">
                  <span>{"Chain ID"}</span>
                  <input type="number" value={chainId} onChange={(event) => setChainId(event.target.value)} />
                </label>
                <label className="duration-field">
                  <span>{"Native symbol"}</span>
                  <input value={nativeSymbol} placeholder={"ETH"} onChange={(event) => setNativeSymbol(event.target.value.toUpperCase())} />
                </label>
                <label className="duration-field">
                  <span>{"Native decimals"}</span>
                  <input type="number" value={nativeDecimals} onChange={(event) => setNativeDecimals(event.target.value)} />
                </label>
              </div>
              <label className="form-field wide">
                <span>{"RPC URL"}</span>
                <input value={rpcUrl} placeholder={"https://…"} onChange={(event) => setRpcUrl(event.target.value)} />
              </label>
              <label className="form-field wide">
                <span>{"WS URL (optional)"}</span>
                <input value={wsUrl} placeholder={"wss://…"} onChange={(event) => setWsUrl(event.target.value)} />
              </label>
              <label className="form-field wide">
                <span>{"Block explorer URL (optional)"}</span>
                <input value={explorerUrl} placeholder={"https://etherscan.io"} onChange={(event) => setExplorerUrl(event.target.value)} />
              </label>
              <PriceSourceFields
                source={priceSource} sourceID={priceSourceId} symbol={nativeSymbol}
                onSource={setPriceSource} onSourceID={setPriceSourceId}
                onFetched={(micros) => setRateUsd(microsToUsd(micros))}
              />
              <RateField symbol={nativeSymbol} usd={rateUsd} onChange={setRateUsd} required={enabled} starPriceMicros={starPriceMicros} />
              <label className="duration-field">
                <span>{"Confirmations required"}</span>
                <input type="number" min="1" value={confirmations} onChange={(event) => setConfirmations(event.target.value)} />
              </label>
              <label className="gift-switch">
                <input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} />
                <span className="gift-switch-track" aria-hidden="true"><span /></span>
                <span>{"Start watching immediately"}</span>
              </label>
              <button className="btn" type="button" onClick={() => { setMode("presets"); setSelectedPreset(null); }}>{"← Back"}</button>
            </>
          )}
        </div>
        <div className="modal-actions">
          <button className="btn" type="button" onClick={onClose}>{"Close"}</button>
          {reviewing && (
            <ActionButton
              label={"Add network"}
              icon={<Plus size={15} />}
              tone="primary"
              path="/api/actions/donation-chain-create"
              disabled={!valid}
              payload={() => ({
                chain_key: key.trim().toLowerCase(),
                name: name.trim(),
                chain_id: Number(chainId),
                native_symbol: nativeSymbol.trim(),
                native_decimals: Number(nativeDecimals),
                rpc_url: rpcUrl.trim(),
                ws_url: wsUrl.trim(),
                confirmations_required: Number(confirmations),
                price_feed_address: "",
                manual_usd_rate_micros: rateMicros,
                explorer_url: explorerUrl.trim(),
                price_source: priceSource,
                price_source_id: priceSource === "coingecko" ? priceSourceId.trim() : "",
                enabled
              })}
              onDone={onAdded}
            />
          )}
        </div>
      </section>
    </div>,
    document.body
  );
}

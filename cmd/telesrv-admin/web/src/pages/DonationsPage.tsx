import { AlertTriangle, Loader2, Pencil, Plus, RefreshCw, Send, Trash2, X } from "lucide-react";
import { useState, useEffect } from "react";
import { createPortal } from "react-dom";
import { api, errorMessage } from "../api";
import { ActionButton } from "../components/ActionButton";
import { Alert, Badge, EmptyRow, Metric, PageFrame } from "../components/ui";
import { DONATION_CHAIN_PRESETS, chainLogoColor, chainLogoShort, type DonationChainPreset } from "../donationChainPresets";
import type { DonationChain, DonationChainBalance, DonationDepositRow, DonationToken, DonationWalletStatus } from "../types";

type ChainDraft = {
  rpcURL: string;
  wsURL: string;
  enabled: boolean;
  confirmationsRequired: string;
  priceFeedAddress: string;
  manualUSDRateMicros: string;
};

function draftFromChain(chain: DonationChain): ChainDraft {
  return {
    rpcURL: chain.RPCURL,
    wsURL: chain.WSURL,
    enabled: chain.Enabled,
    confirmationsRequired: String(chain.ConfirmationsRequired),
    priceFeedAddress: chain.PriceFeedAddress,
    manualUSDRateMicros: String(chain.ManualUSDRateMicros)
  };
}

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

// ChainLogo is a colored badge standing in for a real brand logo (no bundled
// artwork): a preset chain gets its curated color, a custom one a neutral
// gray derived from its own name.
function ChainLogo({ chainKey, name }: { chainKey: string; name: string }) {
  return (
    <span
      className="chain-logo"
      style={{ background: chainLogoColor(chainKey) }}
      aria-hidden="true"
    >
      {chainLogoShort(chainKey, name)}
    </span>
  );
}

// Crypto donations: custodial-wallet status, per-chain watcher config (RPC/WS
// endpoint, enabled flag, confirmation depth, pricing), the deposit ledger
// across every user, and a manual sweep to withdraw accumulated funds. The
// wallet itself is auto-provisioned at server startup (see docs/donations.md)
// -- there is deliberately no button here to create or regenerate it, and
// the mnemonic/private keys are never exposed through this or any other
// admin route; a sweep signs with them in server memory only, once, per
// transfer.
export function DonationsPage() {
  const [wallet, setWallet] = useState<DonationWalletStatus | null>(null);
  const [chains, setChains] = useState<DonationChain[]>([]);
  const [tokens, setTokens] = useState<DonationToken[]>([]);
  const [deposits, setDeposits] = useState<DonationDepositRow[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [editingChain, setEditingChain] = useState<DonationChain | null>(null);
  const [sweepingChain, setSweepingChain] = useState<DonationChain | null>(null);
  const [addingChain, setAddingChain] = useState(false);
  const [balances, setBalances] = useState<Record<string, DonationChainBalance>>({});
  const [balanceErrors, setBalanceErrors] = useState<Record<string, string>>({});
  const [balancesLoading, setBalancesLoading] = useState<Set<string>>(new Set());

  async function load() {
    setBusy(true);
    setError("");
    try {
      const [walletStatus, chainsResp, depositsResp] = await Promise.all([
        api.donationWalletStatus(),
        api.donationChains(),
        api.donationDeposits(new URLSearchParams({ limit: "100" }))
      ]);
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
  // down, no RPC URL set) only affects that one chain's cell.
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

  return (
    <PageFrame
      title={"Crypto donations"}
      eyebrow={"Custodial wallet status, per-chain watcher config, the deposit ledger, and manual sweeps for /deposit donations"}
      actions={
        <>
          <button className="btn primary icon-text" type="button" onClick={() => setAddingChain(true)}>
            <Plus size={15} /> {"Add chain"}
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
      </div>
      {!wallet?.HasWallet && (
        <p className="muted">
          {"A wallet is generated automatically the next time the server starts -- there is no manual step, and no admin action creates one. The recovery phrase is logged once at that startup and never stored anywhere retrievable through this panel."}
        </p>
      )}

      <section className="section-block">
        <h2>{"Chains"}</h2>
        {chains.length === 0 && !busy && (
          <p className="muted">{"No chains configured yet. Click \"Add chain\" above to pick a preset or add a custom one."}</p>
        )}
        <div className="table-wrap">
          <table className="data-table">
            <thead>
              <tr>
                <th>{"Chain"}</th>
                <th>{"Network"}</th>
                <th>{"Status"}</th>
                <th>{"Tokens"}</th>
                <th>{"Balance"}</th>
                <th>{"Enabled"}</th>
                <th>{"Actions"}</th>
              </tr>
            </thead>
            <tbody>
              {chains.map((chain) => {
                const chainTokens = tokens.filter((t) => t.ChainKey === chain.Key);
                const misconfigured = chain.Enabled && !chain.RPCURL;
                return (
                  <tr key={chain.Key}>
                    <td>
                      <div className="chain-name-cell">
                        <ChainLogo chainKey={chain.Key} name={chain.Name} />
                        <div>
                          <strong>{chain.Name}</strong>
                          <div className="muted mono">{chain.Key}</div>
                        </div>
                      </div>
                    </td>
                    <td className="mono">{chain.NativeSymbol} · chain {chain.ChainID}</td>
                    <td>
                      {misconfigured && (
                        <div className="muted" title={"Enabled but no RPC URL set -- the watcher can't actually connect yet"}>
                          <AlertTriangle size={12} /> {"No RPC set"}
                        </div>
                      )}
                      {!misconfigured && !chain.Enabled && <span className="muted">{"—"}</span>}
                      {!misconfigured && chain.Enabled && <Badge tone="good">{"Watching"}</Badge>}
                    </td>
                    <td>
                      {chainTokens.length === 0
                        ? <span className="muted">{"none"}</span>
                        : chainTokens.map((t) => (
                          <div key={t.Symbol} className="mono">
                            {t.Symbol} {t.ContractAddress ? "" : <span className="muted">{"(not set)"}</span>}
                          </div>
                        ))}
                    </td>
                    <td>
                      {balancesLoading.has(chain.Key) && <Loader2 size={14} className="spin" />}
                      {!balancesLoading.has(chain.Key) && balanceErrors[chain.Key] && (
                        <span className="muted" title={balanceErrors[chain.Key]}>{"unavailable"}</span>
                      )}
                      {!balancesLoading.has(chain.Key) && !balanceErrors[chain.Key] && chain.RPCURL.trim() === "" && (
                        <span className="muted">{"no RPC set"}</span>
                      )}
                      {!balancesLoading.has(chain.Key) && !balanceErrors[chain.Key] && balances[chain.Key] && (
                        <div className="chain-balance">
                          <strong className="mono">{`$${(balances[chain.Key].TotalUSDValueMicros / 1_000_000).toFixed(2)}`}</strong>
                          {balances[chain.Key].Assets.filter((a) => a.TotalRaw !== "0").map((a) => (
                            <div key={a.Symbol} className="muted mono">{`${formatAmount(a.TotalRaw, a.Decimals)} ${a.Symbol}`}</div>
                          ))}
                          {balances[chain.Key].Assets.every((a) => a.TotalRaw === "0") && <span className="muted">{"empty"}</span>}
                        </div>
                      )}
                    </td>
                    <td>
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
                        onDone={() => void load()}
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
                    </td>
                    <td>
                      <div className="gift-table-actions">
                        <button className="btn compact-btn" type="button" onClick={() => setEditingChain(chain)}>
                          <Pencil size={13} /> {"Edit"}
                        </button>
                        <button className="btn compact-btn" type="button" onClick={() => setSweepingChain(chain)}>
                          <Send size={13} /> {"Sweep"}
                        </button>
                        <ActionButton
                          compact
                          tone="danger"
                          icon={<Trash2 size={13} />}
                          label={"Delete"}
                          path="/api/actions/donation-chain-delete"
                          payload={() => ({ chain_key: chain.Key })}
                          onDone={() => void load()}
                        />
                      </div>
                    </td>
                  </tr>
                );
              })}
              {chains.length === 0 && !busy && <EmptyRow colSpan={7} />}
            </tbody>
          </table>
        </div>
      </section>

      <section className="section-block">
        <h2>{"Deposits"}</h2>
        <div className="table-wrap">
          <table className="data-table">
            <thead>
              <tr>
                <th>{"User"}</th>
                <th>{"Chain"}</th>
                <th>{"Amount"}</th>
                <th>{"USD (est.)"}</th>
                <th>{"Stars"}</th>
                <th>{"Status"}</th>
                <th>{"Detected"}</th>
                <th>{"Tx"}</th>
              </tr>
            </thead>
            <tbody>
              {deposits.map((d) => {
                const decimals = chains.find((c) => c.Key === d.ChainKey)?.NativeDecimals ?? 0;
                const asset = d.TokenSymbol || chains.find((c) => c.Key === d.ChainKey)?.NativeSymbol || "";
                return (
                  <tr key={d.ID}>
                    <td>{d.UserFirstName || d.UserPhone || d.UserID}</td>
                    <td>{chains.find((c) => c.Key === d.ChainKey)?.Name ?? d.ChainKey}</td>
                    <td className="mono">{formatAmount(d.AmountRaw, decimals)} {asset}</td>
                    <td className="mono">${(d.USDValueMicros / 1_000_000).toFixed(2)}</td>
                    <td className="mono">{d.StarsCredited}</td>
                    <td><Badge tone={statusTone(d.Status)}>{d.Status}</Badge></td>
                    <td>{new Date(d.DetectedAt).toLocaleString()}</td>
                    <td className="mono" title={d.TxHash}>{d.TxHash.slice(0, 8)}…</td>
                  </tr>
                );
              })}
              {deposits.length === 0 && !busy && <EmptyRow colSpan={8} />}
            </tbody>
          </table>
        </div>
      </section>

      {editingChain && (
        <ChainEditModal
          chain={editingChain}
          onClose={() => setEditingChain(null)}
          onSaved={() => { setEditingChain(null); void load(); }}
        />
      )}
      {sweepingChain && (
        <SweepChainModal chain={sweepingChain} onClose={() => setSweepingChain(null)} />
      )}
      {addingChain && (
        <AddChainModal
          existingKeys={new Set(chains.map((c) => c.Key))}
          onClose={() => setAddingChain(false)}
          onAdded={() => { setAddingChain(false); void load(); }}
        />
      )}
    </PageFrame>
  );
}

function ChainEditModal({ chain, onClose, onSaved }: { chain: DonationChain; onClose: () => void; onSaved: () => void }) {
  const [draft, setDraft] = useState<ChainDraft>(draftFromChain(chain));

  return createPortal(
    <div className="modal-backdrop" role="presentation">
      <section className="modal command-modal" role="dialog" aria-modal="true" aria-label={`Edit ${chain.Name}`}>
        <div className="modal-head">
          <div>
            <div className="eyebrow">{"Chains"}</div>
            <h2>{chain.Name}</h2>
          </div>
          <button className="icon-btn" type="button" onClick={onClose} aria-label={"Close"}><X size={15} /></button>
        </div>
        <div className="command-body">
          <p className="muted">{`Chain ID ${chain.ChainID} · ${chain.NativeSymbol} (${chain.NativeDecimals} decimals) -- fixed at setup, not editable here.`}</p>
          <label className="form-field">
            <span>{"RPC URL"}</span>
            <input value={draft.rpcURL} placeholder={"https://…"} onChange={(event) => setDraft((p) => ({ ...p, rpcURL: event.target.value }))} />
          </label>
          <label className="form-field">
            <span>{"WS URL (optional)"}</span>
            <input value={draft.wsURL} placeholder={"wss://…"} onChange={(event) => setDraft((p) => ({ ...p, wsURL: event.target.value }))} />
          </label>
          <div className="bot-create-fields">
            <label className="duration-field">
              <span>{"Confirmations required"}</span>
              <input type="number" min="1" value={draft.confirmationsRequired} onChange={(event) => setDraft((p) => ({ ...p, confirmationsRequired: event.target.value }))} />
            </label>
            <label className="duration-field">
              <span>{"Manual USD rate (µ, per whole unit)"}</span>
              <input type="number" value={draft.manualUSDRateMicros} onChange={(event) => setDraft((p) => ({ ...p, manualUSDRateMicros: event.target.value }))} />
            </label>
          </div>
          <label className="form-field">
            <span>{"Price feed address (optional)"}</span>
            <input value={draft.priceFeedAddress} placeholder={"0x…"} onChange={(event) => setDraft((p) => ({ ...p, priceFeedAddress: event.target.value }))} />
          </label>
          <label className="gift-switch">
            <input type="checkbox" checked={draft.enabled} onChange={(event) => setDraft((p) => ({ ...p, enabled: event.target.checked }))} />
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
            payload={() => ({
              chain_key: chain.Key,
              rpc_url: draft.rpcURL,
              ws_url: draft.wsURL,
              enabled: draft.enabled,
              confirmations_required: Number(draft.confirmationsRequired),
              price_feed_address: draft.priceFeedAddress,
              manual_usd_rate_micros: Number(draft.manualUSDRateMicros)
            })}
            onDone={onSaved}
          />
        </div>
      </section>
    </div>,
    document.body
  );
}

// SweepChainModal moves every deposit address's balance on this chain
// (native currency and any watchable token) to one operator-supplied
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
          <div>
            <div className="eyebrow">{"Chains"}</div>
            <h2>{`Sweep ${chain.Name}`}</h2>
          </div>
          <button className="icon-btn" type="button" onClick={onClose} aria-label={"Close"}><X size={15} /></button>
        </div>
        <div className="command-body">
          <p>
            {"Moves the balance of every deposit address on this chain -- native "}
            <strong>{chain.NativeSymbol}</strong>
            {" and any watchable stablecoin -- to one destination address. Gas for each transfer always comes out of that same address's own balance. An address without enough native currency to cover its own gas is skipped, not auto-funded."}
          </p>
          <label className="form-field">
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

function AddChainModal({ existingKeys, onClose, onAdded }: { existingKeys: Set<string>; onClose: () => void; onAdded: () => void }) {
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
  const [manualUsdRate, setManualUsdRate] = useState("");
  const [enabled, setEnabled] = useState(false);

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
  }

  function startCustom() {
    setMode("custom");
    setSelectedPreset(null);
    setKey(""); setName(""); setChainId(""); setNativeSymbol(""); setNativeDecimals("18");
    setRpcUrl(""); setWsUrl(""); setConfirmations("12"); setManualUsdRate("");
  }

  const reviewing = mode === "custom" || selectedPreset !== null;
  const keyTaken = existingKeys.has(key.trim().toLowerCase());
  // manual_usd_rate_micros only matters once the chain is actually
  // watched: a chain saved disabled (still being set up) can be missing it
  // for now, but flipping "Enable immediately" without it means every
  // deposit prices to zero Stars and never gets credited -- see
  // app/donations.Service.CreateChain's identical backend check.
  const rateRequired = enabled;
  const valid = reviewing && /^[a-z][a-z0-9_]{1,31}$/.test(key.trim().toLowerCase()) && !keyTaken &&
    name.trim() !== "" && Number(chainId) > 0 && nativeSymbol.trim() !== "" && Number(nativeDecimals) > 0 &&
    Number(confirmations) > 0 && rpcUrl.trim() !== "" && (!rateRequired || Number(manualUsdRate) > 0);

  return createPortal(
    <div className="modal-backdrop" role="presentation">
      <section className="modal command-modal" role="dialog" aria-modal="true" aria-label={"Add chain"}>
        <div className="modal-head">
          <div>
            <div className="eyebrow">{"Chains"}</div>
            <h2>{"Add chain"}</h2>
          </div>
          <button className="icon-btn" type="button" onClick={onClose} aria-label={"Close"}><X size={15} /></button>
        </div>
        <div className="command-body">
          {!reviewing && (
            <>
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
                      {taken && <Badge tone="neutral">{"Already added"}</Badge>}
                    </button>
                  );
                })}
              </div>
              <button className="btn" type="button" onClick={startCustom}>{"Or add a custom chain…"}</button>
            </>
          )}
          {reviewing && (
            <>
              <p className="muted">
                {selectedPreset
                  ? `Review ${selectedPreset.name}'s details before adding -- every field below is editable, including the suggested public RPC endpoint.`
                  : "Fill in every field for your custom EVM-compatible chain."}
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
              {keyTaken && <Alert>{"That key is already in use by another chain."}</Alert>}
              <div className="bot-create-fields">
                <label className="duration-field">
                  <span>{"Chain ID"}</span>
                  <input type="number" value={chainId} onChange={(event) => setChainId(event.target.value)} />
                </label>
                <label className="duration-field">
                  <span>{"Native symbol"}</span>
                  <input value={nativeSymbol} placeholder={"ETH"} onChange={(event) => setNativeSymbol(event.target.value)} />
                </label>
                <label className="duration-field">
                  <span>{"Native decimals"}</span>
                  <input type="number" value={nativeDecimals} onChange={(event) => setNativeDecimals(event.target.value)} />
                </label>
              </div>
              <label className="form-field">
                <span>{"RPC URL"}</span>
                <input value={rpcUrl} placeholder={"https://…"} onChange={(event) => setRpcUrl(event.target.value)} />
              </label>
              <label className="form-field">
                <span>{"WS URL (optional)"}</span>
                <input value={wsUrl} placeholder={"wss://…"} onChange={(event) => setWsUrl(event.target.value)} />
              </label>
              <div className="bot-create-fields">
                <label className="duration-field">
                  <span>{"Confirmations required"}</span>
                  <input type="number" min="1" value={confirmations} onChange={(event) => setConfirmations(event.target.value)} />
                </label>
                <label className="duration-field">
                  <span>{"Manual USD rate (µ, per whole unit)"}</span>
                  <input
                    type="number"
                    value={manualUsdRate}
                    placeholder={"e.g. 2000000000 = $2000"}
                    onChange={(event) => setManualUsdRate(event.target.value)}
                  />
                </label>
              </div>
              {rateRequired && !(Number(manualUsdRate) > 0) && (
                <Alert>{"Required to enable: without it, every deposit on this chain prices to $0 / 0 Stars and is never credited."}</Alert>
              )}
              <label className="gift-switch">
                <input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} />
                <span className="gift-switch-track" aria-hidden="true"><span /></span>
                <span>{"Enable immediately"}</span>
              </label>
              <button className="btn" type="button" onClick={() => { setMode("presets"); setSelectedPreset(null); }}>{"← Back"}</button>
            </>
          )}
        </div>
        <div className="modal-actions">
          <button className="btn" type="button" onClick={onClose}>{"Close"}</button>
          {reviewing && (
            <ActionButton
              label={"Add chain"}
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
                manual_usd_rate_micros: Number(manualUsdRate) || 0,
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

import { Plus, Vault, X } from "./icons";
import { useState } from "react";
import { createPortal } from "react-dom";
import { ChannelPicker, UserPicker } from "./EntityPicker";
import { ActionButton } from "./ActionButton";
import { currencyExponent, formatCurrency, toSmallestUnits } from "../lib/format";
import type { AccountRow, ChannelRow, CollectibleCurrency } from "../types";

type OwnerKind = "vault" | "user" | "channel";

// MintCollectibleUsernameModal collects everything a new collectible needs,
// grouped into clearly labeled steps (identity, owner, price, optional
// marketplace record) instead of one flat wall of fields -- then hands off to
// ActionButton for the usual reason/dry-run/confirm flow.
export function MintCollectibleUsernameModal({ onClose, onMinted }: { onClose: () => void; onMinted: () => void }) {
  const [ownerKind, setOwnerKind] = useState<OwnerKind>("vault");
  const [owner, setOwner] = useState<AccountRow | null>(null);
  const [ownerChannel, setOwnerChannel] = useState<ChannelRow | null>(null);
  const [mintUsername, setMintUsername] = useState("");
  const [currency, setCurrency] = useState<CollectibleCurrency>("XTR");
  const [amount, setAmount] = useState("");
  const [addCryptoLeg, setAddCryptoLeg] = useState(false);
  const [cryptoCurrency, setCryptoCurrency] = useState("TON");
  const [cryptoAmount, setCryptoAmount] = useState("");
  const [showRecord, setShowRecord] = useState(false);
  const [url, setUrl] = useState("");
  const [purchaseDate, setPurchaseDate] = useState("");
  const [purchaseTime, setPurchaseTime] = useState("");

  // int64 request fields are sent as decimal strings (the backend tags them
  // `,string`); purchase_date is Unix seconds. Both amounts are typed in whole
  // currency units and converted here: the API and fragment.collectibleInfo
  // carry smallest units, so 900 TON has to leave the panel as
  // 900000000000 nanotons or clients render 0.0000009.
  const minorAmount = toSmallestUnits(amount, currency);
  const minorCryptoAmount = addCryptoLeg ? toSmallestUnits(cryptoAmount, cryptoCurrency) : "0";
  const amountInvalid = minorAmount === null;
  const cryptoAmountInvalid = addCryptoLeg && minorCryptoAmount === null;
  const canSubmit = mintUsername.trim() !== "" && amount.trim() !== "" && !amountInvalid && !cryptoAmountInvalid
    && (ownerKind === "vault" || (ownerKind === "user" ? owner !== null : ownerChannel !== null));

  function mintPayload(): Record<string, unknown> {
    const payload: Record<string, unknown> = {
      username: mintUsername.trim().replace(/^@/, ""),
      currency,
      amount: minorAmount ?? "0"
    };
    if (ownerKind === "user" && owner) payload.owner_user_id = String(owner.ID);
    if (ownerKind === "channel" && ownerChannel) payload.owner_channel_id = String(ownerChannel.ID);
    // The backend accepts either no crypto leg at all, or TON with a positive
    // nanoton amount -- never a currency without an amount.
    if (addCryptoLeg) {
      payload.crypto_currency = cryptoCurrency;
      payload.crypto_amount = minorCryptoAmount ?? "0";
    }
    if (url.trim()) payload.url = url.trim();
    if (purchaseDate) {
      // fragment.collectibleInfo.purchase_date is a unix timestamp, and the date
      // has always been read as UTC here. The time follows the same clock rather
      // than the operator's local one, so adding it cannot silently shift what a
      // date-only entry used to mean; the field label says UTC.
      const parsed = Date.parse(`${purchaseDate}T${purchaseTime || "00:00"}:00Z`);
      if (Number.isFinite(parsed)) payload.purchase_date = Math.floor(parsed / 1000);
    }
    return payload;
  }

  return createPortal(
    <div className="modal-backdrop" role="presentation">
      <section className="modal command-modal" role="dialog" aria-modal="true" aria-label={"Mint a collectible username"}>
        <div className="modal-head">
          <div>
            <div className="eyebrow">{"NFT usernames"}</div>
            <h2>{"Mint a collectible username"}</h2>
          </div>
          <button className="icon-btn" type="button" onClick={onClose} aria-label={"Close"}><X size={15} /></button>
        </div>
        <div className="command-body">
          <div className="mint-field-group">
            <div className="mint-field-group-label">{"1. Username"}</div>
            <label className="duration-field">
              <span>{"Username"}</span>
              <input value={mintUsername} onChange={(event) => setMintUsername(event.target.value)} placeholder="durov" />
            </label>
          </div>

          <div className="mint-field-group">
            <div className="mint-field-group-label">{"2. Owner"}</div>
            <div className="toolbar" role="group" aria-label={"Owner type"}>
              <button type="button" className={`btn ${ownerKind === "vault" ? "primary" : ""}`} onClick={() => setOwnerKind("vault")}>
                <Vault size={15} /> {"Vault (no owner)"}
              </button>
              <button type="button" className={`btn ${ownerKind === "user" ? "primary" : ""}`} onClick={() => setOwnerKind("user")}>
                {"User owner"}
              </button>
              <button type="button" className={`btn ${ownerKind === "channel" ? "primary" : ""}`} onClick={() => setOwnerKind("channel")}>
                {"Channel owner"}
              </button>
            </div>
            {ownerKind === "user" && <UserPicker label={"User owner"} value={owner} onChange={setOwner} />}
            {ownerKind === "channel" && <ChannelPicker label={"Channel owner"} value={ownerChannel} onChange={setOwnerChannel} />}
            {ownerKind === "vault" && <p className="bot-create-note">{"Mints the asset unassigned; issue it to someone later from the asset page."}</p>}
          </div>

          <div className="mint-field-group">
            <div className="mint-field-group-label">{"3. Price"}</div>
            <p className="bot-create-note">{"A record of what it was sold for -- minting doesn't charge anyone."}</p>
            <div className="bot-create-fields">
              <label className="duration-field">
                <span>{"Currency"}</span>
                <select value={currency} onChange={(event) => setCurrency(event.target.value as CollectibleCurrency)}>
                  <option value="XTR">XTR</option>
                  <option value="TON">TON</option>
                  <option value="USD">USD</option>
                </select>
              </label>
              <label className="duration-field">
                <span>{`Amount (${currency})`}</span>
                <input value={amount} onChange={(event) => setAmount(event.target.value)} inputMode="decimal" placeholder="1000" />
              </label>
            </div>
            {amount.trim() !== "" && !amountInvalid && (
              <p className="bot-create-note">{`Clients will show: ${formatCurrency(minorAmount ?? "0", currency)}.`}</p>
            )}
            {amountInvalid && <p className="bot-create-note">{`Not a valid ${currency} amount: digits only, at most ${String(currencyExponent(currency))} decimal places.`}</p>}
            <label className="checkline">
              <input type="checkbox" checked={addCryptoLeg} onChange={(event) => setAddCryptoLeg(event.target.checked)} />
              {" Also record a TON price"}
            </label>
            {addCryptoLeg && (
              <div className="bot-create-fields">
                <label className="duration-field">
                  <span>{"Crypto currency"}</span>
                  <select value={cryptoCurrency} onChange={(event) => setCryptoCurrency(event.target.value)}>
                    <option value="TON">TON</option>
                  </select>
                </label>
                <label className="duration-field">
                  <span>{`Crypto amount (${cryptoCurrency})`}</span>
                  <input value={cryptoAmount} onChange={(event) => setCryptoAmount(event.target.value)} inputMode="decimal" placeholder="12.5" />
                </label>
              </div>
            )}
            {cryptoAmountInvalid && <p className="bot-create-note">{`Not a valid ${cryptoCurrency} amount: digits only, at most ${String(currencyExponent(cryptoCurrency))} decimal places.`}</p>}
          </div>

          <div className="mint-field-group">
            <button type="button" className="link-button" onClick={() => setShowRecord((v) => !v)}>
              {showRecord ? "Hide marketplace record" : "+ Add marketplace record (optional)"}
            </button>
            {showRecord && (
              <div className="bot-create-fields">
                <label className="duration-field">
                  <span>{"Marketplace URL"}</span>
                  <input value={url} onChange={(event) => setUrl(event.target.value)} placeholder="https://fragment.com/username/durov" />
                </label>
                <label className="duration-field">
                  <span>{"Purchase date (UTC)"}</span>
                  <input value={purchaseDate} onChange={(event) => setPurchaseDate(event.target.value)} type="date" />
                </label>
                <label className="duration-field">
                  <span>{"Purchase time (UTC)"}</span>
                  <input
                    value={purchaseTime}
                    onChange={(event) => setPurchaseTime(event.target.value)}
                    type="time"
                    step={60}
                    disabled={!purchaseDate}
                  />
                </label>
              </div>
            )}
          </div>
        </div>
        <div className="modal-actions">
          <button className="btn" type="button" onClick={onClose}>{"Close"}</button>
          <ActionButton
            disabled={!canSubmit}
            label={"Mint username"}
            icon={<Plus size={15} />}
            tone="neutral"
            path="/api/actions/mint-collectible-username"
            payload={mintPayload}
            onDone={onMinted}
          />
        </div>
      </section>
    </div>,
    document.body
  );
}

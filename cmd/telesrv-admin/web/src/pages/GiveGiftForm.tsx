import { CheckCircle2, CircleAlert, Gift, Loader2, Play, User, Users } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { api, errorMessage } from "../api";
import { ChannelPicker, UserPicker } from "../components/EntityPicker";
import { Alert, JsonBlock } from "../components/ui";
import type { AccountRow, ChannelRow, CommandResult, StarGiftCatalogRow, StarGiftCollectibleAttributeRow, StarGiftCollectiblePreview } from "../types";

const SYSTEM_SENDER = "777000";

type RecipientKind = "user" | "channel";

function attrLabel(attr: StarGiftCollectibleAttributeRow): string {
  const rarity = attr.rarity_permille > 0 ? ` · ${(attr.rarity_permille / 10).toFixed(1)}%` : "";
  return `${attr.name || `#${attr.id}`}${rarity}`;
}

export function GiveGiftForm({ gift, onDone }: { gift: StarGiftCatalogRow; onDone?: () => void }) {
  const [kind, setKind] = useState<RecipientKind>("user");
  const [user, setUser] = useState<AccountRow | null>(null);
  const [channel, setChannel] = useState<ChannelRow | null>(null);
  const [message, setMessage] = useState("");
  const [hideName, setHideName] = useState(false);
  const [upgrade, setUpgrade] = useState(false);
  const [preview, setPreview] = useState<StarGiftCollectiblePreview | null>(null);
  const [previewError, setPreviewError] = useState("");
  const [modelID, setModelID] = useState("0");
  const [patternID, setPatternID] = useState("0");
  const [backdropID, setBackdropID] = useState("0");
  const [reason, setReason] = useState("");
  const [result, setResult] = useState<CommandResult | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const recipientID = kind === "user" ? user?.ID ?? 0 : channel?.ID ?? 0;
  const upgradable = kind === "user" && upgrade;

  // Reset the collectible selection whenever the chosen gift changes; the
  // recipient/sender/message are intentionally preserved for fast re-issuing.
  useEffect(() => {
    setUpgrade(false);
    setPreview(null);
    setPreviewError("");
    setModelID("0");
    setPatternID("0");
    setBackdropID("0");
    setResult(null);
    setError("");
  }, [gift.GiftID]);

  useEffect(() => {
    if (!upgradable || preview) return;
    let cancelled = false;
    setPreviewError("");
    api.giftCollectibles(gift.GiftID)
      .then((data) => { if (!cancelled) setPreview(data); })
      .catch((err) => { if (!cancelled) setPreviewError(errorMessage(err)); });
    return () => { cancelled = true; };
  }, [upgradable, preview, gift.GiftID]);

  function buildPayload(confirm: boolean): Record<string, unknown> {
    return {
      gift_id: gift.GiftID,
      // Gifts are always sent from the official system account (777000).
      sender_user_id: SYSTEM_SENDER,
      user_id: kind === "user" ? String(recipientID) : "0",
      channel_id: kind === "channel" ? String(recipientID) : "0",
      hide_name: hideName,
      message: message.trim(),
      upgrade: upgradable,
      model_attribute_id: upgradable ? modelID : "0",
      pattern_attribute_id: upgradable ? patternID : "0",
      backdrop_attribute_id: upgradable ? backdropID : "0",
      reason: reason.trim(),
      confirm
    };
  }

  const previewPayload = useMemo(() => buildPayload(false), [gift.GiftID, kind, recipientID, message, hideName, upgrade, modelID, patternID, backdropID, reason]);
  const canConfirm = result?.dry_run && !result.error;

  async function run(confirm: boolean) {
    if (recipientID <= 0) {
      setError("Select a recipient first");
      return;
    }
    if (!reason.trim()) {
      setError("Please enter an operation reason");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const commandResult = await api.action("/api/actions/give-star-gift", buildPayload(confirm));
      setResult(commandResult);
      if (confirm && !commandResult.error) {
        onDone?.();
      }
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="give-gift-form">
      <div className="give-gift-summary">
        <Gift size={16} />
        <div>
          <strong>{gift.Title || `Gift #${gift.GiftID}`}</strong>
          <span className="mono">#{gift.GiftID} · ⭐ {gift.Stars}</span>
        </div>
      </div>

      <div className="give-gift-tabs" role="group" aria-label={"Recipient type"}>
        <button type="button" className={`btn ${kind === "user" ? "primary" : ""}`} onClick={() => { setKind("user"); setResult(null); }}>
          <User size={15} /> {"User"}
        </button>
        <button type="button" className={`btn ${kind === "channel" ? "primary" : ""}`} onClick={() => { setKind("channel"); setUpgrade(false); setResult(null); }}>
          <Users size={15} /> {"Channel"}
        </button>
      </div>

      {kind === "user"
        ? <UserPicker label={"Recipient user"} value={user} onChange={(row) => { setUser(row); setResult(null); }} />
        : <ChannelPicker label={"Recipient channel"} value={channel} onChange={(row) => { setChannel(row); setResult(null); }} />}

      <label className="form-field">
        <span>{"Sender account ID"}</span>
        <input value={SYSTEM_SENDER} disabled readOnly />
        <small className="field-hint">{"Gifts are always sent from the system account 777000 (OwpenGram)."}</small>
      </label>

      <label className="form-field">
        <span>{"Attached message (optional)"}</span>
        <textarea value={message} rows={2} maxLength={128} onChange={(event) => { setMessage(event.target.value); setResult(null); }} placeholder={"Shown with the gift"} />
      </label>

      <label className="gift-switch">
        <input type="checkbox" checked={hideName} onChange={(event) => { setHideName(event.target.checked); setResult(null); }} />
        <span className="gift-switch-track" aria-hidden="true"><span /></span>
        <span>{"Hide sender name from recipient"}</span>
      </label>

      {kind === "user" && (
        <>
          <label className="gift-switch">
            <input type="checkbox" checked={upgrade} onChange={(event) => { setUpgrade(event.target.checked); if (!event.target.checked) { setModelID("0"); setPatternID("0"); setBackdropID("0"); } setResult(null); }} />
            <span className="gift-switch-track" aria-hidden="true"><span /></span>
            <span>{"Deliver as upgraded collectible"}</span>
          </label>
          {upgrade && <p className="give-gift-upgrade-note">{"The gift is minted as a unique collectible. Pick specific attributes below, or leave them on Random to draw from the published pool. The collectible number is assigned automatically. Requires a published collectible upgrade with remaining supply."}</p>}
          {upgrade && previewError && <Alert>{previewError}</Alert>}
          {upgrade && preview && (
            <div className="gift-fields-grid give-gift-attrs">
              <label>
                <span>{"Model"}</span>
                <select value={modelID} onChange={(event) => { setModelID(event.target.value); setResult(null); }}>
                  <option value="0">{"Random"}</option>
                  {(preview.models ?? []).map((attr) => <option key={attr.id} value={attr.id}>{attrLabel(attr)}</option>)}
                </select>
              </label>
              <label>
                <span>{"Pattern"}</span>
                <select value={patternID} onChange={(event) => { setPatternID(event.target.value); setResult(null); }}>
                  <option value="0">{"Random"}</option>
                  {(preview.patterns ?? []).map((attr) => <option key={attr.id} value={attr.id}>{attrLabel(attr)}</option>)}
                </select>
              </label>
              <label>
                <span>{"Backdrop"}</span>
                <select value={backdropID} onChange={(event) => { setBackdropID(event.target.value); setResult(null); }}>
                  <option value="0">{"Random"}</option>
                  {(preview.backdrops ?? []).map((attr) => <option key={attr.id} value={attr.id}>{attrLabel(attr)}</option>)}
                </select>
              </label>
            </div>
          )}
        </>
      )}

      <label className="form-field">
        <span>{"Operation reason"}</span>
        <textarea value={reason} rows={2} onChange={(event) => setReason(event.target.value)} placeholder={"Describe why this operation is being performed"} />
      </label>

      <div className="command-preview">
        <div className="preview-head">{"Request preview"}</div>
        <JsonBlock value={JSON.stringify(previewPayload, null, 2)} />
      </div>

      {error && <Alert>{error}</Alert>}
      {result && (
        <div className="result-box">
          <div className="result-title">
            {result.error ? <CircleAlert size={16} /> : <CheckCircle2 size={16} />}
            <strong>{result.message || result.error || "Action result"}</strong>
          </div>
          <div className="result-line"><span>{"Command ID"}</span><strong>{result.command_id}</strong></div>
          <div className="result-line"><span>{"Status"}</span><strong>{result.status}</strong></div>
          <div className="result-line"><span>{"Dry-run"}</span><strong>{result.dry_run ? "Yes" : "No"}</strong></div>
          {result.details && <JsonBlock value={JSON.stringify(result.details, null, 2)} />}
        </div>
      )}

      <div className="give-gift-form-actions">
        <button className="btn icon-text" type="button" onClick={() => run(false)} disabled={busy}>
          {busy ? <Loader2 size={15} className="spin" /> : <Play size={15} />}
          {result ? "Run dry-run again" : "Run dry-run first"}
        </button>
        <button className="btn primary icon-text" type="button" onClick={() => run(true)} disabled={busy || !canConfirm}>
          <Gift size={15} />
          {"Give gift"}
        </button>
      </div>
    </div>
  );
}

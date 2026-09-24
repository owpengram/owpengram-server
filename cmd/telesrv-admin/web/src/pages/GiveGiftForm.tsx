import { CheckCircle2, CircleAlert, Gift, Loader2, Sparkles, User, Users } from "lucide-react";
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
  const [pickAttrs, setPickAttrs] = useState(false);
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
    setPickAttrs(false);
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
  // A dry-run result staged for the *current* form -- editing anything after
  // it clears result, so this is never stale confirmation of an old choice.
  const canConfirm = result?.dry_run && !result.error;

  function invalidateStaged() {
    setResult(null);
    setError("");
  }

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

  // One button drives both steps: the first click stages a dry-run and shows
  // what would happen, the second (now armed) click confirms it. Anything
  // edited in between un-arms it, so a stale preview can never be confirmed
  // by accident.
  const mainAction = canConfirm ? "confirm" : "dry-run";

  return (
    <div className="give-gift-form">
      <div className="give-gift-summary">
        <Gift size={16} />
        <div>
          <strong>{gift.Title || `Gift #${gift.GiftID}`}</strong>
          <span className="mono">#{gift.GiftID} · ⭐ {gift.Stars} · from 777000 (OwpenGram)</span>
        </div>
      </div>

      <div className="give-gift-tabs" role="group" aria-label={"Recipient type"}>
        <button type="button" className={`btn ${kind === "user" ? "primary" : ""}`} onClick={() => { setKind("user"); invalidateStaged(); }}>
          <User size={15} /> {"User"}
        </button>
        <button type="button" className={`btn ${kind === "channel" ? "primary" : ""}`} onClick={() => { setKind("channel"); setUpgrade(false); invalidateStaged(); }}>
          <Users size={15} /> {"Channel"}
        </button>
      </div>

      {kind === "user"
        ? <UserPicker label={"Recipient user"} value={user} onChange={(row) => { setUser(row); invalidateStaged(); }} />
        : <ChannelPicker label={"Recipient channel"} value={channel} onChange={(row) => { setChannel(row); invalidateStaged(); }} />}

      {kind === "user" && (
        <label className="gift-switch">
          <input type="checkbox" checked={upgrade} onChange={(event) => { setUpgrade(event.target.checked); if (!event.target.checked) { setPickAttrs(false); setModelID("0"); setPatternID("0"); setBackdropID("0"); } invalidateStaged(); }} />
          <span className="gift-switch-track" aria-hidden="true"><span /></span>
          <span>{"Deliver as upgraded collectible"}</span>
        </label>
      )}
      {upgrade && previewError && <Alert>{previewError}</Alert>}
      {upgrade && preview && (
        <div className="give-gift-attrs-toggle">
          <p className="give-gift-upgrade-note"><Sparkles size={13} /> {"Model, pattern and backdrop are drawn at random from the published pool."}</p>
          {!pickAttrs
            ? <button className="btn compact-btn" type="button" onClick={() => setPickAttrs(true)}>{"Choose specific attributes instead"}</button>
            : (
              <div className="gift-fields-grid give-gift-attrs">
                <label>
                  <span>{"Model"}</span>
                  <select value={modelID} onChange={(event) => { setModelID(event.target.value); invalidateStaged(); }}>
                    <option value="0">{"Random"}</option>
                    {(preview.models ?? []).map((attr) => <option key={attr.id} value={attr.id}>{attrLabel(attr)}</option>)}
                  </select>
                </label>
                <label>
                  <span>{"Pattern"}</span>
                  <select value={patternID} onChange={(event) => { setPatternID(event.target.value); invalidateStaged(); }}>
                    <option value="0">{"Random"}</option>
                    {(preview.patterns ?? []).map((attr) => <option key={attr.id} value={attr.id}>{attrLabel(attr)}</option>)}
                  </select>
                </label>
                <label>
                  <span>{"Backdrop"}</span>
                  <select value={backdropID} onChange={(event) => { setBackdropID(event.target.value); invalidateStaged(); }}>
                    <option value="0">{"Random"}</option>
                    {(preview.backdrops ?? []).map((attr) => <option key={attr.id} value={attr.id}>{attrLabel(attr)}</option>)}
                  </select>
                </label>
              </div>
            )}
        </div>
      )}

      <details className="raw-details">
        <summary>{"Message and delivery options"}</summary>
        <div className="give-gift-more-options">
          <label className="form-field">
            <span>{"Attached message (optional)"}</span>
            <textarea value={message} rows={2} maxLength={128} onChange={(event) => { setMessage(event.target.value); invalidateStaged(); }} placeholder={"Shown with the gift"} />
          </label>
          <label className="gift-switch">
            <input type="checkbox" checked={hideName} onChange={(event) => { setHideName(event.target.checked); invalidateStaged(); }} />
            <span className="gift-switch-track" aria-hidden="true"><span /></span>
            <span>{"Hide sender name from recipient"}</span>
          </label>
        </div>
      </details>

      <label className="form-field">
        <span>{"Operation reason"}</span>
        <textarea value={reason} rows={2} onChange={(event) => { setReason(event.target.value); invalidateStaged(); }} placeholder={"Describe why this operation is being performed"} />
      </label>

      {error && <Alert>{error}</Alert>}
      {result && (
        <div className="result-box">
          <div className="result-title">
            {result.error ? <CircleAlert size={16} /> : <CheckCircle2 size={16} />}
            <strong>{result.error ? result.error : canConfirm ? "Ready — review below, then confirm" : result.message || "Delivered"}</strong>
          </div>
          {result.details && <JsonBlock value={JSON.stringify(result.details, null, 2)} />}
        </div>
      )}

      <details className="raw-details">
        <summary>{"Request JSON"}</summary>
        <JsonBlock value={JSON.stringify(previewPayload, null, 2)} />
      </details>

      <div className="give-gift-form-actions">
        <button className="btn primary icon-text" type="button" onClick={() => run(mainAction === "confirm")} disabled={busy}>
          {busy ? <Loader2 size={15} className="spin" /> : <Gift size={15} />}
          {mainAction === "confirm" ? "Confirm and send" : "Preview gift"}
        </button>
      </div>
    </div>
  );
}

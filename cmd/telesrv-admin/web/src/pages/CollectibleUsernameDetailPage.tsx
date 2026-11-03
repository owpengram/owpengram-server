import { ArrowLeft, ArrowLeftRight, ExternalLink, Flame, RefreshCw, ScrollText, Settings2, Trash2, Undo2, UserRound } from "../components/icons";
import { useEffect, useState, type ReactNode } from "react";
import { api, errorMessage } from "../api";
import { ActionButton } from "../components/ActionButton";
import { ChannelPicker, UserPicker } from "../components/EntityPicker";
import { Alert, Badge, EmptyRow, LoadingSurface, PageFrame, SectionHead, Summary } from "../components/ui";
import { displayUsername, formatCurrency, formatDate } from "../lib/format";
import type { Navigate } from "../routing";
import type {
  AccountRow,
  ChannelRow,
  CollectibleUsernameDetail,
  CollectibleUsernameTransferKind
} from "../types";
import { UsernameStatus, ownerLabel, priceLabel } from "./CollectibleUsernamesPage";

type RecipientKind = "user" | "channel";
type Tab = "profile" | "actions";

export function CollectibleUsernameDetailPage({ id, navigate }: { id: string; navigate: Navigate }) {
  const [detail, setDetail] = useState<CollectibleUsernameDetail | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [tab, setTab] = useState<Tab>("profile");
  const [recipientKind, setRecipientKind] = useState<RecipientKind>("user");
  const [recipientUser, setRecipientUser] = useState<AccountRow | null>(null);
  const [recipientChannel, setRecipientChannel] = useState<ChannelRow | null>(null);

  async function load() {
    setBusy(true);
    setError("");
    try {
      setDetail(await api.collectibleUsername(id));
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    void load();
    setTab("profile");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);

  if (error && !detail) {
    return <Alert>{error}</Alert>;
  }
  if (!detail) {
    return <LoadingSurface label={busy ? "Loading collectible username…" : "Waiting for data"} />;
  }

  const asset = detail.asset;
  const transfers = detail.transfers ?? [];
  const vaultLabel = "Vault";
  const hasOwner = Boolean(asset.OwnerPeerType) && asset.OwnerPeerID !== "" && asset.OwnerPeerID !== "0";
  const burned = asset.Status === "burned";

  function openOwner() {
    if (!hasOwner) return;
    navigate(asset.OwnerPeerType === "channel" ? `/channels/${asset.OwnerPeerID}` : `/accounts/${asset.OwnerPeerID}`);
  }

  // Peer ids travel as decimal strings to match the backend `,string` tags.
  function transferPayload(): Record<string, unknown> {
    const payload: Record<string, unknown> = { username: asset.Username };
    if (recipientKind === "user" && recipientUser) payload.to_user_id = String(recipientUser.ID);
    if (recipientKind === "channel" && recipientChannel) payload.to_channel_id = String(recipientChannel.ID);
    return payload;
  }

  const tabs: Array<{ key: Tab; label: string; icon: ReactNode }> = [
    { key: "profile", label: "Profile & Status", icon: <UserRound size={15} /> },
    { key: "actions", label: "Actions & Management", icon: <Settings2 size={15} /> }
  ];

  return (
    <PageFrame
      title={`Collectible ${displayUsername(asset.Username)}`}
      eyebrow={"NFT usernames / Asset"}
      actions={
        <>
          <button className="btn icon-text" type="button" onClick={() => navigate("/collectible-usernames")}>
            <ArrowLeft size={15} /> {"Back to list"}
          </button>
          <button className="btn icon-text" type="button" onClick={load} disabled={busy}>
            <RefreshCw size={15} className={busy ? "spin" : ""} /> {"Refresh"}
          </button>
        </>
      }
    >
      {error && <Alert>{error}</Alert>}
      <section className="entity-head">
        <div className="entity-head-main">
          <div>
            <div className="entity-title">{displayUsername(asset.Username)}</div>
            <div className="entity-subtitle">{`Asset #${asset.ID}`}</div>
          </div>
        </div>
        <div className="entity-badges">
          <UsernameStatus status={asset.Status} />
          <Badge tone={asset.TransferCount > 0 ? "warn" : "neutral"}>{`${asset.TransferCount} transfers`}</Badge>
          {asset.Status === "owned" && (
            <Badge tone={asset.RegistryActive ? "good" : "warn"}>
              {asset.RegistryActive ? "Active in profile" : "Hidden in profile"}
            </Badge>
          )}
        </div>
      </section>

      <div className="toolbar" role="group" aria-label={"Asset sections"}>
        {tabs.map((item) => (
          <button
            key={item.key}
            className={`btn icon-text ${tab === item.key ? "primary" : ""}`}
            type="button"
            aria-pressed={tab === item.key}
            onClick={() => setTab(item.key)}
          >
            {item.icon} {item.label}
          </button>
        ))}
      </div>

      {tab === "profile" && (
        <div className="stacked-sections">
          <div className="summary-grid">
            <Summary label={"Owner"} value={ownerLabel(asset, vaultLabel)} />
            <Summary label={"Price"} value={priceLabel(asset)} mono />
            <Summary label={"Purchase date (UTC)"} value={formatDate(asset.PurchaseDate) || "-"} />
            <Summary
              label={"Original owner"}
              value={peerLabel(asset.OriginalOwnerPeerType, asset.OriginalOwnerPeerID, vaultLabel, asset.OriginalOwnerUsername)}
            />
            <Summary label={"Transfers"} value={String(asset.TransferCount)} mono />
            <Summary label={"Created"} value={formatDate(asset.CreatedAt) || "-"} />
            <Summary label={"Updated"} value={formatDate(asset.UpdatedAt) || "-"} />
          </div>
          <div className="toolbar">
            {hasOwner && (
              <button className="row-link" type="button" onClick={openOwner}>
                {asset.OwnerPeerType === "channel" ? "Open owner channel" : "Open owner account"}
              </button>
            )}
            {asset.URL && (
              <a className="row-link" href={asset.URL} target="_blank" rel="noreferrer noopener">
                <ExternalLink size={14} /> {"Open marketplace page"}
              </a>
            )}
          </div>
          <section className="section-block">
            <SectionHead title={"Provenance history"} text={"Mint, transfer, revoke and burn events in chronological order."} action={<ScrollText size={16} />} />
            <div className="table-wrap">
              <table className="data-table">
                <thead>
                  <tr>
                    <th>{"ID"}</th>
                    <th>{"Event"}</th>
                    <th>{"From"}</th>
                    <th>{"To"}</th>
                    <th>{"Price"}</th>
                    <th>{"Actor"}</th>
                    <th>{"Reason"}</th>
                    <th>{"Time"}</th>
                  </tr>
                </thead>
                <tbody>
                  {transfers.map((row) => (
                    <tr key={row.ID}>
                      <td className="mono">{row.ID}</td>
                      <td><TransferKind kind={row.Kind} /></td>
                      <td className="mono">{peerLabel(row.FromPeerType, row.FromPeerID, vaultLabel, row.FromUsername)}</td>
                      <td className="mono">{peerLabel(row.ToPeerType, row.ToPeerID, vaultLabel, row.ToUsername)}</td>
                      <td className="mono">{row.Amount && row.Amount !== "0" ? formatCurrency(row.Amount, row.Currency) : "-"}</td>
                      <td>{row.Actor || "-"}</td>
                      <td className="truncate">{row.Reason || "-"}</td>
                      <td>{formatDate(row.CreatedAt) || "-"}</td>
                    </tr>
                  ))}
                  {transfers.length === 0 && <EmptyRow colSpan={8} />}
                </tbody>
              </table>
            </div>
          </section>
        </div>
      )}

      {tab === "actions" && (
        <div className="stacked-sections">
          {burned ? (
            <section className="section-block">
              <SectionHead title={"Asset Operations"} />
              <div className="card-body">
                <p className="bot-create-note">{"This username is burned — no further operations are possible."}</p>
              </div>
            </section>
          ) : (
            <div className="action-groups">
              <section className="section-block">
                <SectionHead title={"Transfer Ownership"} text={"Sent immediately; appended to the provenance history."} />
                <div className="card-body">
                  <div className="toolbar" role="group" aria-label={"Recipient type"}>
                    <button type="button" className={`btn ${recipientKind === "user" ? "primary" : ""}`} onClick={() => setRecipientKind("user")}>
                      {"To user"}
                    </button>
                    <button type="button" className={`btn ${recipientKind === "channel" ? "primary" : ""}`} onClick={() => setRecipientKind("channel")}>
                      {"To channel"}
                    </button>
                  </div>
                  {recipientKind === "user"
                    ? <UserPicker label={"To user"} value={recipientUser} onChange={setRecipientUser} />
                    : <ChannelPicker label={"To channel"} value={recipientChannel} onChange={setRecipientChannel} />}
                  <ActionButton
                    label={"Transfer"}
                    icon={<ArrowLeftRight size={15} />}
                    tone="warn"
                    path="/api/actions/transfer-collectible-username"
                    payload={transferPayload}
                    onDone={load}
                  />
                </div>
              </section>

              <section className="section-block">
                <SectionHead title={"Revoke To Vault"} text={"Returns the username to the vault; it can be issued again later."} />
                <div className="card-body">
                  <div className="action-stack">
                    <ActionButton
                      label={"Revoke to vault"}
                      icon={<Undo2 size={15} />}
                      tone="warn"
                      path="/api/actions/revoke-collectible-username"
                      payload={() => ({ username: asset.Username, burn: false })}
                      onDone={load}
                    />
                  </div>
                </div>
              </section>

              <section className="section-block">
                <SectionHead title={"Danger Zone"} />
                <div className="card-body">
                  <div className="danger-zone">
                    <ActionButton
                      label={"Burn permanently"}
                      icon={<Flame size={15} />}
                      tone="danger"
                      path="/api/actions/revoke-collectible-username"
                      payload={() => ({ username: asset.Username, burn: true })}
                      onDone={load}
                    />
                    <p className="bot-create-note">{"Irreversible: the username is destroyed and can never be issued again."}</p>
                    <ActionButton
                      label={"Delete record"}
                      icon={<Trash2 size={15} />}
                      tone="danger"
                      path="/api/actions/delete-collectible-username"
                      payload={() => ({ username: asset.Username })}
                      onDone={() => navigate("/collectible-usernames")}
                    />
                    <p className="bot-create-note">{"Erases the asset and its ownership history, and frees the username for a fresh issue. Use this for a username issued by mistake; a burn keeps the history instead."}</p>
                  </div>
                </div>
              </section>
            </div>
          )}
        </div>
      )}
    </PageFrame>
  );
}

const usernameKindLabels: Record<CollectibleUsernameTransferKind, string> = {
  mint: "Mint",
  transfer: "Transfer",
  burn: "Burn",
  revoke: "Revoke"
};

function TransferKind({ kind }: { kind: CollectibleUsernameTransferKind }) {
  const tone = kind === "burn" ? "danger" : kind === "revoke" ? "warn" : kind === "mint" ? "good" : "neutral";
  return <Badge tone={tone}>{usernameKindLabels[kind]}</Badge>;
}

function peerLabel(type: string, peerID: string, vaultLabel: string, username = ""): string {
  if (!type || peerID === "" || peerID === "0") return vaultLabel;
  const handle = displayUsername(username);
  return handle ? `${handle} · ${type}:${peerID}` : `${type}:${peerID}`;
}

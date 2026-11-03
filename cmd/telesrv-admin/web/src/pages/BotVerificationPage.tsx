import {
  Ban,
  BadgeCheck,
  Building2,
  ChevronDown,
  ChevronRight,
  ExternalLink,
  Loader2,
  Plus,
  Power,
  PowerOff,
  RefreshCw,
  Search,
  Stamp,
  Sticker,
  Trash2
} from "../components/icons";
import { useEffect, useState, type ReactNode } from "react";
import { api, APIError, errorMessage } from "../api";
import { ActionButton } from "../components/ActionButton";
import { EmojiPicker } from "../components/EmojiPicker";
import { BotPicker } from "../components/EntityPicker";
import { Alert, Badge, EmptyRow, Metric, PageFrame, QueryPanel, SectionHead } from "../components/ui";
import { displayUsername, formatDate } from "../lib/format";
import {
  permissionBotVerificationManage,
  permissionVerificationReview,
  usePermissions
} from "../permissions";
import type { Navigate } from "../routing";
import type {
  BotRow,
  BotVerificationPeerType,
  BotVerifierRow,
  CustomVerificationRequestRow,
  CustomVerificationRequestStatus,
  CustomVerificationRow,
  VerificationIconRow
} from "../types";

type Tab = "requests" | "verifiers" | "icons" | "marks";
type StatusFilter = "all" | CustomVerificationRequestStatus;
type PeerTypeFilter = "all" | BotVerificationPeerType;

const statuses: CustomVerificationRequestStatus[] = ["pending", "approved", "rejected", "revoked"];
const peerTypes: BotVerificationPeerType[] = ["user", "channel"];

export const statusLabels: Record<CustomVerificationRequestStatus, string> = {
  pending: "Pending",
  approved: "Approved",
  rejected: "Rejected",
  revoked: "Mark revoked"
};

export const peerTypeLabels: Record<BotVerificationPeerType, string> = {
  user: "Account",
  channel: "Channel"
};

// The section owns four different objects — applications, verifiers, the icon
// catalogue and the granted marks — and mixing them into one table would hide which
// row an action addresses. They are separate tabs over one shared verifier/icon
// load: the roster feeds three of the four filters, so it is fetched once here
// rather than per tab.
export function BotVerificationPage({ navigate }: { navigate: Navigate }) {
  const { can } = usePermissions();
  const canManage = can(permissionBotVerificationManage);
  const canSeeOfficial = can(permissionVerificationReview);
  const [tab, setTab] = useState<Tab>("requests");
  const [verifiers, setVerifiers] = useState<BotVerifierRow[]>([]);
  const [icons, setIcons] = useState<VerificationIconRow[]>([]);
  const [error, setError] = useState("");
  const [rosterDenied, setRosterDenied] = useState(false);

  async function loadRoster() {
    setError("");
    setRosterDenied(false);
    try {
      const [verifierResult, iconResult] = await Promise.all([
        api.botVerifiers(new URLSearchParams({ limit: "200" })),
        api.verificationIcons(new URLSearchParams({ limit: "200" }))
      ]);
      setVerifiers(verifierResult.rows ?? []);
      setIcons(iconResult.rows ?? []);
    } catch (err) {
      // A 403 here is not a fault to alarm about: it means the session may review
      // applications but not see the roster. Saying so beats an empty table that
      // reads as "no verifiers configured".
      if (err instanceof APIError && err.status === 403) {
        setVerifiers([]);
        setIcons([]);
        setRosterDenied(true);
        return;
      }
      setError(errorMessage(err));
    }
  }

  useEffect(() => {
    void loadRoster();
  }, []);

  const tabs: Array<{ key: Tab; label: string; icon: ReactNode }> = [
    { key: "requests", label: "Applications", icon: <Stamp size={15} /> },
    { key: "verifiers", label: "Verifiers", icon: <Building2 size={15} /> },
    { key: "icons", label: "Icon catalogue", icon: <Sticker size={15} /> },
    { key: "marks", label: "Granted marks", icon: <BadgeCheck size={15} /> }
  ];

  return (
    <PageFrame
      title={"Third-party verification"}
      eyebrow={"Third-party verification / Verifiers, icons, marks"}
      actions={
        canSeeOfficial ? (
          <button className="btn icon-text" type="button" onClick={() => navigate("/verification")}>
            <ExternalLink size={15} /> {"Official verification"}
          </button>
        ) : undefined
      }
    >
      {error && <Alert>{error}</Alert>}
      {rosterDenied && <Alert>{"The server refused the verifier roster and the icon catalogue for this session (403), so both lists are empty here — applications can still be reviewed."}</Alert>}
      {/* The one thing an operator has to understand before touching anything here:
          this is a verifier company's own icon, not the platform checkmark. */}
      <section className="section-block">
        <SectionHead title={"A verifier company's icon — not the official checkmark"} text={"A third-party mark is a verifier bot's own icon, drawn right BEFORE the name of an account, a bot or a channel, plus one line of description in the profile. It says “this verifier vouches for this peer”, and nothing more."} />
        <p className="bot-create-note">{"The icon is a custom emoji document. The client fetches it through messages.getCustomEmojiDocuments, so a document id that resolves to nothing renders as no badge at all — which is why marks are granted from the catalogue below rather than from a typed number."}</p>
        <p className="bot-create-note">{"The official checkmark is a different mechanism, granted by the platform in the Verification section. The two are stored, shown and taken away separately, and neither one implies the other."}</p>
        {!canManage && <p className="bot-create-note">{"This session can read the section and decide applications, but not change verifiers or the icon catalogue — that needs the botverification.manage permission."}</p>}
      </section>

      <div className="toolbar" role="group" aria-label={"Third-party verification"}>
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

      {tab === "requests" && <RequestsBlock navigate={navigate} verifiers={verifiers} />}
      {tab === "verifiers" && (
        <VerifiersBlock
          verifiers={verifiers}
          icons={icons}
          canManage={canManage}
          onChanged={loadRoster}
          navigate={navigate}
        />
      )}
      {tab === "icons" && (
        <IconsBlock icons={icons} verifiers={verifiers} canManage={canManage} onChanged={loadRoster} />
      )}
      {tab === "marks" && <MarksBlock verifiers={verifiers} canManage={canManage} navigate={navigate} />}
    </PageFrame>
  );
}

// ---------------------------------------------------------------------------
// Applications
// ---------------------------------------------------------------------------

function RequestsBlock({ navigate, verifiers }: { navigate: Navigate; verifiers: BotVerifierRow[] }) {
  const [status, setStatus] = useState<StatusFilter>("pending");
  const [verifierBotID, setVerifierBotID] = useState("");
  const [peerType, setPeerType] = useState<PeerTypeFilter>("all");
  const [q, setQ] = useState("");
  const [limit, setLimit] = useState("50");
  const [rows, setRows] = useState<CustomVerificationRequestRow[]>([]);
  const [counts, setCounts] = useState<Record<string, string>>({});
  const [hasMore, setHasMore] = useState(false);
  const [cursor, setCursor] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  // One free-text field: the backend matches the application id, the peer id and a
  // username (applicant or peer), so "@durov", "42" and a peer id all work without a
  // mode switch.
  async function load(next = false) {
    setBusy(true);
    setError("");
    const params = new URLSearchParams({ limit });
    if (status !== "all") params.set("status", status);
    if (verifierBotID) params.set("verifier_bot_id", verifierBotID);
    if (peerType !== "all") params.set("peer_type", peerType);
    if (q.trim()) params.set("q", q.trim().replace(/^@/, ""));
    if (next && cursor) params.set("before_id", cursor);
    try {
      const result = await api.customVerificationRequests(params);
      const page = result.rows ?? [];
      setRows((current) => (next ? [...current, ...page] : page));
      setCursor(result.next_before_id ?? "");
      setHasMore(Boolean(result.has_more));
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  // The counts describe the whole queue, not the current page, so they are fetched
  // separately from the keyset listing.
  async function loadCounts() {
    try {
      const result = await api.botVerificationCounts();
      setCounts(result.counts ?? {});
    } catch (err) {
      setError(errorMessage(err));
    }
  }

  useEffect(() => {
    void load(false);
    void loadCounts();
  }, []);

  function refresh() {
    void load(false);
    void loadCounts();
  }

  return (
    <>
      <section className="section-block">
        <SectionHead
          title={"Application queue"}
          text={"Applications filed with a verifier bot by the owner of the peer. The counters cover the whole queue, not the page below."}
          action={
            <button className="btn icon-text" type="button" onClick={refresh} disabled={busy}>
              <RefreshCw size={15} className={busy ? "spin" : ""} /> {"Refresh"}
            </button>
          }
        />
        {error && <Alert>{error}</Alert>}
        <div className="metric-row">
          {statuses.map((item) => (
            <Metric
              key={item}
              label={statusLabels[item]}
              value={counts[item] ?? "0"}
              mono
              tone={countTone(item, counts[item] ?? "0")}
            />
          ))}
        </div>
      </section>

      <QueryPanel>
        <form className="toolbar" onSubmit={(event) => { event.preventDefault(); void load(false); }}>
          <label className="searchbox">
            <Search size={15} />
            <input value={q} onChange={(event) => setQ(event.target.value)} placeholder={"Application id, peer id, username or title"} />
          </label>
          <label className="field-inline">
            <span>{"Status"}</span>
            <select value={status} onChange={(event) => setStatus(event.target.value as StatusFilter)}>
              <option value="all">{"All statuses"}</option>
              {statuses.map((item) => (
                <option key={item} value={item}>{statusLabels[item]}</option>
              ))}
            </select>
          </label>
          <label className="field-inline">
            <span>{"Verifier"}</span>
            <VerifierOptions value={verifierBotID} verifiers={verifiers} onChange={setVerifierBotID} />
          </label>
          <label className="field-inline">
            <span>{"Peer type"}</span>
            <select value={peerType} onChange={(event) => setPeerType(event.target.value as PeerTypeFilter)}>
              <option value="all">{"All types"}</option>
              {peerTypes.map((item) => (
                <option key={item} value={item}>{peerTypeLabels[item]}</option>
              ))}
            </select>
          </label>
          <label className="field-inline">
            <span>{"Limit"}</span>
            <input className="small-input" value={limit} onChange={(event) => setLimit(event.target.value)} type="number" min="1" max="200" />
          </label>
          <button className="btn primary icon-text" type="submit" disabled={busy}>
            {busy ? <Loader2 size={15} className="spin" /> : <Search size={15} />} {"Search"}
          </button>
        </form>
      </QueryPanel>

      <div className="table-wrap">
        <table className="data-table">
          <thead>
            <tr>
              <th>{"ID"}</th>
              <th>{"Verifier"}</th>
              <th>{"Peer"}</th>
              <th>{"Applicant"}</th>
              <th>{"Stated reason"}</th>
              <th>{"Status"}</th>
              <th>{"Filed"}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => (
              <tr key={row.ID}>
                <td className="mono">
                  <button className="row-link" type="button" onClick={() => navigate(`/bot-verification/${row.ID}`)}>
                    #{row.ID}
                  </button>
                </td>
                <td>
                  <strong>{displayUsername(row.VerifierBotUsername) || row.VerifierBotID}</strong>
                  <div className="entity-subtitle mono">{row.VerifierBotID}</div>
                </td>
                <td>
                  <strong>{peerLabel(row)}</strong>
                  <div className="entity-subtitle mono">
                    {peerTypeLabels[row.PeerType]} · {row.PeerID}
                  </div>
                </td>
                <td>
                  {displayUsername(row.ApplicantUsername) || "-"}
                  <div className="entity-subtitle mono">{row.ApplicantUserID}</div>
                </td>
                <td className="truncate">{row.Reason || "-"}</td>
                <td><RequestStatusBadge status={row.Status} /></td>
                <td>{formatDate(row.CreatedAt) || "-"}</td>
                <td>
                  <button className="row-link" type="button" onClick={() => navigate(`/bot-verification/${row.ID}`)}>
                    <Stamp size={14} /> {"Details"} <ChevronRight size={14} />
                  </button>
                </td>
              </tr>
            ))}
            {rows.length === 0 && <EmptyRow colSpan={8} />}
          </tbody>
        </table>
      </div>
      {hasMore && (
        <div className="toolbar">
          <button className="btn icon-text" type="button" onClick={() => load(true)} disabled={busy}>
            {busy ? <Loader2 size={15} className="spin" /> : <ChevronDown size={15} />} {"Load more"}
          </button>
        </div>
      )}
    </>
  );
}

// ---------------------------------------------------------------------------
// Verifiers
// ---------------------------------------------------------------------------

function VerifiersBlock({
  verifiers,
  icons,
  canManage,
  onChanged,
  navigate
}: {
  verifiers: BotVerifierRow[];
  icons: VerificationIconRow[];
  canManage: boolean;
  onChanged: () => void;
  navigate: Navigate;
}) {
  const [bot, setBot] = useState<BotRow | null>(null);
  // editing carries the bot id of the row being updated: the grant endpoint is an
  // upsert, and version is the optimistic lock of the row it overwrites. A fresh
  // grant sends "0", which is what "there is no row yet" means.
  const [editing, setEditing] = useState<BotVerifierRow | null>(null);
  const [iconDocumentID, setIconDocumentID] = useState("");
  const [company, setCompany] = useState("");
  const [defaultDescription, setDefaultDescription] = useState("");
  const [canModify, setCanModify] = useState(false);
  const activeIcons = icons.filter((icon) => icon.Active);
  // A verifier can hold an icon the operator has since retired. Editing that row must
  // not silently swap the icon just because the select has no matching option, so the
  // current document is kept in the list and labelled instead.
  const iconOptions: Array<{ value: string; label: string }> = activeIcons.map((icon) => ({
    value: icon.DocumentID,
    label: `${icon.Name} · ${icon.DocumentID}`
  }));
  if (iconDocumentID && !iconOptions.some((option) => option.value === iconDocumentID)) {
    const retired = icons.find((icon) => icon.DocumentID === iconDocumentID);
    iconOptions.unshift({
      value: iconDocumentID,
      label: `${retired?.Name ?? iconDocumentID} · ${iconDocumentID} (${"Retired"})`
    });
  }

  function startEdit(row: BotVerifierRow) {
    setEditing(row);
    setBot(null);
    setIconDocumentID(row.IconDocumentID);
    setCompany(row.CompanyName);
    setDefaultDescription(row.DefaultDescription);
    setCanModify(row.CanModifyCustomDescription);
  }

  function resetForm() {
    setEditing(null);
    setBot(null);
    setIconDocumentID("");
    setCompany("");
    setDefaultDescription("");
    setCanModify(false);
  }

  // int64 fields go out as decimal strings (the backend tags them `,string`), which
  // is also the shape they arrived in, so nothing is re-parsed on the way back.
  function grantPayload(): Record<string, unknown> {
    const botID = editing ? editing.BotID : bot ? String(bot.ID) : "0";
    return {
      bot_id: botID,
      icon_document_id: iconDocumentID || "0",
      company_name: company.trim(),
      default_description: defaultDescription.trim(),
      can_modify_custom_description: canModify,
      version: editing ? editing.Version : "0"
    };
  }

  return (
    <>
      {canManage && (
        <section className="section-block">
          <SectionHead
            title={editing ? "Update verifier" : "Grant verifier status"}
            text={"The bot gets an icon from the catalogue and a company name to vouch under. The same call updates an existing verifier, which is why it carries a version."}
            action={
              editing ? (
                <button className="btn icon-text" type="button" onClick={resetForm}>
                  {"Cancel update"}
                </button>
              ) : undefined
            }
          />
          {editing ? (
            <p className="bot-create-note">
              {`Updating ${displayUsername(editing.BotUsername) || editing.BotID} — version ${editing.Version} is sent as the optimistic lock, so a row somebody else changed meanwhile is refused instead of overwritten.`}
            </p>
          ) : (
            <BotPicker label={"Bot"} value={bot} onChange={setBot} />
          )}
          <div className="bot-create-fields">
            <label className="duration-field">
              <span>{"Icon from the catalogue"}</span>
              <select value={iconDocumentID} onChange={(event) => setIconDocumentID(event.target.value)}>
                <option value="">{"Pick an icon"}</option>
                {iconOptions.map((option) => (
                  <option key={option.value} value={option.value}>{option.label}</option>
                ))}
              </select>
            </label>
            <label className="duration-field">
              <span>{"Company"}</span>
              <input
                value={company}
                onChange={(event) => setCompany(event.target.value)}
                placeholder={"Acme Verification Ltd"}
              />
            </label>
            <label className="duration-field">
              <span>{"Default description"}</span>
              <input
                value={defaultDescription}
                onChange={(event) => setDefaultDescription(event.target.value)}
                placeholder={"Verified by Acme"}
              />
            </label>
          </div>
          <label className="checkline">
            <input type="checkbox" checked={canModify} onChange={(event) => setCanModify(event.target.checked)} />
            {"The verifier may replace the description per peer"}
          </label>
          <p className="bot-create-note">{"This is botVerifierSettings.can_modify_custom_description: with it off, every mark this verifier grants carries the default description above, whatever the applicant asked for."}</p>
          {activeIcons.length === 0 && <Alert>{"The catalogue has no active icon, so there is nothing to grant. Add one in the icon catalogue first."}</Alert>}
          <div className="bot-create-actions">
            <span className="bot-create-note">{"The bot can mark peers as soon as the row exists and is enabled."}</span>
            <ActionButton
              label={editing ? "Update verifier" : "Grant verifier status"}
              icon={<Plus size={15} />}
              tone="neutral"
              path="/api/actions/grant-bot-verifier"
              payload={grantPayload}
              onDone={() => {
                resetForm();
                onChanged();
              }}
            />
          </div>
        </section>
      )}

      <section className="section-block">
        <SectionHead
          title={"Verifier bots"}
          text={"Bots allowed to hand out their own mark. Verifier status is granted per deployment, so every row here is a badge printer an operator switched on by hand."}
          action={
            <button className="btn icon-text" type="button" onClick={onChanged}>
              <RefreshCw size={15} /> {"Refresh"}
            </button>
          }
        />
        <div className="table-wrap">
          <table className="data-table">
            <thead>
              <tr>
                <th>{"Bot"}</th>
                <th>{"Company"}</th>
                <th>{"Icon"}</th>
                <th>{"Own description"}</th>
                <th>{"Status"}</th>
                <th>{"Marks"}</th>
                <th>{"Granted by"}</th>
                <th>{"Updated"}</th>
                {canManage && <th></th>}
              </tr>
            </thead>
            <tbody>
              {verifiers.map((row) => (
                <tr key={row.BotID}>
                  <td>
                    <button className="row-link" type="button" onClick={() => navigate(`/bots/${row.BotID}`)}>
                      <strong>{displayUsername(row.BotUsername) || row.BotName || row.BotID}</strong>
                    </button>
                    <div className="entity-subtitle mono">{row.BotID}</div>
                  </td>
                  <td>
                    <strong>{row.CompanyName || "-"}</strong>
                    <div className="entity-subtitle truncate">{row.DefaultDescription || "Not set"}</div>
                  </td>
                  <td>
                    {row.IconName || "-"}
                    <div className="entity-subtitle mono">{row.IconDocumentID}</div>
                  </td>
                  <td>{row.CanModifyCustomDescription ? "Yes" : "No"}</td>
                  <td>
                    {row.Enabled
                      ? <Badge tone="good">{"Enabled"}</Badge>
                      : <Badge tone="warn">{"disabled"}</Badge>}
                  </td>
                  <td className="mono">{String(row.MarkCount ?? "0")}</td>
                  <td>
                    {row.GrantedBy || "-"}
                    <div className="entity-subtitle truncate">{row.GrantReason || "-"}</div>
                  </td>
                  <td>{formatDate(row.UpdatedAt) || "-"}</td>
                  {canManage && (
                    <td>
                      <div className="row-actions">
                        <button className="btn compact-btn" type="button" onClick={() => startEdit(row)}>
                          {"Edit"}
                        </button>
                        <ActionButton
                          label={row.Enabled ? "Disable" : "Enable"}
                          icon={row.Enabled ? <PowerOff size={14} /> : <Power size={14} />}
                          tone={row.Enabled ? "warn" : "neutral"}
                          compact
                          path="/api/actions/set-bot-verifier-enabled"
                          payload={() => ({ bot_id: row.BotID, enabled: !row.Enabled })}
                          onDone={onChanged}
                        />
                        <ActionButton
                          label={"Revoke status"}
                          icon={<Trash2 size={14} />}
                          tone="danger"
                          compact
                          path="/api/actions/revoke-bot-verifier"
                          payload={() => ({ bot_id: row.BotID })}
                          onDone={onChanged}
                        />
                      </div>
                    </td>
                  )}
                </tr>
              ))}
              {verifiers.length === 0 && <EmptyRow colSpan={canManage ? 9 : 8} />}
            </tbody>
          </table>
        </div>
        <p className="bot-create-note">{"Disabling is the per-verifier kill switch: the marks already granted keep rendering, but the bot can no longer mark anything new and its settings stop being projected into botInfo."}</p>
        <p className="bot-create-note">{"Revoking verifier status removes the row and every mark this verifier granted — the icon disappears from all of its peers at once."}</p>
      </section>
    </>
  );
}

// ---------------------------------------------------------------------------
// Icon catalogue
// ---------------------------------------------------------------------------

function IconsBlock({
  icons,
  verifiers,
  canManage,
  onChanged
}: {
  icons: VerificationIconRow[];
  verifiers: BotVerifierRow[];
  canManage: boolean;
  onChanged: () => void;
}) {
  const [documentID, setDocumentID] = useState("");
  const [name, setName] = useState("");
  const [ownerBotID, setOwnerBotID] = useState("");

  // owner_bot_id is omitted entirely for a shared entry rather than sent as "" —
  // `,string,omitempty` cannot decode an empty string.
  function iconPayload(): Record<string, unknown> {
    const payload: Record<string, unknown> = {
      document_id: documentID.trim() || "0",
      name: name.trim()
    };
    if (ownerBotID) payload.owner_bot_id = ownerBotID;
    return payload;
  }

  return (
    <>
      {canManage && (
        <section className="section-block">
          <SectionHead title={"Add or rename an icon"} text={"Search and pick any custom-emoji document already on this deployment (including bundled/system ones). Adding an id that already exists renames it instead of duplicating it."} />
          <EmojiPicker label={"Document"} value={documentID} onChange={setDocumentID} />
          <div className="bot-create-fields">
            <label className="duration-field">
              <span>{"Name"}</span>
              <input
                value={name}
                onChange={(event) => setName(event.target.value)}
                placeholder={"Acme blue tick"}
              />
            </label>
            <label className="duration-field">
              <span>{"Owner"}</span>
              <select value={ownerBotID} onChange={(event) => setOwnerBotID(event.target.value)}>
                <option value="">{"Shared"}</option>
                {verifiers.map((row) => (
                  <option key={row.BotID} value={row.BotID}>
                    {`${row.CompanyName || row.BotID} · ${displayUsername(row.BotUsername) || row.BotID}`}
                  </option>
                ))}
              </select>
            </label>
          </div>
          <p className="bot-create-note">{"A document id that resolves to nothing produces an invisible badge: the peer is marked in the database and the client draws nothing."}</p>
          <p className="bot-create-note">{"A shared icon may be granted to any verifier; picking an owner reserves it for that one bot."}</p>
          <div className="bot-create-actions">
            <span className="bot-create-note">{"Adding an icon grants nothing by itself — it only makes the document available to grant."}</span>
            <ActionButton
              label={"Save icon"}
              icon={<Plus size={15} />}
              tone="neutral"
              path="/api/actions/upsert-verification-icon"
              payload={iconPayload}
              onDone={() => {
                setDocumentID("");
                setName("");
                setOwnerBotID("");
                onChanged();
              }}
            />
          </div>
        </section>
      )}

      <section className="section-block">
        <SectionHead
          title={"Icon catalogue"}
          text={"The custom emoji documents a verifier may mark with. Nothing else can be used as an icon, so the catalogue is where a wrong badge is prevented rather than fixed."}
          action={
            <button className="btn icon-text" type="button" onClick={onChanged}>
              <RefreshCw size={15} /> {"Refresh"}
            </button>
          }
        />
        <div className="table-wrap">
          <table className="data-table">
            <thead>
              <tr>
                <th>{"Document ID"}</th>
                <th>{"Name"}</th>
                <th>{"Owner"}</th>
                <th>{"Status"}</th>
                <th>{"Verifiers using it"}</th>
                <th>{"Filed"}</th>
                {canManage && <th></th>}
              </tr>
            </thead>
            <tbody>
              {icons.map((row) => (
                <tr key={row.ID}>
                  <td className="mono">{row.DocumentID}</td>
                  <td><strong>{row.Name || "-"}</strong></td>
                  <td>
                    {row.OwnerBotID && row.OwnerBotID !== "0"
                      ? <>
                          {displayUsername(row.OwnerBotUsername) || row.OwnerBotID}
                          <div className="entity-subtitle mono">{row.OwnerBotID}</div>
                        </>
                      : <Badge>{"Shared"}</Badge>}
                  </td>
                  <td>
                    {row.Active
                      ? <Badge tone="good">{"Active"}</Badge>
                      : <Badge tone="warn">{"Retired"}</Badge>}
                  </td>
                  <td className="mono">{String(row.UsedByVerifiers ?? "0")}</td>
                  <td>{formatDate(row.CreatedAt) || "-"}</td>
                  {canManage && (
                    <td>
                      <div className="row-actions">
                        <ActionButton
                          label={row.Active ? "Retire" : "Activate"}
                          icon={row.Active ? <PowerOff size={14} /> : <Power size={14} />}
                          tone={row.Active ? "warn" : "neutral"}
                          compact
                          path="/api/actions/set-verification-icon-active"
                          payload={() => ({ icon_id: row.ID, active: !row.Active })}
                          onDone={onChanged}
                        />
                      </div>
                    </td>
                  )}
                </tr>
              ))}
              {icons.length === 0 && <EmptyRow colSpan={canManage ? 7 : 6} />}
            </tbody>
          </table>
        </div>
        <p className="bot-create-note">{"Retiring an icon stops it from being granted to anybody new. Marks already carrying it keep it: the icon is copied onto the mark when it is granted."}</p>
      </section>
    </>
  );
}

// ---------------------------------------------------------------------------
// Granted marks
// ---------------------------------------------------------------------------

function MarksBlock({
  verifiers,
  canManage,
  navigate
}: {
  verifiers: BotVerifierRow[];
  canManage: boolean;
  navigate: Navigate;
}) {
  const [verifierBotID, setVerifierBotID] = useState("");
  const [peerType, setPeerType] = useState<PeerTypeFilter>("all");
  const [q, setQ] = useState("");
  const [limit, setLimit] = useState("50");
  const [rows, setRows] = useState<CustomVerificationRow[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [cursor, setCursor] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function load(next = false) {
    setBusy(true);
    setError("");
    const params = new URLSearchParams({ limit });
    if (verifierBotID) params.set("verifier_bot_id", verifierBotID);
    if (peerType !== "all") params.set("peer_type", peerType);
    if (q.trim()) params.set("q", q.trim().replace(/^@/, ""));
    if (next && cursor) params.set("before_id", cursor);
    try {
      const result = await api.customVerifications(params);
      const page = result.rows ?? [];
      setRows((current) => (next ? [...current, ...page] : page));
      setCursor(result.next_before_id ?? "");
      setHasMore(Boolean(result.has_more));
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    void load(false);
  }, []);

  return (
    <>
      <section className="section-block">
        <SectionHead
          title={"Granted marks"}
          text={"Every peer currently carrying a third-party mark, whoever granted it — an operator decision, the verifier bot itself, or the peer's owner through bots.setCustomVerification."}
          action={
            <button className="btn icon-text" type="button" onClick={() => load(false)} disabled={busy}>
              <RefreshCw size={15} className={busy ? "spin" : ""} /> {"Refresh"}
            </button>
          }
        />
        {error && <Alert>{error}</Alert>}
      </section>

      <QueryPanel>
        <form className="toolbar" onSubmit={(event) => { event.preventDefault(); void load(false); }}>
          <label className="searchbox">
            <Search size={15} />
            <input value={q} onChange={(event) => setQ(event.target.value)} placeholder={"Peer id, username or title"} />
          </label>
          <label className="field-inline">
            <span>{"Verifier"}</span>
            <VerifierOptions value={verifierBotID} verifiers={verifiers} onChange={setVerifierBotID} />
          </label>
          <label className="field-inline">
            <span>{"Peer type"}</span>
            <select value={peerType} onChange={(event) => setPeerType(event.target.value as PeerTypeFilter)}>
              <option value="all">{"All types"}</option>
              {peerTypes.map((item) => (
                <option key={item} value={item}>{peerTypeLabels[item]}</option>
              ))}
            </select>
          </label>
          <label className="field-inline">
            <span>{"Limit"}</span>
            <input className="small-input" value={limit} onChange={(event) => setLimit(event.target.value)} type="number" min="1" max="200" />
          </label>
          <button className="btn primary icon-text" type="submit" disabled={busy}>
            {busy ? <Loader2 size={15} className="spin" /> : <Search size={15} />} {"Search"}
          </button>
        </form>
      </QueryPanel>

      <div className="table-wrap">
        <table className="data-table">
          <thead>
            <tr>
              <th>{"ID"}</th>
              <th>{"Verifier"}</th>
              <th>{"Peer"}</th>
              <th>{"Description"}</th>
              <th>{"Icon"}</th>
              <th>{"Filed"}</th>
              {canManage && <th></th>}
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => (
              <tr key={row.ID}>
                <td className="mono">#{row.ID}</td>
                <td>
                  <strong>{row.CompanyName || displayUsername(row.VerifierBotUsername) || row.VerifierBotID}</strong>
                  <div className="entity-subtitle mono">
                    {displayUsername(row.VerifierBotUsername) || row.VerifierBotID}
                  </div>
                </td>
                <td>
                  <button className="row-link" type="button" onClick={() => navigate(peerHref(row.PeerType, row.PeerID))}>
                    <strong>{peerLabel(row)}</strong>
                  </button>
                  <div className="entity-subtitle mono">
                    {peerTypeLabels[row.PeerType]} · {row.PeerID}
                  </div>
                </td>
                <td className="truncate">{row.Description || "Not set"}</td>
                <td className="mono">{row.IconDocumentID}</td>
                <td>{formatDate(row.CreatedAt) || "-"}</td>
                {canManage && (
                  <td>
                    <div className="row-actions">
                      <ActionButton
                        label={"Remove mark"}
                        icon={<Ban size={14} />}
                        tone="danger"
                        compact
                        path="/api/actions/revoke-custom-verification"
                        payload={() => ({
                          verifier_bot_id: row.VerifierBotID,
                          peer_type: row.PeerType,
                          peer_id: row.PeerID
                        })}
                        onDone={() => load(false)}
                      />
                    </div>
                  </td>
                )}
              </tr>
            ))}
            {rows.length === 0 && <EmptyRow colSpan={canManage ? 7 : 6} />}
          </tbody>
        </table>
      </div>
      <p className="bot-create-note">{"Removing a mark clears the icon and the description from the peer. The application it came from keeps its history."}</p>
      {hasMore && (
        <div className="toolbar">
          <button className="btn icon-text" type="button" onClick={() => load(true)} disabled={busy}>
            {busy ? <Loader2 size={15} className="spin" /> : <ChevronDown size={15} />} {"Load more"}
          </button>
        </div>
      )}
    </>
  );
}

// ---------------------------------------------------------------------------
// Shared bits
// ---------------------------------------------------------------------------

// The verifier filter lists the roster rather than asking for a bot id: a company
// name is what an operator reads in the queue, and a disabled verifier still owns
// rows worth filtering by, so it stays in the list and is labelled instead.
function VerifierOptions({
  value,
  verifiers,
  onChange
}: {
  value: string;
  verifiers: BotVerifierRow[];
  onChange: (value: string) => void;
}) {
  return (
    <select value={value} onChange={(event) => onChange(event.target.value)}>
      <option value="">{"All verifiers"}</option>
      {verifiers.map((row) => (
        <option key={row.BotID} value={row.BotID}>
          {`${row.CompanyName || row.BotID} · ${displayUsername(row.BotUsername) || row.BotID}`
            + (row.Enabled ? "" : ` (${"disabled"})`)}
        </option>
      ))}
    </select>
  );
}

export function RequestStatusBadge({ status }: { status: CustomVerificationRequestStatus }) {
  return <Badge tone={statusTone(status)}>{statusLabels[status]}</Badge>;
}

export function statusTone(status: CustomVerificationRequestStatus): "neutral" | "good" | "warn" | "danger" {
  if (status === "approved") return "good";
  if (status === "pending") return "warn";
  if (status === "rejected") return "danger";
  return "neutral";
}

// pending is the only status that waits for somebody, so it is the only one
// highlighted — and only while something actually sits in it.
function countTone(status: CustomVerificationRequestStatus, count: string): "neutral" | "good" | "warn" {
  if (status === "pending") return count !== "0" && count !== "" ? "warn" : "neutral";
  return status === "approved" ? "good" : "neutral";
}

export function peerLabel(row: { PeerUsername: string; PeerTitle: string; PeerID: string }): string {
  return displayUsername(row.PeerUsername) || row.PeerTitle || `#${row.PeerID}`;
}

// The panel page that owns the peer type. A third-party mark can sit on an ordinary
// account or on a bot — both are user rows, so both open the account page.
export function peerHref(peerType: BotVerificationPeerType, peerID: string): string {
  return peerType === "channel" ? `/channels/${peerID}` : `/accounts/${peerID}`;
}

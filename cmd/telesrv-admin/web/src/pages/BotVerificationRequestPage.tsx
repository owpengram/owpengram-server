import {
  ArrowLeft,
  BadgeCheck,
  Ban,
  Building2,
  CheckCircle2,
  ExternalLink,
  RefreshCw,
  ShieldOff,
  Stamp,
  User,
  XCircle
} from "../components/icons";
import { useEffect, useState, type ReactNode } from "react";
import { api, APIError, errorMessage } from "../api";
import { ActionButton } from "../components/ActionButton";
import { Alert, Badge, LoadingSurface, PageFrame, SectionHead, SplitLayout, Summary } from "../components/ui";
import { displayUsername, formatDate } from "../lib/format";
import type { Navigate } from "../routing";
import type { BotVerifierRow, CustomVerificationRequestDetail } from "../types";
import { RequestStatusBadge, peerHref, peerLabel, peerTypeLabels, statusLabels } from "./BotVerificationPage";

export function BotVerificationRequestPage({ id, navigate }: { id: string; navigate: Navigate }) {
  const [detail, setDetail] = useState<CustomVerificationRequestDetail | null>(null);
  const [note, setNote] = useState("");
  const [conflict, setConflict] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function load() {
    setBusy(true);
    setError("");
    try {
      setDetail(await api.customVerificationRequest(id));
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  function refresh() {
    setConflict(false);
    void load();
  }

  useEffect(() => {
    void load();
  }, [id]);

  // 409 is the one failure the operator cannot fix by editing the form: another
  // admin decided against the version this page read. The panel says so in plain
  // words and reloads, so the next attempt carries the current version.
  function handleActionError(err: unknown): string | undefined {
    if (err instanceof APIError && err.status === 409) {
      setConflict(true);
      void load();
      return "Another admin has already changed this application. The data has been reloaded — check the status before deciding again.";
    }
    return undefined;
  }

  if (error && !detail) {
    return <Alert>{error}</Alert>;
  }
  if (!detail) {
    return <LoadingSurface label={"Loading the application…"} />;
  }

  const request = detail.request;
  const verifier = liveVerifier(detail.verifier);
  const markActive = detail.mark_active;
  const canDecide = request.Status === "pending";
  const canRevoke = request.Status === "approved";
  const trimmedNote = note.trim();
  // What the mark would actually say: the applicant's wording only when this
  // verifier is allowed to override its own default, otherwise the default. Same
  // rule the backend applies (BotVerifierSettings.DescriptionFor), shown here so a
  // reviewer is not surprised by the text that ends up in the profile.
  const requestedDescription = request.RequestedDescription.trim();
  const descriptionAllowed = Boolean(verifier?.CanModifyCustomDescription) && requestedDescription !== "";
  const effectiveDescription = descriptionAllowed
    ? requestedDescription
    : (verifier?.DefaultDescription ?? "").trim();

  // version is the optimistic-locking token: it goes with every decision, as the
  // decimal string it arrived as, so a stale page cannot overwrite a fresh one.
  function decisionPayload(): Record<string, unknown> {
    const payload: Record<string, unknown> = { version: request.Version };
    if (trimmedNote) payload.internal_note = trimmedNote;
    return payload;
  }

  function afterDecision() {
    setNote("");
    setConflict(false);
    void load();
  }

  return (
    <PageFrame
      title={`Application #${request.ID}`}
      eyebrow={"Third-party verification / Review"}
      actions={
        <>
          <button className="btn icon-text" type="button" onClick={() => navigate("/bot-verification")}>
            <ArrowLeft size={15} /> {"Back to list"}
          </button>
          <button className="btn icon-text" type="button" onClick={refresh} disabled={busy}>
            <RefreshCw size={15} className={busy ? "spin" : ""} /> {"Refresh"}
          </button>
        </>
      }
    >
      {error && <Alert>{error}</Alert>}
      {conflict && <Alert>{"Another admin has already changed this application. The data has been reloaded — check the status before deciding again."}</Alert>}
      <SplitLayout
        main={
          <div className="stacked-sections">
            <section className="entity-head">
              <div>
                <div className="entity-title">{peerLabel(request)}</div>
                <div className="entity-subtitle mono">
                  #{request.ID} · {peerTypeLabels[request.PeerType]}:{request.PeerID} · v{request.Version}
                </div>
              </div>
              <div className="entity-badges">
                <RequestStatusBadge status={request.Status} />
                {markActive
                  ? <Badge tone="good"><BadgeCheck size={12} /> {"Mark is live"}</Badge>
                  : <Badge tone="neutral">{"No mark on the peer"}</Badge>}
              </div>
            </section>

            {/* Repeated on the detail page on purpose: the decision an operator is
                about to take grants a company's icon, not the platform badge. */}
            <section className="section-block">
              <SectionHead title={"A verifier company's icon — not the official checkmark"} text={"A third-party mark is a verifier bot's own icon, drawn right BEFORE the name of an account, a bot or a channel, plus one line of description in the profile. It says “this verifier vouches for this peer”, and nothing more."} />
              <p className="bot-create-note">{"The icon is a custom emoji document. The client fetches it through messages.getCustomEmojiDocuments, so a document id that resolves to nothing renders as no badge at all — which is why marks are granted from the catalogue below rather than from a typed number."}</p>
            </section>

            <section className="section-block">
              <SectionHead
                title={"Verifier"}
                text={"The company whose icon the peer would carry, as its row stands right now."}
                action={
                  <button className="btn icon-text" type="button" onClick={() => navigate(`/bots/${request.VerifierBotID}`)}>
                    <Building2 size={15} /> {"Open verifier bot"}
                  </button>
                }
              />
              <div className="summary-grid">
                <Summary label={"Company"} value={verifier?.CompanyName || "-"} />
                <Summary label={"Bot"} value={displayUsername(request.VerifierBotUsername) || "-"} />
                <Summary label={"Verifier bot ID"} value={request.VerifierBotID} mono />
                <Summary label={"Document ID"} value={verifier?.IconDocumentID || "-"} mono />
                <Summary label={"Name"} value={verifier?.IconName || "-"} />
                <Summary
                  label={"Own description"}
                  value={verifier?.CanModifyCustomDescription ? "Yes" : "No"}
                />
              </div>
              <FieldBlock label={"Default description"}>
                {verifier?.DefaultDescription
                  ? <p className="about-text">{verifier.DefaultDescription}</p>
                  : <p className="bot-create-note">{"Not set"}</p>}
              </FieldBlock>
              {!verifier && <Alert>{"The verifier row is gone: its status was revoked after this application was filed. There is no icon to grant, so the application can only be rejected."}</Alert>}
              {verifier && !verifier.Enabled && <Alert>{"This verifier is disabled. It cannot mark anything new until an operator enables it again."}</Alert>}
            </section>

            <section className="section-block">
              <SectionHead
                title={"Peer"}
                text={"The account, bot or channel the icon would be attached to."}
                action={
                  <button
                    className="btn icon-text"
                    type="button"
                    onClick={() => navigate(peerHref(request.PeerType, request.PeerID))}
                  >
                    <ExternalLink size={15} /> {"Open peer"}
                  </button>
                }
              />
              <div className="summary-grid">
                <Summary label={"Type"} value={peerTypeLabels[request.PeerType]} />
                <Summary label={"Username"} value={displayUsername(request.PeerUsername) || "-"} />
                <Summary label={"Title"} value={request.PeerTitle || "-"} />
                <Summary label={"Peer ID"} value={request.PeerID} mono />
              </div>
            </section>

            <section className="section-block">
              <SectionHead
                title={"Applicant"}
                text={"Who filed the application with the verifier bot."}
                action={
                  <button
                    className="btn icon-text"
                    type="button"
                    onClick={() => navigate(`/accounts/${request.ApplicantUserID}`)}
                  >
                    <User size={15} /> {"Open account"}
                  </button>
                }
              />
              <div className="summary-grid">
                <Summary label={"Username"} value={displayUsername(request.ApplicantUsername) || "-"} />
                <Summary label={"User ID"} value={request.ApplicantUserID} mono />
                <Summary label={"Filed"} value={formatDate(request.CreatedAt) || "-"} />
                <Summary label={"Updated"} value={formatDate(request.UpdatedAt) || "-"} />
              </div>
            </section>

            <section className="section-block">
              <SectionHead title={"Application"} text={"What the applicant wrote, rendered as plain text."} />
              <div className="stacked-sections">
                <div className="summary-grid">
                  <Summary label={"Correlation ID"} value={request.CorrelationID || "-"} mono />
                  <Summary label={"Status"} value={statusLabels[request.Status]} />
                </div>
                <FieldBlock label={"Stated reason"}>
                  {request.Reason
                    ? <p className="about-text">{request.Reason}</p>
                    : <p className="bot-create-note">{"Not set"}</p>}
                </FieldBlock>
                <FieldBlock label={"Requested description"}>
                  {requestedDescription
                    ? <p className="about-text">{requestedDescription}</p>
                    : <p className="bot-create-note">{"Not set"}</p>}
                </FieldBlock>
                <FieldBlock label={"Description the mark would carry"}>
                  {effectiveDescription
                    ? <p className="about-text">{effectiveDescription}</p>
                    : <p className="bot-create-note">{"Not set"}</p>}
                </FieldBlock>
                <p className="bot-create-note">{"Resolved the same way the backend resolves it: the applicant's wording only when this verifier may set its own description, otherwise the verifier's default."}</p>
                {requestedDescription !== "" && !descriptionAllowed && (
                  <p className="bot-create-note">{"This verifier may not set a per-peer description, so the requested wording is ignored and the default is applied."}</p>
                )}
              </div>
            </section>

            <section className="section-block">
              <SectionHead title={"Decision"} text={"What was decided, by whom, and with which wording."} />
              <div className="stacked-sections">
                <div className="summary-grid">
                  <Summary label={"Decided by"} value={request.DecidedBy || "-"} />
                  <Summary label={"Approved"} value={formatDate(request.ApprovedAt) || "-"} />
                  <Summary label={"Rejected"} value={formatDate(request.RejectedAt) || "-"} />
                  <Summary label={"Version (optimistic lock)"} value={request.Version} mono />
                </div>
                <FieldBlock label={"Decision reason"}>
                  {request.DecisionReason
                    ? <p className="about-text">{request.DecisionReason}</p>
                    : <p className="bot-create-note">{"No decision yet"}</p>}
                </FieldBlock>
                {/* The internal note is the operator handover text and is labelled as
                    admin-only wherever it appears. */}
                <FieldBlock label={`${"Internal note"} · ${"admins only"}`}>
                  {request.InternalNote
                    ? <p className="about-text">{request.InternalNote}</p>
                    : <p className="bot-create-note">{"Not set"}</p>}
                </FieldBlock>
              </div>
            </section>
          </div>
        }
        side={
          <section className="action-dock">
            <div className="dock-title"><Stamp size={14} /> {"Decision"}</div>
            {!canDecide && !canRevoke && <p className="bot-create-note">{"This status has no available actions."}</p>}
            {(canDecide || canRevoke) && (
              <>
                <label className="duration-field">
                  <span>{"Internal note"}</span>
                  <textarea
                    value={note}
                    onChange={(event) => setNote(event.target.value)}
                    rows={3}
                    placeholder={"Handover note for other admins"}
                  />
                </label>
                <p className="bot-create-note">{"Optional. Stored with the decision and visible to admins only — never sent to the applicant."}</p>
              </>
            )}
            {canDecide && (
              <>
                {!verifier && <Alert>{"The verifier row is gone: its status was revoked after this application was filed. There is no icon to grant, so the application can only be rejected."}</Alert>}
                {verifier && !verifier.Enabled && <Alert>{"This verifier is disabled. It cannot mark anything new until an operator enables it again."}</Alert>}
                {markActive && <p className="bot-create-note">{"This peer already carries this verifier's mark; approving refreshes the description and records the decision."}</p>}
                <div className="action-stack">
                  <ActionButton
                    label={"Approve"}
                    icon={<CheckCircle2 size={15} />}
                    tone="neutral"
                    path={`/api/botverification/requests/${request.ID}/approve`}
                    payload={decisionPayload}
                    onDone={afterDecision}
                    onError={handleActionError}
                  />
                  <ActionButton
                    label={"Reject"}
                    icon={<XCircle size={15} />}
                    tone="warn"
                    path={`/api/botverification/requests/${request.ID}/reject`}
                    payload={decisionPayload}
                    onDone={afterDecision}
                    onError={handleActionError}
                  />
                </div>
                <p className="bot-create-note">{"Puts the verifier's icon before the peer's name and its description in the profile, and messages the applicant."}</p>
                <p className="bot-create-note">{"The reason is mandatory: it is the wording the applicant is told, so write what exactly was missing."}</p>
              </>
            )}
            {canRevoke && (
              <>
                <div className="dock-title"><ShieldOff size={14} /> {"Danger zone"}</div>
                <div className="danger-zone">
                  <ActionButton
                    label={"Revoke mark"}
                    icon={<Ban size={15} />}
                    tone="danger"
                    path={`/api/botverification/requests/${request.ID}/revoke`}
                    payload={decisionPayload}
                    onDone={afterDecision}
                    onError={handleActionError}
                  />
                  <p className="bot-create-note">{"Takes the icon and the description off the peer and closes the application as revoked. The official checkmark, if the peer has one, is untouched."}</p>
                  {!markActive && <p className="bot-create-note">{"The peer carries no mark right now — revoking only closes the application."}</p>}
                </div>
              </>
            )}
          </section>
        }
      />
    </PageFrame>
  );
}

function FieldBlock({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="duration-field">
      <span>{label}</span>
      {children}
    </div>
  );
}

// A verifier whose row was revoked after the application was filed can come back as
// null or as a zeroed record, depending on how the backend renders "gone". Both mean
// the same thing to a reviewer, so they collapse into one absent value here.
function liveVerifier(row: BotVerifierRow | null): BotVerifierRow | null {
  if (!row) return null;
  if (!row.BotID || row.BotID === "0") return null;
  return row;
}

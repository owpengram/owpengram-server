import {
  ArrowLeft,
  BadgeCheck,
  Ban,
  CheckCircle2,
  ExternalLink,
  Handshake,
  RefreshCw,
  ShieldOff,
  User,
  XCircle
} from "../components/icons";
import { useEffect, useState, type ReactNode } from "react";
import { api, APIError, errorMessage } from "../api";
import { ActionButton } from "../components/ActionButton";
import { Alert, Badge, EmptyRow, LoadingSurface, PageFrame, SectionHead, SplitLayout, Summary } from "../components/ui";
import { displayUsername, formatDate, safeHttpURL } from "../lib/format";
import { permissionVerificationRevoke, usePermissions } from "../permissions";
import type { Navigate } from "../routing";
import type { VerificationApplicationDetail, VerificationEventKind } from "../types";
import {
  VerificationStatusBadge,
  targetHref,
  targetLabel,
  verificationStatusLabels,
  verificationTargetTypeLabels
} from "./VerificationPage";

const verificationEventKindLabels: Record<VerificationEventKind, string> = {
  created: "Created",
  updated: "Updated",
  submitted: "Submitted",
  claimed: "Claimed",
  approved: "Approved",
  rejected: "Rejected",
  cancelled: "Cancelled",
  revoked: "Badge revoked",
  notified: "Applicant notified"
};

export function VerificationDetailPage({ id, navigate }: { id: string; navigate: Navigate }) {
  const { can } = usePermissions();
  const [detail, setDetail] = useState<VerificationApplicationDetail | null>(null);
  const [note, setNote] = useState("");
  const [conflict, setConflict] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function load() {
    setBusy(true);
    setError("");
    try {
      setDetail(await api.verificationApplication(id));
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
  // reviewer decided against the version this page read. The panel says so in
  // plain words and reloads, so the next attempt carries the current version.
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

  const app = detail.application;
  const events = detail.events ?? [];
  const controls = detail.applicant_controls_target;
  const verified = detail.target_verified;
  const canClaim = app.Status === "submitted";
  const canDecide = app.Status === "submitted" || app.Status === "in_review";
  const canRevoke = app.Status === "approved" && can(permissionVerificationRevoke);
  const trimmedNote = note.trim();

  // version is the optimistic-locking token: it goes with every decision, as the
  // decimal string it arrived as, so a stale page cannot overwrite a fresh one.
  function decisionPayload(): Record<string, unknown> {
    const payload: Record<string, unknown> = { version: app.Version };
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
      title={`Application #${app.ID}`}
      eyebrow={"Verification / Review"}
      actions={
        <>
          <button className="btn icon-text" type="button" onClick={() => navigate("/verification")}>
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
                <div className="entity-title">{targetLabel(app)}</div>
                <div className="entity-subtitle mono">
                  #{app.ID} · {verificationTargetTypeLabels[app.TargetType]}:{app.TargetID} · v{app.Version}
                </div>
              </div>
              <div className="entity-badges">
                <VerificationStatusBadge status={app.Status} />
                {verified && <Badge tone="good"><BadgeCheck size={12} /> {"Badge already on"}</Badge>}
                <Badge tone={controls ? "good" : "danger"}>
                  {controls ? "Control confirmed" : "No control over the target"}
                </Badge>
              </div>
            </section>

            <section className="section-block">
              <SectionHead
                title={"Target"}
                text={"The peer the badge would be attached to, as it exists right now."}
                action={
                  <button className="btn icon-text" type="button" onClick={() => navigate(targetHref(app))}>
                    <ExternalLink size={15} /> {"Open target"}
                  </button>
                }
              />
              <div className="summary-grid">
                <Summary label={"Type"} value={verificationTargetTypeLabels[app.TargetType]} />
                <Summary label={"Username"} value={displayUsername(app.TargetUsername) || "-"} />
                <Summary label={"Title"} value={app.TargetTitle || "-"} />
                <Summary label={"Peer ID"} value={app.TargetID} mono />
              </div>
            </section>

            <section className="section-block">
              <SectionHead
                title={"Applicant"}
                text={"Who filed the application and whether they still hold rights on the target."}
                action={
                  <button className="btn icon-text" type="button" onClick={() => navigate(`/accounts/${app.ApplicantUserID}`)}>
                    <User size={15} /> {"Open account"}
                  </button>
                }
              />
              <div className="summary-grid">
                <Summary label={"Username"} value={displayUsername(app.ApplicantUsername) || "-"} />
                <Summary label={"Name"} value={app.ApplicantName || "-"} />
                <Summary label={"User ID"} value={app.ApplicantUserID} mono />
                <Summary label={"Submitted"} value={formatDate(app.SubmittedAt) || "-"} />
              </div>
              {controls
                ? <p className="bot-create-note">{"The applicant controls the target right now — checked against the live records, not against the submission snapshot."}</p>
                : <Alert>{"The applicant no longer controls the target. Approving would hand the badge to someone who does not hold the peer — normally a reason to reject."}</Alert>}
            </section>

            <section className="section-block">
              <SectionHead title={"Application"} text={"Everything the applicant submitted, rendered as plain text."} />
              <div className="stacked-sections">
                <div className="summary-grid">
                  <Summary label={"Category"} value={app.Category || "-"} />
                  <Summary label={"Correlation ID"} value={app.CorrelationID || "-"} mono />
                  <Summary label={"Created"} value={formatDate(app.CreatedAt) || "-"} />
                  <Summary label={"Updated"} value={formatDate(app.UpdatedAt) || "-"} />
                </div>
                <FieldBlock label={"Description"}>
                  {app.Description
                    ? <p className="about-text">{app.Description}</p>
                    : <p className="bot-create-note">{"Not provided"}</p>}
                </FieldBlock>
                <FieldBlock label={"Official website"}>
                  {app.OfficialWebsite
                    ? <div className="about-text"><SafeLink value={app.OfficialWebsite} /></div>
                    : <p className="bot-create-note">{"Not provided"}</p>}
                </FieldBlock>
                <FieldBlock label={"Social links"}>
                  <LinkList values={app.SocialLinks} />
                </FieldBlock>
                <FieldBlock label={"Press coverage"}>
                  <LinkList values={app.PressLinks} />
                </FieldBlock>
                <FieldBlock label={"Applicant comment"}>
                  {app.AdditionalNote
                    ? <p className="about-text">{app.AdditionalNote}</p>
                    : <p className="bot-create-note">{"Not provided"}</p>}
                </FieldBlock>
                <p className="bot-create-note">{"Only http:// and https:// links are clickable and open in a new tab; anything else is shown as text."}</p>
              </div>
            </section>

            <section className="section-block">
              <SectionHead title={"Decision"} text={"What was decided, by whom, and with which wording."} />
              <div className="stacked-sections">
                <div className="summary-grid">
                  <Summary label={"Reviewer"} value={app.ReviewerAdminID || "-"} />
                  <Summary label={"Decided"} value={formatDate(app.ReviewedAt) || "-"} />
                  <Summary label={"Status"} value={verificationStatusLabels[app.Status]} />
                  <Summary label={"Version (optimistic lock)"} value={app.Version} mono />
                </div>
                <FieldBlock label={"Decision reason"}>
                  {app.DecisionReason
                    ? <p className="about-text">{app.DecisionReason}</p>
                    : <p className="bot-create-note">{"No decision yet"}</p>}
                </FieldBlock>
                {/* The internal note is the reviewer handover text and is labelled
                    as admin-only wherever it appears. */}
                <FieldBlock label={`${"Internal note"} · ${"admins only"}`}>
                  {app.InternalNote
                    ? <p className="about-text">{app.InternalNote}</p>
                    : <p className="bot-create-note">{"Not provided"}</p>}
                </FieldBlock>
              </div>
            </section>

            <section className="section-block">
              <SectionHead title={"History"} text={"Immutable trail of every status transition, with actor and reason."} />
              <div className="table-wrap">
                <table className="data-table">
                  <thead>
                    <tr>
                      <th>{"Event"}</th>
                      <th>{"From → to"}</th>
                      <th>{"Actor"}</th>
                      <th>{"Reason"}</th>
                      <th>{"Internal note"}</th>
                      <th>{"Time"}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {events.map((row) => (
                      <tr key={row.ID}>
                        <td><EventKind kind={row.Kind} /></td>
                        <td className="mono">
                          {row.FromStatus || "-"} → {row.ToStatus || "-"}
                        </td>
                        <td>{row.Actor || "-"}</td>
                        <td className="truncate">{row.Reason || "-"}</td>
                        <td className="truncate">{row.Note || "-"}</td>
                        <td>{formatDate(row.CreatedAt) || "-"}</td>
                      </tr>
                    ))}
                    {events.length === 0 && <EmptyRow colSpan={6} />}
                  </tbody>
                </table>
              </div>
            </section>
          </div>
        }
        side={
          <section className="action-dock">
            <div className="dock-title">{"Review actions"}</div>
            {!canClaim && !canDecide && !canRevoke && (
              <p className="bot-create-note">{"This status has no available actions."}</p>
            )}
            {canClaim && (
              <>
                <div className="action-stack">
                  <ActionButton
                    label={"Take into review"}
                    icon={<Handshake size={15} />}
                    tone="neutral"
                    path={`/api/verification/applications/${app.ID}/claim`}
                    payload={() => ({ version: app.Version })}
                    onDone={afterDecision}
                    onError={handleActionError}
                  />
                </div>
                <p className="bot-create-note">{"Assigns the application to you and moves it to in review, so two reviewers never work on the same one."}</p>
              </>
            )}
            {/* One optional note field feeds every decision on this page,
                including a revoke. */}
            {(canDecide || canRevoke) && (
              <>
                <label className="duration-field">
                  <span>{"Internal note"}</span>
                  <textarea
                    value={note}
                    onChange={(event) => setNote(event.target.value)}
                    rows={3}
                    placeholder={"Handover note for other reviewers"}
                  />
                </label>
                <p className="bot-create-note">{"Optional. Stored with the decision and visible to admins only — never sent to the applicant."}</p>
              </>
            )}
            {canDecide && (
              <>
                {!controls && <Alert>{"The applicant no longer controls the target. Approving would hand the badge to someone who does not hold the peer — normally a reason to reject."}</Alert>}
                {verified && <p className="bot-create-note">{"The target already carries the badge; approving only records the decision."}</p>}
                <div className="action-stack">
                  <ActionButton
                    label={"Approve"}
                    icon={<CheckCircle2 size={15} />}
                    tone="neutral"
                    path={`/api/verification/applications/${app.ID}/approve`}
                    payload={decisionPayload}
                    onDone={afterDecision}
                    onError={handleActionError}
                  />
                  <ActionButton
                    label={"Reject"}
                    icon={<XCircle size={15} />}
                    tone="warn"
                    path={`/api/verification/applications/${app.ID}/reject`}
                    payload={decisionPayload}
                    onDone={afterDecision}
                    onError={handleActionError}
                  />
                </div>
                <p className="bot-create-note">{"Grants the official badge to the target and closes the application."}</p>
                <p className="bot-create-note">{"The reason is mandatory: it is the wording the applicant is told, so write what exactly was missing."}</p>
              </>
            )}
            {canRevoke && (
              <>
                <div className="dock-title"><ShieldOff size={14} /> {"Danger zone"}</div>
                <div className="danger-zone">
                  <ActionButton
                    label={"Revoke verification"}
                    icon={<Ban size={15} />}
                    tone="danger"
                    path="/api/actions/revoke-verification"
                    payload={() => {
                      // Revoke addresses the peer, not the application: the
                      // approved application stays approved as history.
                      const payload: Record<string, unknown> = {
                        target_type: app.TargetType,
                        target_id: app.TargetID
                      };
                      if (trimmedNote) payload.internal_note = trimmedNote;
                      return payload;
                    }}
                    onDone={afterDecision}
                    onError={handleActionError}
                  />
                  <p className="bot-create-note">{"Clears the badge from the target. The approved application stays in history."}</p>
                  {!verified && <p className="bot-create-note">{"The target carries no badge right now — there is nothing to revoke."}</p>}
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

// Applicant-supplied text is rendered as ordinary React children (escaped by
// React) and only ever linked when it is an http(s) URL. No markup from a
// submission reaches the DOM.
function SafeLink({ value }: { value: string }) {
  const href = safeHttpURL(value);
  if (!href) {
    return <span className="mono">{value}</span>;
  }
  return (
    <a className="row-link" href={href} target="_blank" rel="noopener noreferrer">
      {value} <ExternalLink size={13} />
    </a>
  );
}

function LinkList({ values }: { values: string[] | null }) {
  const links = (values ?? []).filter((item) => item.trim() !== "");
  if (links.length === 0) {
    return <p className="bot-create-note">{"Not provided"}</p>;
  }
  return (
    <div className="about-text">
      {links.map((item, index) => (
        <div key={`${index}-${item}`}><SafeLink value={item} /></div>
      ))}
    </div>
  );
}

function EventKind({ kind }: { kind: VerificationEventKind }) {
  const tone = kind === "approved"
    ? "good"
    : kind === "rejected" || kind === "revoked" || kind === "cancelled"
      ? "danger"
      : kind === "submitted" || kind === "claimed"
        ? "warn"
        : "neutral";
  return <Badge tone={tone}>{verificationEventKindLabels[kind]}</Badge>;
}

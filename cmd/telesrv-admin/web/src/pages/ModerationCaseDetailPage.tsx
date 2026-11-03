import { ArrowLeft, CheckCircle2, RefreshCw, ShieldCheck } from "../components/icons";
import { useEffect, useMemo, useState } from "react";
import { api, errorMessage } from "../api";
import { Alert, JsonBlock, LoadingSurface, PageFrame, SectionHead, SplitLayout, Summary } from "../components/ui";
import { formatDate } from "../lib/format";
import type { Navigate } from "../routing";
import type { ModerationCaseDetail, ModerationReport } from "../types";
import {
  CaseSeverity,
  CaseStatus,
  moderationEnumLabel,
  moderationTargetLabel
} from "./ModerationCasesPage";

type DecisionPreset = "no_violation" | "scam" | "fake" | "freeze" | "scam_freeze" | "fake_freeze" | "delete_messages" | "delete_account";

export function ModerationCaseDetailPage({ id, navigate }: { id: number; navigate: Navigate }) {
  const [detail, setDetail] = useState<ModerationCaseDetail | null>(null);
  const [report, setReport] = useState<ModerationReport | null>(null);
  const [reason, setReason] = useState("");
  const [preset, setPreset] = useState<DecisionPreset>("no_violation");
  const [messageIDs, setMessageIDs] = useState("");
  const [ownerUserID, setOwnerUserID] = useState("");
  const [revokeMessages, setRevokeMessages] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  function selectReport(next: ModerationReport | null) {
    setReport(next);
    if (!next) return;
    const ids = next.Items
      .filter((item) => item.Kind === "message")
      .map((item) => Number(item.ItemID))
      .filter((value) => Number.isSafeInteger(value) && value > 0);
    setMessageIDs(ids.join(", "));
    setOwnerUserID(String(next.ReporterUserID));
  }

  async function load() {
    setError("");
    try {
      const next = await api.moderationCase(id);
      setDetail(next);
      const reportID = next.ReportIDs[0];
      selectReport(reportID ? await api.moderationReport(reportID) : null);
    } catch (err) {
      setError(errorMessage(err));
    }
  }

  useEffect(() => {
    void load();
  }, [id]);

  const selectedActions = useMemo(
    () => actionsForPreset(
      preset,
      detail?.Case.Target.Type,
      parseMessageIDs(messageIDs),
      Number(ownerUserID),
      revokeMessages
    ),
    [preset, detail?.Case.Target.Type, messageIDs, ownerUserID, revokeMessages]
  );
  const appealRemedy = useMemo(
    () => detail ? requiredAppealRemedy(detail) : { actions: [], label: "None", blocked: false },
    [detail]
  );

  async function claim() {
    if (!detail) return;
    setBusy(true);
    setError("");
    try {
      await api.claimModerationCase(id, detail.Case.Version);
      await load();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function decide() {
    if (!detail || !reason.trim()) {
      setError("A review reason is required.");
      return;
    }
    if (preset === "delete_messages" && selectedActions.length === 0) {
      setError(detail.Case.Target.Type === "user"
        ? "Private-message deletion requires valid evidence message IDs and the reporter's owner_user_id."
        : "Channel-message deletion requires at least one valid evidence message ID.");
      return;
    }
    if (!window.confirm(`Submit the “${decisionPresetLabel(preset)}” decision? The action will run through the durable action queue.`)) return;
    setBusy(true);
    setError("");
    try {
      const result = await api.decideModerationCase(id, {
        expected_version: detail.Case.Version,
        reason: reason.trim(),
        kind: preset === "no_violation" ? "no_violation" : "violation",
        actions: selectedActions
      });
      setDetail(result.case);
      setReason("");
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function reviewAppeal(appealID: number, granted: boolean) {
    if (!detail || !reason.trim()) {
      setError("An appeal review reason is required.");
      return;
    }
    if (!window.confirm(granted ? "Grant this appeal?" : "Deny this appeal?")) return;
    setBusy(true);
    try {
      const result = await api.reviewModerationAppeal(id, appealID, {
        expected_version: detail.Case.Version,
        reason: reason.trim(),
        granted,
        actions: granted ? appealRemedy.actions : []
      });
      setDetail(result.case);
      setReason("");
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  if (error && !detail) return <Alert>{error}</Alert>;
  if (!detail) return <LoadingSurface label={"Loading moderation case…"} />;
  const item = detail.Case;
  const canClaim = item.Status === "open" || item.Status === "in_review" || item.Status === "appeal_review";
  const canDecide = (item.Status === "in_review" || item.Status === "action_failed") && Boolean(item.AssignedTo);
  const canSubmitDecision = canDecide && (item.Status !== "action_failed" || preset !== "no_violation");
  const pendingAppeal = detail.Appeals.find((appeal) => appeal.Status === "pending");

  return (
    <PageFrame
      title={`Review case #${item.ID}`}
      eyebrow={"Moderation / Case detail"}
      actions={
        <>
          <button className="btn icon-text" onClick={() => navigate("/moderation")}>
            <ArrowLeft size={15} /> {"Back to queue"}
          </button>
          <button className="btn icon-text" onClick={load}>
            <RefreshCw size={15} /> {"Refresh"}
          </button>
        </>
      }
    >
      {error && <Alert>{error}</Alert>}
      <SplitLayout
        main={
          <div className="stacked-sections">
            <section className="entity-head">
              <div>
                <div className="entity-title">{moderationTargetLabel(item.Target.Type, item.Target.ID)}</div>
                <div className="entity-subtitle">
                  {`Version ${item.Version} · Updated ${formatDate(item.UpdatedAt)}`}
                </div>
              </div>
              <div className="entity-badges">
                <CaseStatus status={item.Status} />
                <CaseSeverity value={item.Severity} />
              </div>
            </section>
            <div className="summary-grid">
              <Summary label={"Target"} value={moderationTargetLabel(item.Target.Type, item.Target.ID)} mono />
              <Summary
                label={"Reports"}
                value={`${item.ReportCount} reports from ${item.DistinctReporterCount} reporters`}
              />
              <Summary label={"Reviewer"} value={item.AssignedTo || "-"} />
              <Summary
                label={"First / latest report"}
                value={`${formatDate(item.FirstReportAt)} / ${formatDate(item.LastReportAt)}`}
              />
            </div>
            <section className="section-block">
              <SectionHead title={"Report evidence"} text={"Shows up to the latest 100 reports; snapshots are frozen when reports are admitted."} />
              <div className="toolbar">
                {detail.ReportIDs.map((reportID) => (
                  <button className="btn" key={reportID} onClick={async () => selectReport(await api.moderationReport(reportID))}>
                    #{reportID}
                  </button>
                ))}
              </div>
              {report && (
                <>
                  <div className="summary-grid">
                    <Summary
                      label={"Source / Reason"}
                      value={`${moderationEnumLabel("source", report.Source)} / ${moderationEnumLabel("reason", report.Reason)}`}
                    />
                    <Summary label={"Reporter"} value={String(report.ReporterUserID)} mono />
                    <Summary label={"Option"} value={report.Option} mono />
                    <Summary label={"Time"} value={formatDate(report.CreatedAt)} />
                  </div>
                  {report.Comment && <p className="about-text">{report.Comment}</p>}
                  <JsonBlock value={JSON.stringify(report, null, 2)} />
                </>
              )}
            </section>
            <section className="section-block">
              <SectionHead title={"Decision and action audit"} text={"Actions run idempotently through a lease worker; failures retain their error and attempt count."} />
              <JsonBlock value={JSON.stringify({ decisions: detail.Decisions, actions: detail.Actions }, null, 2)} />
            </section>
            {detail.Appeals.length > 0 && (
              <section className="section-block">
                <SectionHead title={"Appeals"} />
                <JsonBlock value={JSON.stringify(detail.Appeals, null, 2)} />
              </section>
            )}
          </div>
        }
        side={
          <section className="action-dock">
            <div className="dock-title">{"Case actions"}</div>
            {canClaim && (
              <button className="btn primary icon-text" disabled={busy} onClick={claim}>
                <ShieldCheck size={15} /> {item.AssignedTo ? "Renew claim" : "Claim case"}
              </button>
            )}
            <label className="field">
              <span>{"Review reason"}</span>
              <textarea value={reason} onChange={(event) => setReason(event.target.value)} rows={5} />
            </label>
            <label className="field">
              <span>{"Decision template"}</span>
              <select value={preset} onChange={(event) => setPreset(event.target.value as DecisionPreset)}>
                <option value="no_violation">{"No violation (dismiss report)"}</option>
                <option value="scam">{"Mark as SCAM"}</option>
                <option value="fake">{"Mark as FAKE"}</option>
                <option value="freeze">{"Freeze account"}</option>
                <option value="scam_freeze">{"SCAM + freeze"}</option>
                <option value="fake_freeze">{"FAKE + freeze"}</option>
                <option value="delete_messages">{"Delete messages covered by evidence"}</option>
                <option value="delete_account">{"Delete account"}</option>
              </select>
            </label>
            {preset === "delete_messages" && (
              <>
                <label className="field">
                  <span>{"Evidence message IDs (comma-separated)"}</span>
                  <input value={messageIDs} onChange={(event) => setMessageIDs(event.target.value)} placeholder="101, 102" />
                </label>
                {item.Target.Type === "user" && (
                  <>
                    <label className="field">
                      <span>{"Private-chat owner_user_id"}</span>
                      <input value={ownerUserID} onChange={(event) => setOwnerUserID(event.target.value)} inputMode="numeric" />
                    </label>
                    <label className="field checkbox-field">
                      <input type="checkbox" checked={revokeMessages} onChange={(event) => setRevokeMessages(event.target.checked)} />
                      <span>{"Revoke for both sides"}</span>
                    </label>
                  </>
                )}
                <Alert>{"The server will verify again that every message ID exists in this case's immutable report evidence."}</Alert>
              </>
            )}
            {item.Status === "action_failed" && preset === "no_violation" && (
              <Alert>{"The action was partially executed and cannot be changed directly to no violation. Select a new action to retry while retaining the previous failure audit."}</Alert>
            )}
            {canDecide && (
              <button className="btn danger icon-text" disabled={busy || !canSubmitDecision} onClick={decide}>
                <CheckCircle2 size={15} /> {item.Status === "action_failed"
                  ? "Retry action"
                  : "Submit decision"}
              </button>
            )}
            {pendingAppeal && item.AssignedTo && (
              <>
                <div className="dock-title">{`Appeal review #${pendingAppeal.ID}`}</div>
                <Summary label={"Automatic remedy after approval"} value={appealRemedy.label} />
                {appealRemedy.blocked && (
                  <Alert>{"The case contains a completed irreversible deletion. It cannot be marked as approved and restored; deny it or escalate for manual handling."}</Alert>
                )}
                <button className="btn" disabled={busy} onClick={() => reviewAppeal(pendingAppeal.ID, false)}>
                  {"Deny appeal"}
                </button>
                <button className="btn primary" disabled={busy || appealRemedy.blocked} onClick={() => reviewAppeal(pendingAppeal.ID, true)}>
                  {"Grant appeal"}
                </button>
              </>
            )}
          </section>
        }
      />
    </PageFrame>
  );
}

function actionsForPreset(
  preset: DecisionPreset,
  targetType: string | undefined,
  messageIDs: number[],
  ownerUserID: number,
  revoke: boolean
): Array<{ kind: string; payload: Record<string, unknown> }> {
  switch (preset) {
    case "scam":
      return [{ kind: "mark_scam", payload: {} }];
    case "fake":
      return [{ kind: "mark_fake", payload: {} }];
    case "freeze":
      return [{ kind: "freeze_account", payload: {} }];
    case "scam_freeze":
      return [{ kind: "mark_scam", payload: {} }, { kind: "freeze_account", payload: {} }];
    case "fake_freeze":
      return [{ kind: "mark_fake", payload: {} }, { kind: "freeze_account", payload: {} }];
    case "delete_messages":
      if (messageIDs.length === 0) return [];
      if (targetType === "channel") {
        return [{ kind: "delete_channel_message", payload: { ids: messageIDs } }];
      }
      if (targetType === "user" && Number.isSafeInteger(ownerUserID) && ownerUserID > 0) {
        return [{ kind: "delete_private_message", payload: { owner_user_id: ownerUserID, ids: messageIDs, revoke } }];
      }
      return [];
    case "delete_account":
      return [{ kind: "delete_account", payload: {} }];
    default:
      return [];
  }
}

function parseMessageIDs(raw: string): number[] {
  const values = raw
    .split(/[,\s]+/)
    .filter(Boolean)
    .map(Number);
  if (values.length === 0 || values.some((value) => !Number.isSafeInteger(value) || value <= 0)) return [];
  return [...new Set(values)];
}

function requiredAppealRemedy(detail: ModerationCaseDetail): {
  actions: Array<{ kind: string; payload: Record<string, unknown> }>;
  label: string;
  blocked: boolean;
} {
  let flagsActive = false;
  let freezeActive = false;
  let irreversible = false;
  for (const action of [...detail.Actions].sort((left, right) => left.ID - right.ID)) {
    if (action.Status !== "succeeded") continue;
    switch (action.Kind) {
      case "mark_scam":
      case "mark_fake":
        flagsActive = true;
        break;
      case "clear_peer_flags":
        flagsActive = false;
        break;
      case "freeze_account":
        freezeActive = true;
        break;
      case "unfreeze_account":
        freezeActive = false;
        break;
      case "delete_private_message":
      case "delete_channel_message":
      case "delete_account":
        irreversible = true;
        break;
    }
  }
  const actions: Array<{ kind: string; payload: Record<string, unknown> }> = [];
  const labels: string[] = [];
  if (flagsActive) {
    actions.push({ kind: "clear_peer_flags", payload: {} });
    labels.push("Clear SCAM / FAKE");
  }
  if (freezeActive) {
    actions.push({ kind: "unfreeze_account", payload: {} });
    labels.push("Unfreeze account");
  }
  return { actions, label: labels.join(" + ") || "No recovery action needed", blocked: irreversible };
}

function decisionPresetLabel(preset: DecisionPreset): string {
  const labels: Record<DecisionPreset, string> = {
    no_violation: "No violation (dismiss report)",
    scam: "Mark as SCAM",
    fake: "Mark as FAKE",
    freeze: "Freeze account",
    scam_freeze: "SCAM + freeze",
    fake_freeze: "FAKE + freeze",
    delete_messages: "Delete messages covered by evidence",
    delete_account: "Delete account"
  };
  return labels[preset];
}

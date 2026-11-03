import { ArrowsRotateRight, Person } from "@gravity-ui/icons";
import { useEffect, useState } from "react";
import { api, errorMessage } from "../api";
import { DataTable, FilterChip, type Column } from "../components/DataTable";
import { Alert, Badge, Metric, PageFrame } from "../components/ui";
import { formatDate, formatRelative } from "../lib/format";
import { useDebounced } from "../lib/useDebounced";
import type { Navigate } from "../routing";
import type { ModerationCaseRow } from "../types";

const defaultStatuses = "open,in_review,action_pending,action_failed,appeal_review";
const allStatuses = "open,in_review,action_pending,action_failed,resolved,dismissed,appeal_review";
// One status view at a time; the server filters by it.
const statusViews = [
  { value: defaultStatuses, label: "Active queue" },
  { value: "action_pending,action_failed", label: "Needs action" },
  { value: "appeal_review", label: "Appeals" },
  { value: "resolved,dismissed", label: "Closed" },
  { value: allStatuses, label: "All" }
];

export function ModerationCasesPage({ navigate }: { navigate: Navigate }) {
  const [statuses, setStatuses] = useState(defaultStatuses);
  const [assignedTo, setAssignedTo] = useState("");
  const reviewer = useDebounced(assignedTo.trim());
  const [rows, setRows] = useState<ModerationCaseRow[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function load() {
    setBusy(true);
    setError("");
    try {
      const params = new URLSearchParams({ statuses, limit: "100" });
      if (reviewer) params.set("assigned_to", reviewer);
      setRows((await api.moderationCases(params)).cases);
      setLoaded(true);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [statuses, reviewer]);

  const pendingActions = rows.filter((row) => row.Status === "action_pending" || row.Status === "action_failed").length;
  const critical = rows.filter((row) => row.Severity === 4).length;

  const columns: Column<ModerationCaseRow>[] = [
    { key: "id", label: "Case", className: "mono", sortValue: (row) => row.ID, firstDir: "desc", render: (row) => `#${row.ID}` },
    {
      key: "target",
      label: "Target",
      sortValue: (row) => moderationTargetLabel(row.Target.Type, row.Target.ID),
      render: (row) => moderationTargetLabel(row.Target.Type, row.Target.ID)
    },
    { key: "status", label: "Status", sortValue: (row) => row.Status, render: (row) => <CaseStatus status={row.Status} /> },
    { key: "severity", label: "Severity", firstDir: "desc", sortValue: (row) => row.Severity, render: (row) => <CaseSeverity value={row.Severity} /> },
    {
      key: "reports",
      label: "Reports",
      align: "right",
      firstDir: "desc",
      sortValue: (row) => row.ReportCount,
      render: (row) => (
        <span title={`${row.DistinctReporterCount} distinct reporters`}>
          {row.ReportCount}
          <span className="muted-cell"> / {row.DistinctReporterCount}</span>
        </span>
      )
    },
    { key: "reviewer", label: "Reviewer", sortValue: (row) => row.AssignedTo, render: (row) => row.AssignedTo || <span className="muted-cell">—</span> },
    {
      key: "latest",
      label: "Latest report",
      firstDir: "desc",
      sortValue: (row) => new Date(row.LastReportAt).getTime() || 0,
      render: (row) => <span title={formatDate(row.LastReportAt)}>{formatRelative(row.LastReportAt)}</span>
    }
  ];

  return (
    <PageFrame
      title={"Reports"}
      actions={
        <button className="icon-btn" type="button" aria-label={"Refresh"} title={"Refresh"} onClick={() => void load()} disabled={busy}>
          <ArrowsRotateRight className={busy ? "spin" : undefined} />
        </button>
      }
    >
      {error && <Alert>{error}</Alert>}
      <div className="metric-row">
        <Metric label={"In this view"} value={String(rows.length)} loading={!loaded} />
        <Metric label={"Critical"} value={String(critical)} tone={critical ? "danger" : "neutral"} loading={!loaded} />
        <Metric label={"Needs action"} value={String(pendingActions)} tone={pendingActions ? "warn" : "good"} loading={!loaded} />
      </div>
      <div className="list-controls">
        {statusViews.map((view) => (
          <FilterChip key={view.value} label={view.label} active={statuses === view.value} onToggle={() => setStatuses(view.value)} />
        ))}
        <span className="list-controls-spacer" />
        <label className="searchbox" style={{ maxWidth: 240 }}>
          <Person />
          <input value={assignedTo} onChange={(event) => setAssignedTo(event.target.value)} placeholder={"Reviewer"} aria-label={"Filter by reviewer"} />
        </label>
      </div>
      <DataTable
        columns={columns}
        rows={rows}
        rowKey={(row) => row.ID}
        onRowClick={(row) => navigate(`/moderation/${row.ID}`)}
        defaultSort={{ key: "severity", dir: "desc" }}
        loading={busy && !loaded}
        empty={"No cases in this view."}
      />
    </PageFrame>
  );
}

export function CaseStatus({ status }: { status: string }) {
  const tone = status === "resolved" || status === "dismissed"
    ? "good"
    : status === "action_failed"
      ? "danger"
      : status === "action_pending"
        ? "warn"
        : "neutral";
  return <Badge tone={tone}>{moderationEnumLabel("status", status)}</Badge>;
}

const severityLabels: Record<string, string> = {
  low: "Low",
  medium: "Medium",
  high: "High",
  critical: "Critical"
};

export function CaseSeverity({ value }: { value: number }) {
  const keys = ["", "low", "medium", "high", "critical"];
  const key = keys[value];
  return (
    <Badge tone={value >= 4 ? "danger" : value >= 3 ? "warn" : "neutral"}>
      {key ? severityLabels[key] : value}
    </Badge>
  );
}

const moderationLabels: Record<string, Record<string, string>> = {
  status: {
    open: "Open",
    in_review: "In review",
    action_pending: "Action pending",
    action_failed: "Action failed",
    appeal_review: "Appeal review",
    resolved: "Resolved",
    dismissed: "Dismissed"
  },
  targetType: {
    channel: "Channel",
    chat: "Group",
    user: "Account"
  },
  source: {
    account_peer: "Account / peer",
    antispam_false_positive: "Anti-spam false positive",
    channel_spam: "Channel spam",
    encrypted_spam: "Encrypted-chat spam",
    ephemeral: "Ephemeral media",
    messages: "Messages",
    messages_spam: "Message spam",
    profile_photo: "Profile photo",
    reaction: "Reaction",
    sponsored: "Sponsored message",
    story: "Story"
  },
  reason: {
    child_abuse: "Child abuse",
    copyright: "Copyright",
    fake: "Fake",
    geo_irrelevant: "Location-irrelevant",
    illegal_drugs: "Illegal drugs",
    other: "Other",
    personal_details: "Personal details",
    pornography: "Pornography",
    spam: "Spam",
    violence: "Violence"
  }
};

export function moderationEnumLabel(group: string, value: string): string {
  return moderationLabels[group]?.[value] ?? value;
}

export function moderationTargetLabel(type: string, id: number): string {
  return `${moderationEnumLabel("targetType", type)} #${id}`;
}

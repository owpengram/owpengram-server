import { ArrowsRotateRight, Magnifier } from "@gravity-ui/icons";
import { useEffect, useState } from "react";
import { api, errorMessage } from "../api";
import { DataTable, FilterChip, type Column } from "../components/DataTable";
import { Alert, Badge, PageFrame } from "../components/ui";
import { displayUsername, formatDate, formatRelative } from "../lib/format";
import { useDebounced } from "../lib/useDebounced";
import type { Navigate } from "../routing";
import type {
  VerificationApplicationRow,
  VerificationStatus,
  VerificationTargetType
} from "../types";

type StatusFilter = "all" | VerificationStatus;
type TargetFilter = "all" | VerificationTargetType;

const statuses: VerificationStatus[] = ["draft", "submitted", "in_review", "approved", "rejected", "cancelled"];
const targetTypes: VerificationTargetType[] = ["bot", "channel", "supergroup", "user"];

export const verificationStatusLabels: Record<VerificationStatus, string> = {
  draft: "Draft",
  submitted: "Submitted",
  in_review: "In review",
  approved: "Approved",
  rejected: "Rejected",
  cancelled: "Cancelled"
};

export const verificationTargetTypeLabels: Record<VerificationTargetType, string> = {
  bot: "Bot",
  channel: "Channel",
  supergroup: "Supergroup",
  user: "User"
};

export function VerificationPage({ navigate }: { navigate: Navigate }) {
  const [status, setStatus] = useState<StatusFilter>("all");
  const [targetType, setTargetType] = useState<TargetFilter>("all");
  const [q, setQ] = useState("");
  const query = useDebounced(q.trim().replace(/^@/, ""));
  const [rows, setRows] = useState<VerificationApplicationRow[]>([]);
  const [counts, setCounts] = useState<Record<string, string>>({});
  const [hasMore, setHasMore] = useState(false);
  const [cursor, setCursor] = useState("");
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function load(next = false) {
    setBusy(true);
    setError("");
    const params = new URLSearchParams({ limit: "50" });
    if (status !== "all") params.set("status", status);
    if (targetType !== "all") params.set("target_type", targetType);
    if (query) params.set("q", query);
    if (next && cursor) params.set("before_id", cursor);
    try {
      const result = await api.verificationApplications(params);
      const page = result.rows ?? [];
      setRows((current) => (next ? [...current, ...page] : page));
      setCursor(result.next_before_id ?? "");
      setHasMore(Boolean(result.has_more));
      setLoaded(true);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function loadCounts() {
    try {
      const result = await api.verificationCounts();
      setCounts(result.counts ?? {});
    } catch (err) {
      setError(errorMessage(err));
    }
  }

  useEffect(() => {
    void load(false);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [status, targetType, query]);

  useEffect(() => {
    void loadCounts();
  }, []);

  function refresh() {
    void load(false);
    void loadCounts();
  }

  const columns: Column<VerificationApplicationRow>[] = [
    { key: "id", label: "ID", className: "mono", firstDir: "desc", sortValue: (row) => Number(row.ID), render: (row) => `#${row.ID}` },
    {
      key: "target",
      label: "Target",
      sortValue: (row) => targetLabel(row),
      render: (row) => (
        <span>
          <span>{targetLabel(row)}</span>
          <span className="cell-sub"> {verificationTargetTypeLabels[row.TargetType]}</span>
          {row.TargetVerified && <> <Badge tone="good">{"Already verified"}</Badge></>}
        </span>
      )
    },
    {
      key: "applicant",
      label: "Applicant",
      sortValue: (row) => displayUsername(row.ApplicantUsername) || row.ApplicantName,
      render: (row) => displayUsername(row.ApplicantUsername) || row.ApplicantName || <span className="muted-cell">—</span>
    },
    { key: "category", label: "Category", sortValue: (row) => row.Category, render: (row) => row.Category || <span className="muted-cell">—</span> },
    { key: "status", label: "Status", sortValue: (row) => row.Status, render: (row) => <VerificationStatusBadge status={row.Status} /> },
    {
      key: "submitted",
      label: "Submitted",
      firstDir: "desc",
      sortValue: (row) => new Date(row.SubmittedAt).getTime() || 0,
      render: (row) => <span title={formatDate(row.SubmittedAt)}>{formatRelative(row.SubmittedAt) || "—"}</span>
    },
    { key: "reviewer", label: "Reviewer", sortValue: (row) => row.ReviewerAdminID, render: (row) => row.ReviewerAdminID || <span className="muted-cell">—</span> }
  ];

  return (
    <PageFrame
      title={"Verification"}
      actions={
        <button className="icon-btn" type="button" aria-label={"Refresh"} title={"Refresh"} onClick={refresh} disabled={busy}>
          <ArrowsRotateRight className={busy ? "spin" : undefined} />
        </button>
      }
    >
      {error && <Alert>{error}</Alert>}
      <div className="list-controls">
        <label className="searchbox">
          <Magnifier />
          <input value={q} onChange={(event) => setQ(event.target.value)} placeholder={"Search by ID, username or title"} aria-label={"Search applications"} />
        </label>
        <FilterChip label={"All"} active={status === "all"} onToggle={() => setStatus("all")} />
        {statuses.map((item) => (
          <FilterChip
            key={item}
            label={verificationStatusLabels[item]}
            count={Number(counts[item] ?? 0)}
            active={status === item}
            onToggle={() => setStatus(status === item ? "all" : item)}
          />
        ))}
        <select value={targetType} onChange={(event) => setTargetType(event.target.value as TargetFilter)} aria-label={"Target type"} style={{ width: "auto", height: 30 }}>
          <option value="all">{"Any target"}</option>
          {targetTypes.map((item) => (
            <option key={item} value={item}>{verificationTargetTypeLabels[item]}</option>
          ))}
        </select>
      </div>
      <DataTable
        columns={columns}
        rows={rows}
        rowKey={(row) => row.ID}
        onRowClick={(row) => navigate(`/verification/${row.ID}`)}
        loading={busy && !loaded}
        empty={"No applications in this view."}
      />
      <div className="table-footer">
        <span>{rows.length > 0 ? `${rows.length} shown` : ""}</span>
        {hasMore && (
          <button className="btn" type="button" onClick={() => void load(true)} disabled={busy}>
            {"Load more"}
          </button>
        )}
      </div>
    </PageFrame>
  );
}

export function VerificationStatusBadge({ status }: { status: VerificationStatus }) {
  return <Badge tone={statusTone(status)}>{verificationStatusLabels[status]}</Badge>;
}

export function statusTone(status: VerificationStatus): "neutral" | "good" | "warn" | "danger" {
  if (status === "approved") return "good";
  if (status === "submitted" || status === "in_review") return "warn";
  if (status === "rejected") return "danger";
  return "neutral";
}

// submitted and in_review are the two statuses that need a reviewer; they are
// highlighted only while something actually sits in them.
export function targetLabel(row: VerificationApplicationRow): string {
  return displayUsername(row.TargetUsername) || row.TargetTitle || `#${row.TargetID}`;
}

// The panel page that owns the target peer type, so a reviewer can inspect the
// live record rather than only the submission snapshot.
export function targetHref(row: VerificationApplicationRow): string {
  if (row.TargetType === "bot") return `/bots/${row.TargetID}`;
  if (row.TargetType === "user") return `/accounts/${row.TargetID}`;
  return `/channels/${row.TargetID}`;
}

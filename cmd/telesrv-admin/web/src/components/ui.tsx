import { CircleAlert } from "lucide-react";
import type { ReactNode } from "react";
import { displayUsername, formatDate } from "../lib/format";
import type { AccountUsername, AuditLogRow } from "../types";

type Tone = "neutral" | "good" | "danger" | "warn";

export function PageFrame({
  title,
  eyebrow,
  children,
  actions
}: {
  title: string;
  eyebrow?: string;
  children: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <div className="page-frame">
      <div className="page-title-row">
        <div>
          {eyebrow && <div className="eyebrow">{eyebrow}</div>}
          <h2>{title}</h2>
        </div>
        {actions && <div className="page-actions">{actions}</div>}
      </div>
      {children}
    </div>
  );
}

export function QueryPanel({ children }: { children: ReactNode }) {
  return <div className="query-panel">{children}</div>;
}

export function SplitLayout({ main, side }: { main: ReactNode; side: ReactNode }) {
  return (
    <div className="split-layout">
      <div className="split-main">{main}</div>
      <aside className="split-side">{side}</aside>
    </div>
  );
}

export function SectionHead({ title, text, action }: { title: string; text?: string; action?: ReactNode }) {
  return (
    <div className="section-head">
      <div>
        <h2>{title}</h2>
        {text && <p>{text}</p>}
      </div>
      {action && <div className="section-action">{action}</div>}
    </div>
  );
}

export function Alert({ children }: { children: ReactNode }) {
  return <div className="alert"><CircleAlert size={16} /> <span>{children}</span></div>;
}

export function Badge({ children, tone = "neutral" }: { children: ReactNode; tone?: Tone }) {
  return <span className={`badge ${tone}`}>{children}</span>;
}

export function StatusItem({ label, value, tone }: { label: string; value: string; tone: "neutral" | "good" | "warn" }) {
  return (
    <div className={`status-item ${tone}`}>
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

export function Metric({
  label,
  value,
  tone = "neutral",
  mono = false,
  loading = false
}: {
  label: string;
  value: string;
  tone?: Tone;
  mono?: boolean;
  loading?: boolean;
}) {
  return (
    <div className={`metric ${tone}`}>
      <span>{label}</span>
      <strong className={mono ? "mono" : ""} aria-busy={loading || undefined}>
        {loading ? <span className="skeleton skeleton-text" aria-label="Loading" /> : value}
      </strong>
    </div>
  );
}

export function Summary({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="summary-item">
      <span>{label}</span>
      <strong className={mono ? "mono" : ""}>{value}</strong>
    </div>
  );
}

export function AuditTable({ rows }: { rows: AuditLogRow[] }) {
  return (
    <div className="table-wrap">
      <table className="data-table">
        <thead><tr><th>{"ID"}</th><th>{"Command ID"}</th><th>{"Action"}</th><th>{"Actor"}</th><th>{"Status"}</th><th>{"Dry-run"}</th><th>{"Reason"}</th><th>{"Time"}</th></tr></thead>
        <tbody>
          {rows.map((row) => (
            <tr key={row.ID}>
              <td>{row.ID}</td>
              <td className="mono">{row.CommandID}</td>
              <td>{row.Action}</td>
              <td>{row.Actor}</td>
              <td>{row.Status}</td>
              <td>{row.DryRun ? "Yes" : "No"}</td>
              <td className="truncate">{row.Reason}</td>
              <td>{formatDate(row.CreatedAt)}</td>
            </tr>
          ))}
          {rows.length === 0 && <EmptyRow colSpan={8} />}
        </tbody>
      </table>
    </div>
  );
}

export function EmptyRow({ colSpan }: { colSpan: number }) {
  return <tr><td colSpan={colSpan} className="empty-cell">{"No results"}</td></tr>;
}

// Placeholder rows for a table that has nothing yet *because it is still
// loading* -- distinct from EmptyRow, which asserts the query genuinely
// returned nothing. Showing "No results" during the first fetch reads as a
// wrong answer rather than a pending one.
export function LoadingRow({ colSpan, rows = 3 }: { colSpan: number; rows?: number }) {
  return (
    <>
      {Array.from({ length: rows }, (_, row) => (
        <tr key={row} aria-busy="true">
          {Array.from({ length: colSpan }, (_unused, cell) => (
            <td key={cell}>
              <span className="skeleton skeleton-text" aria-label={cell === 0 ? "Loading" : undefined} />
            </td>
          ))}
        </tr>
      ))}
    </>
  );
}

export function LoadingSurface({ label }: { label: string }) {
  return <section className="surface"><div className="loading-line">{label}</div></section>;
}

export function JsonBlock({ value }: { value: string }) {
  return <pre className="json-block">{value || "{}"}</pre>;
}

// UsernameCell renders a peer's editable username with its collectible usernames
// branching off underneath, in the order clients project them.
//
// An inactive collectible is shown rather than hidden: the peer still owns it, it
// just does not resolve publicly, and an operator looking for "where did that name
// go" needs to see it. It is marked instead of dropped.
// Pass an empty username to render the branch on its own, which is what the
// detail header does: it already shows the editable slot on the line above.
export function UsernameCell({ username, collectibles }: { username?: string; collectibles?: AccountUsername[] | null }) {
  const main = displayUsername(username ?? "");
  const branch = collectibles ?? [];
  if (branch.length === 0) {
    return <>{main || "-"}</>;
  }
  return (
    <>
      {main}
      <ul className="username-branch">
        {branch.map((item) => (
          <li key={item.Username} className={item.Active ? "" : "inactive"}>
            <span>{displayUsername(item.Username)}</span>
            {!item.Active && <em>{"inactive"}</em>}
          </li>
        ))}
      </ul>
    </>
  );
}

import { ArrowsRotateRight, Smartphone } from "@gravity-ui/icons";
import { useEffect, useState } from "react";
import { api, errorMessage } from "../api";
import { Avatar } from "../components/Avatar";
import { DataTable, type Column } from "../components/DataTable";
import { Alert, Badge, Metric, PageFrame, SectionHead } from "../components/ui";
import { displayName, displayPhone, displayUsername, formatDate, formatRelative } from "../lib/format";
import type { Navigate } from "../routing";
import type { SharedDeviceGroup } from "../types";

type SharedAccount = SharedDeviceGroup["Accounts"][number];

// SharedDevicesPage surfaces authorizations that look like they came from the
// same physical device but belong to different accounts -- a lead worth
// investigating for multi-accounting, not a verdict: device_model and
// system_version are self-reported by the client and easy to spoof, and a
// matching IP alone is common and innocent behind NAT, shared wifi, or
// carrier CGNAT. Treat every group here as "worth a look", not "guilty".
export function SharedDevicesPage({ navigate }: { navigate: Navigate }) {
  const [groups, setGroups] = useState<SharedDeviceGroup[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [offset, setOffset] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function load(next = false) {
    setBusy(true);
    setError("");
    const at = next ? offset : 0;
    const params = new URLSearchParams({ limit: "20", offset: String(at) });
    try {
      const result = await api.sharedDeviceGroups(params);
      const page = result.rows ?? [];
      setGroups((current) => (next ? [...current, ...page] : page));
      setOffset(result.next_offset);
      setHasMore(Boolean(result.has_more));
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    void load(false);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const totalFlaggedAccounts = groups.reduce((sum, group) => sum + group.AccountCount, 0);

  const columns: Column<SharedAccount>[] = [
    {
      key: "name",
      label: "Account",
      sortValue: (a) => displayName(a),
      render: (a) => (
        <span className="cell-identity">
          <Avatar id={a.UserID} firstName={a.FirstName} lastName={a.LastName} username={a.Username} size={28} />
          <span>
            <span>{displayName(a)}</span>
            {a.Username && <span className="cell-sub"> {displayUsername(a.Username)}</span>}
          </span>
        </span>
      )
    },
    { key: "id", label: "ID", className: "mono muted-cell", sortValue: (a) => a.UserID, render: (a) => a.UserID },
    { key: "phone", label: "Phone", sortValue: (a) => a.Phone, render: (a) => displayPhone(a.Phone) || <span className="muted-cell">—</span> },
    {
      key: "active",
      label: "Active from this device",
      firstDir: "desc",
      sortValue: (a) => new Date(a.ActiveAt).getTime() || 0,
      render: (a) => <span title={formatDate(a.ActiveAt)}>{formatRelative(a.ActiveAt)}</span>
    }
  ];

  return (
    <PageFrame
      title={"Shared devices"}
      actions={
        <button className="icon-btn" type="button" aria-label={"Refresh"} title={"Refresh"} onClick={() => void load(false)} disabled={busy}>
          <ArrowsRotateRight className={busy ? "spin" : undefined} />
        </button>
      }
    >
      {error && <Alert>{error}</Alert>}
      <div className="metric-row">
        <Metric label={"Device groups on page"} value={String(groups.length)} />
        <Metric label={"Accounts flagged on page"} value={String(totalFlaggedAccounts)} tone="warn" />
      </div>
      <p className="about-text">
        {"Each card below is a device fingerprint (device model + OS + platform + IP) that more than one account has authorized from. "}
        {"device_model/system_version are self-reported by the client, and IP alone can collide innocently -- use this as a lead, not a verdict."}
      </p>

      <div className="stacked-sections">
        {groups.map((group) => (
          <section className="section-block" key={`${group.DeviceModel}|${group.SystemVersion}|${group.Platform}|${group.IP}`}>
            <SectionHead
              title={group.DeviceModel || "Unknown device"}
              text={`${group.Platform || "unknown platform"} ${group.SystemVersion} · ${group.IP} · last active ${formatRelative(group.LastActiveAt)}`}
              action={<Badge tone="warn"><Smartphone width={12} height={12} /> {`${group.AccountCount} accounts`}</Badge>}
            />
            <DataTable
              columns={columns}
              rows={group.Accounts}
              rowKey={(a) => a.UserID}
              onRowClick={(a) => navigate(`/accounts/${a.UserID}`)}
              defaultSort={{ key: "active", dir: "desc" }}
            />
          </section>
        ))}
        {groups.length === 0 && !busy && <p className="about-text">{"No device is shared by more than one account."}</p>}
      </div>

      {hasMore && (
        <div className="table-footer">
          <span />
          <button className="btn" type="button" onClick={() => void load(true)} disabled={busy}>
            {"Load more"}
          </button>
        </div>
      )}
    </PageFrame>
  );
}

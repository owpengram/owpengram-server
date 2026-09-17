import { Loader2, RefreshCw, Search } from "lucide-react";
import { useEffect, useState } from "react";
import { api, errorMessage } from "../api";
import { AuditTable } from "../components/ui";
import { Alert, PageFrame, QueryPanel } from "../components/ui";
import type { AuditLogListResponse } from "../types";

// AuditLogPage is the global counterpart of the AuditLogs table embedded on
// each account/channel/bot detail page: it walks the whole admin_audit_logs
// table instead of one target's slice of it, for permissionAuditRead holders
// who need to review the console's whole action trail in one place.
export function AuditLogPage() {
  const [data, setData] = useState<AuditLogListResponse | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [actor, setActor] = useState("");
  const [action, setAction] = useState("");
  const [status, setStatus] = useState("");

  async function load() {
    setBusy(true);
    setError("");
    const params = new URLSearchParams({ limit: "200" });
    if (actor.trim()) params.set("actor", actor.trim());
    if (action.trim()) params.set("action", action.trim());
    if (status) params.set("status", status);
    try {
      setData(await api.auditLogs(params));
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <PageFrame title={"Audit log"} eyebrow={"Every admin console action, across every account, channel and bot"}>
      {error && <Alert>{error}</Alert>}
      <QueryPanel>
        <form className="toolbar" onSubmit={(event) => { event.preventDefault(); void load(); }}>
          <label className="searchbox">
            <Search size={15} />
            <input value={actor} onChange={(event) => setActor(event.target.value)} placeholder={"Actor"} />
          </label>
          <label className="searchbox">
            <Search size={15} />
            <input value={action} onChange={(event) => setAction(event.target.value)} placeholder={"Action"} />
          </label>
          <label className="gift-page-size">
            <span>{"Status"}</span>
            <select value={status} onChange={(event) => setStatus(event.target.value)}>
              <option value="">{"Any"}</option>
              <option value="completed">{"Completed"}</option>
              <option value="failed">{"Failed"}</option>
              <option value="running">{"Running"}</option>
            </select>
          </label>
          <button className="btn primary icon-text" type="submit" disabled={busy}>
            {busy ? <Loader2 size={15} className="spin" /> : <Search size={15} />} {"Search"}
          </button>
          <button className="btn icon-text" type="button" onClick={() => void load()} disabled={busy}>
            <RefreshCw size={15} className={busy ? "spin" : ""} /> {"Refresh"}
          </button>
        </form>
      </QueryPanel>
      <AuditTable rows={data?.rows ?? []} showTarget />
    </PageFrame>
  );
}

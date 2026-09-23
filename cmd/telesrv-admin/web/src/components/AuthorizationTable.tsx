import { Cable, LogOut, ShieldCheck } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { formatDate } from "../lib/format";
import type { AuthorizationRow } from "../types";
import { ActionButton } from "./ActionButton";
import { EmptyRow } from "./ui";

export function AuthorizationTable({ rows, userID, onDone }: { rows: AuthorizationRow[]; userID: number; onDone: () => void }) {
  const [removedHashes, setRemovedHashes] = useState<Set<string>>(() => new Set());

  useEffect(() => {
    setRemovedHashes(new Set());
  }, [userID]);

  const visibleRows = useMemo(
    () => rows.filter((row) => !removedHashes.has(row.Hash)),
    [rows, removedHashes]
  );

  function afterRevoke(mutator: (previous: Set<string>) => Set<string>) {
    setRemovedHashes((previous) => mutator(previous));
    onDone();
  }

  return (
    <div className="authorization-block">
      <div className="table-wrap">
        <table className="data-table authorization-table">
          <thead>
            <tr>
              <th>{"Device"}</th>
              <th>{"Platform"}</th>
              <th>{"IP"}</th>
              <th>{"Last active"}</th>
              <th className="device-actions-head">{"Actions"}</th>
            </tr>
          </thead>
          <tbody>
            {visibleRows.map((row) => (
              <tr key={row.Hash}>
                <td className="device-text">{row.DeviceModel} {row.SystemVersion}</td>
                <td className="device-text">{row.Platform} {row.AppVersion}</td>
                <td>{row.IP}</td>
                <td>{formatDate(row.ActiveAt)}</td>
                <td className="device-actions-cell">
                  <div className="device-actions">
                    <ActionButton
                      label={"Revoke current"}
                      icon={<LogOut size={13} />}
                      compact
                      path="/api/actions/revoke-sessions"
                      payload={() => ({ user_id: userID, hash: row.Hash })}
                      onDone={() => afterRevoke((previous) => new Set([...previous, row.Hash]))}
                    />
                    <ActionButton
                      label={"Keep current"}
                      icon={<ShieldCheck size={13} />}
                      compact
                      path="/api/actions/revoke-sessions"
                      payload={() => ({ user_id: userID, keep_hash: row.Hash })}
                      onDone={() => afterRevoke(() => new Set(rows.filter((item) => item.Hash !== row.Hash).map((item) => item.Hash)))}
                    />
                  </div>
                </td>
              </tr>
            ))}
            {visibleRows.length === 0 && <EmptyRow colSpan={5} />}
          </tbody>
        </table>
      </div>
      <div className="danger-zone">
        <ActionButton
          label={"Revoke all devices"}
          icon={<Cable size={15} />}
          path="/api/actions/revoke-sessions"
          payload={() => ({ user_id: userID, revoke_all: true })}
          onDone={() => afterRevoke(() => new Set(rows.map((item) => item.Hash)))}
        />
      </div>
    </div>
  );
}

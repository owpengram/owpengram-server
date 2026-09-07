import { KeyRound, Lock, RefreshCw, ShieldCheck, UserPlus, X } from "lucide-react";
import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { api, errorMessage } from "../api";
import { ActionButton } from "../components/ActionButton";
import { Alert, EmptyRow, LoadingRow, PageFrame, QueryPanel, SectionHead } from "../components/ui";
import { groupPermissions, permissionHint, permissionTitle } from "../permissions";
import type { AdminConsoleUser, AdminConsoleSystemOperator } from "../types";

// The operator-accounts screen. The table only reports; every change happens in
// a modal and goes through the panel's usual reason + dry-run + confirm flow,
// because handing somebody the run of the console deserves the same "here is
// what this will do" step as freezing an account.
//
// Everything here is additionally enforced server-side by admins.manage --
// hiding the section is a convenience, not the boundary.
export function AdminUsersPage() {
  const [rows, setRows] = useState<AdminConsoleUser[]>([]);
  const [system, setSystem] = useState<AdminConsoleSystemOperator | null>(null);
  const [available, setAvailable] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState<AdminConsoleUser | null>(null);
  const [resetting, setResetting] = useState<AdminConsoleUser | null>(null);
  const [creating, setCreating] = useState(false);

  async function load() {
    setBusy(true);
    setError("");
    try {
      const result = await api.adminUsers();
      setRows(result.rows ?? []);
      setSystem(result.system ?? null);
      setAvailable(result.available_permissions ?? []);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
      setLoaded(true);
    }
  }

  useEffect(() => {
    void load();
  }, []);

  return (
    <PageFrame eyebrow={"ACCESS / OPERATORS"} title={"Admin operators"}>
      {error && <Alert>{error}</Alert>}

      <QueryPanel>
        <div className="toolbar">
          <button className="btn primary icon-text" type="button" onClick={() => setCreating(true)}>
            <UserPlus size={15} /> {"New operator"}
          </button>
          <button className="btn icon-text" type="button" onClick={() => void load()} disabled={busy}>
            <RefreshCw size={15} className={busy ? "spin" : ""} /> {"Refresh"}
          </button>
        </div>
      </QueryPanel>

      <SectionHead title={"Operators"} />
      <div className="table-wrap">
        <table className="data-table">
          <thead>
            <tr>
              <th>{"Username"}</th>
              <th>{"Can do"}</th>
              <th>{"Status"}</th>
              <th>{"Last login"}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {/* The built-in operator first: it has the most rights and no
                database row, so a list that started with the named accounts
                would put the most powerful login last, or nowhere. */}
            {system && (
              <tr>
                <td className="mono">
                  {system.username} <span className="pill">{"built-in"}</span>
                </td>
                <td><PermissionChips permissions={system.permissions} /></td>
                <td><span className="pill good">{"Enabled"}</span></td>
                <td className="mono">{"—"}</td>
                <td>
                  <span className="muted icon-text">
                    <Lock size={13} /> {"Set in the server environment"}
                  </span>
                </td>
              </tr>
            )}
            {rows.map((row) => (
              <tr key={row.id}>
                <td className="mono">{row.username}</td>
                <td><PermissionChips permissions={row.permissions} /></td>
                <td>
                  {row.enabled
                    ? <span className="pill good">{"Enabled"}</span>
                    : <span className="pill">{"Disabled"}</span>}
                </td>
                <td className="mono">{row.last_login_at ? new Date(row.last_login_at).toLocaleString() : "—"}</td>
                <td>
                  <button className="btn icon-text" type="button" onClick={() => setEditing(row)}>
                    <ShieldCheck size={14} /> {"Access"}
                  </button>
                  <button className="btn icon-text" type="button" onClick={() => setResetting(row)}>
                    <KeyRound size={14} /> {"Password"}
                  </button>
                </td>
              </tr>
            ))}
            {rows.length === 0 && !system &&
              (busy || !loaded ? <LoadingRow colSpan={5} /> : <EmptyRow colSpan={5} />)}
          </tbody>
        </table>
      </div>

      {creating && (
        <OperatorModal
          title={"New operator"}
          available={available}
          onClose={() => setCreating(false)}
          onDone={() => { setCreating(false); void load(); }}
        />
      )}
      {editing && (
        <OperatorModal
          title={`Access for ${editing.username}`}
          available={available}
          existing={editing}
          onClose={() => setEditing(null)}
          onDone={() => { setEditing(null); void load(); }}
        />
      )}
      {resetting && (
        <PasswordModal
          operator={resetting}
          onClose={() => setResetting(null)}
          onDone={() => { setResetting(null); void load(); }}
        />
      )}
    </PageFrame>
  );
}

function PermissionChips({ permissions }: { permissions: string[] }) {
  if (permissions.length === 0) {
    return <span className="muted">{"nothing yet"}</span>;
  }
  return (
    <span className="chip-row">
      {permissions.map((p) => (
        <span className="chip" key={p} title={p}>{permissionTitle(p)}</span>
      ))}
    </span>
  );
}

// PermissionPicker lists the rights by what they let someone do, split into the
// section of the console each governs -- twenty-six checkboxes in one run is a
// wall nobody reads, and the grouping is what makes "what can this person
// actually touch" answerable at a glance.
//
// The raw permission string stays as each row's tooltip, so the screen never
// hides what is actually being stored.
function PermissionPicker({
  available,
  selected,
  onToggle,
  onToggleGroup
}: {
  available: string[];
  selected: string[];
  onToggle: (permission: string, on: boolean) => void;
  onToggleGroup: (permissions: string[], on: boolean) => void;
}) {
  return (
    <div className="permission-groups">
      {groupPermissions(available).map((group) => {
        const all = group.permissions.every((p) => selected.includes(p));
        return (
          <section className="permission-group" key={group.title}>
            <div className="permission-group-head">
              <div>
                <strong>{group.title}</strong>
                <small>{group.hint}</small>
              </div>
              <button
                className="btn compact"
                type="button"
                onClick={() => onToggleGroup(group.permissions, !all)}
              >
                {all ? "Clear" : "Select all"}
              </button>
            </div>
            <div className="permission-grid">
              {group.permissions.map((permission) => (
                <label className="permission-item" key={permission} title={permission}>
                  <input
                    type="checkbox"
                    checked={selected.includes(permission)}
                    onChange={(event) => onToggle(permission, event.target.checked)}
                  />
                  <span className="permission-copy">
                    <strong>{permissionTitle(permission)}</strong>
                    <small>{permissionHint(permission)}</small>
                  </span>
                </label>
              ))}
            </div>
          </section>
        );
      })}
    </div>
  );
}

// OperatorModal creates a new operator, or edits an existing one's access. The
// same shape either way: the only difference is whether a username and password
// are being chosen.
//
// Laid out as head / scrolling body / action bar like every other command modal
// in the panel, so a long permission list scrolls inside the dialog instead of
// pushing its own confirm button off the screen.
function OperatorModal({
  title,
  available,
  existing,
  onClose,
  onDone
}: {
  title: string;
  available: string[];
  existing?: AdminConsoleUser;
  onClose: () => void;
  onDone: () => void;
}) {
  const [username, setUsername] = useState(existing?.username ?? "");
  const [password, setPassword] = useState("");
  const [permissions, setPermissions] = useState<string[]>(existing?.permissions ?? []);
  const [enabled, setEnabled] = useState(existing?.enabled ?? true);
  const isEdit = Boolean(existing);

  // Only the shape the server insists on: a username it will accept, and a
  // password that is actually present. Length is the operator's business.
  const incomplete = isEdit
    ? false
    : username.trim().length < 3 || password.trim() === "";

  return createPortal(
    <div className="modal-backdrop" role="presentation">
      <section className="modal command-modal" role="dialog" aria-modal="true" aria-label={title}>
        <div className="modal-head">
          <div>
            <div className="eyebrow">{"Operators"}</div>
            <h2>{title}</h2>
          </div>
          <button className="icon-btn" type="button" onClick={onClose} aria-label={"Close"}><X size={15} /></button>
        </div>

        <div className="command-body">
          {!isEdit && (
            <div className="operator-identity">
              <label className="duration-field">
                <span>{"Username"}</span>
                <input
                  autoFocus
                  value={username}
                  spellCheck={false}
                  autoCapitalize="none"
                  placeholder={"letters, digits, dot, dash or underscore"}
                  onChange={(event) => setUsername(event.target.value)}
                />
              </label>
              <label className="duration-field">
                <span>{"Password"}</span>
                <input
                  type="password"
                  value={password}
                  autoComplete="new-password"
                  onChange={(event) => setPassword(event.target.value)}
                />
              </label>
            </div>
          )}

          <PermissionPicker
            available={available}
            selected={permissions}
            onToggle={(permission, on) =>
              setPermissions((current) =>
                on ? [...current, permission] : current.filter((p) => p !== permission)
              )
            }
            onToggleGroup={(group, on) =>
              setPermissions((current) =>
                on
                  ? [...current, ...group.filter((p) => !current.includes(p))]
                  : current.filter((p) => !group.includes(p))
              )
            }
          />

          <label className="permission-item standalone">
            <input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} />
            <span className="permission-copy">
              <strong>{"Account is enabled"}</strong>
              <small>{"A disabled operator cannot sign in"}</small>
            </span>
          </label>

          {isEdit && (
            <Alert>{"The new access applies from this operator's next request. They stay signed in."}</Alert>
          )}
        </div>

        <div className="modal-actions toolbar">
          <button className="btn" type="button" onClick={onClose}>{"Cancel"}</button>
          <ActionButton
            label={isEdit ? "Save access" : "Create operator"}
            path={isEdit ? "/api/actions/set-admin-operator-access" : "/api/actions/create-admin-operator"}
            tone="primary"
            disabled={incomplete}
            icon={isEdit ? <ShieldCheck size={15} /> : <UserPlus size={15} />}
            payload={() =>
              isEdit
                ? { id: existing?.id, permissions, enabled }
                : { username: username.trim(), password, permissions, enabled }
            }
            onDone={onDone}
          />
        </div>
      </section>
    </div>,
    document.body
  );
}

function PasswordModal({
  operator,
  onClose,
  onDone
}: {
  operator: AdminConsoleUser;
  onClose: () => void;
  onDone: () => void;
}) {
  const [password, setPassword] = useState("");

  return createPortal(
    <div className="modal-backdrop" role="presentation">
      <section className="modal command-modal narrow" role="dialog" aria-modal="true" aria-label={"Set password"}>
        <div className="modal-head">
          <div>
            <div className="eyebrow">{"Operators"}</div>
            <h2>{`Password for ${operator.username}`}</h2>
          </div>
          <button className="icon-btn" type="button" onClick={onClose} aria-label={"Close"}><X size={15} /></button>
        </div>

        <div className="command-body">
          <label className="duration-field">
            <span>{"New password"}</span>
            <input
              autoFocus
              type="password"
              value={password}
              autoComplete="new-password"
              onChange={(event) => setPassword(event.target.value)}
            />
          </label>
          <Alert>{"Changing the password signs this operator out of any session they already have."}</Alert>
        </div>

        <div className="modal-actions toolbar">
          <button className="btn" type="button" onClick={onClose}>{"Cancel"}</button>
          <ActionButton
            label={"Set password"}
            path={"/api/actions/set-admin-operator-password"}
            tone="primary"
            disabled={password.trim() === ""}
            icon={<KeyRound size={15} />}
            payload={() => ({ id: operator.id, password })}
            onDone={onDone}
          />
        </div>
      </section>
    </div>,
    document.body
  );
}

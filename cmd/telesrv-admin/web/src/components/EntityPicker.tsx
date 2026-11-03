import { Check, Loader2, Search, X } from "./icons";
import { useEffect, useState } from "react";
import { api, errorMessage } from "../api";
import { channelKind, displayName, displayPhone, displayUsername } from "../lib/format";
import type { AccountRow, BotRow, ChannelRow } from "../types";
import { Badge } from "./ui";

export function UserPicker({
  label,
  value,
  onChange
}: {
  label: string;
  value: AccountRow | null;
  onChange: (row: AccountRow | null) => void;
}) {
  const [query, setQuery] = useState("");
  const [rows, setRows] = useState<AccountRow[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function search() {
    setBusy(true);
    setError("");
    const params = new URLSearchParams({ limit: "20" });
    if (query.trim()) {
      params.set("q", query.trim());
    }
    try {
      const result = await api.accounts(params);
      setRows(result.rows);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    void search();
  }, []);

  return (
    <div className="entity-picker">
      <div className="picker-head">
        <span>{label}</span>
        {value ? (
          <button className="link-button" type="button" onClick={() => onChange(null)}>
            <X size={13} /> {"Clear"}
          </button>
        ) : null}
      </div>
      {value ? (
        <div className="selected-entity">
          <Check size={15} />
          <div>
            <strong>{displayName(value)}</strong>
            <span className="mono">{value.ID}</span>
          </div>
          <span>{displayUsername(value.Username) || displayPhone(value.Phone) || "-"}</span>
        </div>
      ) : null}
      <div className="picker-search">
        <Search size={15} />
        <input
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.preventDefault();
              void search();
            }
          }}
          placeholder={"Search user_id / phone / username"}
        />
        <button className="btn compact-btn" type="button" onClick={search} disabled={busy}>
          {busy ? <Loader2 size={14} className="spin" /> : "Search"}
        </button>
      </div>
      {error && <div className="picker-error">{error}</div>}
      <div className="picker-results">
        {rows.map((row) => (
          <button
            key={row.ID}
            className={`picker-row ${value?.ID === row.ID ? "selected" : ""}`}
            type="button"
            onClick={() => onChange(row)}
          >
            <span className="mono">{row.ID}</span>
            <strong>{displayName(row)}</strong>
            <span>{displayUsername(row.Username) || displayPhone(row.Phone) || "-"}</span>
            {row.Verified ? <Badge tone="good">{"Verified"}</Badge> : <Badge>{"Regular"}</Badge>}
          </button>
        ))}
        {rows.length === 0 && !busy ? <div className="picker-empty">{"No results"}</div> : null}
      </div>
    </div>
  );
}

// MultiUserPicker is UserPicker's search widget with a running selection
// instead of a single slot -- clicking a result toggles it in or out of the
// list, shown above the search box as removable chips.
export function MultiUserPicker({
  label,
  selected,
  onChange
}: {
  label: string;
  selected: AccountRow[];
  onChange: (rows: AccountRow[]) => void;
}) {
  const [query, setQuery] = useState("");
  const [rows, setRows] = useState<AccountRow[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function search() {
    setBusy(true);
    setError("");
    const params = new URLSearchParams({ limit: "20" });
    if (query.trim()) {
      params.set("q", query.trim());
    }
    try {
      const result = await api.accounts(params);
      setRows(result.rows);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    void search();
  }, []);

  function toggle(row: AccountRow) {
    if (selected.some((entry) => entry.ID === row.ID)) {
      onChange(selected.filter((entry) => entry.ID !== row.ID));
    } else {
      onChange([...selected, row]);
    }
  }

  function remove(id: number) {
    onChange(selected.filter((entry) => entry.ID !== id));
  }

  return (
    <div className="entity-picker">
      <div className="picker-head">
        <span>{label}</span>
        {selected.length > 0 ? (
          <button className="link-button" type="button" onClick={() => onChange([])}>
            <X size={13} /> {"Clear all"}
          </button>
        ) : null}
      </div>
      {selected.length > 0 ? (
        <div className="picker-chip-list">
          {selected.map((row) => (
            <span key={row.ID} className="picker-chip">
              {displayName(row)} <span className="mono">{row.ID}</span>
              <button type="button" onClick={() => remove(row.ID)} aria-label={`Remove ${row.ID}`}>
                <X size={12} />
              </button>
            </span>
          ))}
        </div>
      ) : null}
      <div className="picker-search">
        <Search size={15} />
        <input
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.preventDefault();
              void search();
            }
          }}
          placeholder={"Search user_id / phone / username"}
        />
        <button className="btn compact-btn" type="button" onClick={search} disabled={busy}>
          {busy ? <Loader2 size={14} className="spin" /> : "Search"}
        </button>
      </div>
      {error && <div className="picker-error">{error}</div>}
      <div className="picker-results">
        {rows.map((row) => {
          const isSelected = selected.some((entry) => entry.ID === row.ID);
          return (
            <button
              key={row.ID}
              className={`picker-row ${isSelected ? "selected" : ""}`}
              type="button"
              onClick={() => toggle(row)}
            >
              <span className="mono">{row.ID}</span>
              <strong>{displayName(row)}</strong>
              <span>{displayUsername(row.Username) || displayPhone(row.Phone) || "-"}</span>
              {isSelected ? <Check size={15} /> : null}
            </button>
          );
        })}
        {rows.length === 0 && !busy ? <div className="picker-empty">{"No results"}</div> : null}
      </div>
    </div>
  );
}

// BotPicker is the same widget over /api/bots. Verifier status is granted to a bot
// account, and an operator knows the handle rather than the id, so the grant form
// resolves it here instead of asking for a raw number.
export function BotPicker({
  label,
  value,
  onChange
}: {
  label: string;
  value: BotRow | null;
  onChange: (row: BotRow | null) => void;
}) {
  const [query, setQuery] = useState("");
  const [rows, setRows] = useState<BotRow[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function search() {
    setBusy(true);
    setError("");
    const params = new URLSearchParams({ limit: "20" });
    if (query.trim()) {
      params.set("q", query.trim().replace(/^@/, ""));
    }
    try {
      const result = await api.bots(params);
      setRows(result.rows ?? []);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    void search();
  }, []);

  return (
    <div className="entity-picker">
      <div className="picker-head">
        <span>{label}</span>
        {value ? (
          <button className="link-button" type="button" onClick={() => onChange(null)}>
            <X size={13} /> {"Clear"}
          </button>
        ) : null}
      </div>
      {value ? (
        <div className="selected-entity">
          <Check size={15} />
          <div>
            <strong>{value.FirstName || "-"}</strong>
            <span className="mono">{value.ID}</span>
          </div>
          <span>{displayUsername(value.Username) || "-"}</span>
        </div>
      ) : null}
      <div className="picker-search">
        <Search size={15} />
        <input
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.preventDefault();
              void search();
            }
          }}
          placeholder={"Bot username or id"}
        />
        <button className="btn compact-btn" type="button" onClick={search} disabled={busy}>
          {busy ? <Loader2 size={14} className="spin" /> : "Search"}
        </button>
      </div>
      {error && <div className="picker-error">{error}</div>}
      <div className="picker-results">
        {rows.map((row) => (
          <button
            key={row.ID}
            className={`picker-row ${value?.ID === row.ID ? "selected" : ""}`}
            type="button"
            onClick={() => onChange(row)}
          >
            <span className="mono">{row.ID}</span>
            <strong>{row.FirstName || "-"}</strong>
            <span>{displayUsername(row.Username) || "-"}</span>
            {row.System ? <Badge tone="warn">{"System"}</Badge> : <Badge>{"Regular"}</Badge>}
          </button>
        ))}
        {rows.length === 0 && !busy ? <div className="picker-empty">{"No results"}</div> : null}
      </div>
    </div>
  );
}

export function ChannelPicker({
  label,
  value,
  onChange
}: {
  label: string;
  value: ChannelRow | null;
  onChange: (row: ChannelRow | null) => void;
}) {
  const [query, setQuery] = useState("");
  const [rows, setRows] = useState<ChannelRow[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function search() {
    setBusy(true);
    setError("");
    const params = new URLSearchParams({ limit: "20" });
    if (query.trim()) {
      params.set("q", query.trim());
    }
    try {
      const result = await api.channels(params);
      setRows(result.rows);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    void search();
  }, []);

  return (
    <div className="entity-picker">
      <div className="picker-head">
        <span>{label}</span>
        {value ? (
          <button className="link-button" type="button" onClick={() => onChange(null)}>
            <X size={13} /> {"Clear"}
          </button>
        ) : null}
      </div>
      {value ? (
        <div className="selected-entity">
          <Check size={15} />
          <div>
            <strong>{value.Title || "-"}</strong>
            <span className="mono">{value.ID}</span>
          </div>
          <span>{displayUsername(value.Username) || channelKind(value)}</span>
        </div>
      ) : null}
      <div className="picker-search">
        <Search size={15} />
        <input
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.preventDefault();
              void search();
            }
          }}
          placeholder={"Search channel_id / username / title"}
        />
        <button className="btn compact-btn" type="button" onClick={search} disabled={busy}>
          {busy ? <Loader2 size={14} className="spin" /> : "Search"}
        </button>
      </div>
      {error && <div className="picker-error">{error}</div>}
      <div className="picker-results">
        {rows.map((row) => (
          <button
            key={row.ID}
            className={`picker-row ${value?.ID === row.ID ? "selected" : ""}`}
            type="button"
            onClick={() => onChange(row)}
          >
            <span className="mono">{row.ID}</span>
            <strong>{row.Title || "-"}</strong>
            <span>{displayUsername(row.Username) || channelKind(row)}</span>
            {row.Verified ? <Badge tone="good">{"Verified"}</Badge> : <Badge>{channelKind(row)}</Badge>}
          </button>
        ))}
        {rows.length === 0 && !busy ? <div className="picker-empty">{"No results"}</div> : null}
      </div>
    </div>
  );
}

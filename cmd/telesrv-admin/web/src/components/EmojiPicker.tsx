import { Check, Loader2, Search, X } from "./icons";
import { useEffect, useState } from "react";
import { api, errorMessage } from "../api";
import type { EmojiRow } from "../types";
import { StaticLottie } from "./StaticLottie";

function isAnimated(mime: string): boolean {
  const m = mime.toLowerCase();
  return m.includes("tgsticker") || m.includes("lottie") || m.includes("json");
}

function EmojiThumb({ row }: { row: EmojiRow }) {
  const [failed, setFailed] = useState(!isAnimated(row.MimeType));

  useEffect(() => {
    setFailed(!isAnimated(row.MimeType));
  }, [row.DocumentID, row.MimeType]);

  if (failed) {
    return <div className="emoji-picker-glyph">{row.Alt || "🙂"}</div>;
  }
  return (
    <StaticLottie
      className="emoji-picker-anim"
      cacheKey={row.DocumentID}
      loader={() => api.emojiAnimation(row.DocumentID)}
      onError={() => setFailed(true)}
    />
  );
}

// EmojiPicker searches every custom-emoji document already on this
// deployment -- unlike the Emoji admin page's per-pack browsing, this is a
// flat document search with no system/non-system distinction, so a bundled
// icon from a default pack (e.g. Topics' ✅) is just as findable as a
// hand-uploaded one. Lets the caller select a document id by clicking it
// instead of typing one from memory; used wherever a raw document id field
// otherwise has nothing else in the panel to pick from (e.g. a
// bot-verification icon).
export function EmojiPicker({
  label,
  value,
  onChange
}: {
  label: string;
  value: string;
  onChange: (documentID: string) => void;
}) {
  const [query, setQuery] = useState("");
  const [rows, setRows] = useState<EmojiRow[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const selected = rows.find((row) => row.DocumentID === value) ?? null;

  async function search() {
    setBusy(true);
    setError("");
    const params = new URLSearchParams({ limit: "24" });
    if (query.trim()) params.set("q", query.trim());
    try {
      const result = await api.emoji(params);
      setRows(result.rows ?? []);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    void search();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div className="entity-picker">
      <div className="picker-head">
        <span>{label}</span>
        {value ? (
          <button className="link-button" type="button" onClick={() => onChange("")}>
            <X size={13} /> {"Clear"}
          </button>
        ) : null}
      </div>
      {value ? (
        <div className="selected-entity">
          <Check size={15} />
          <div>
            <strong>{selected?.Alt || "—"}</strong>
            <span className="mono">{value}</span>
          </div>
          <span>{selected?.SetTitle || "-"}</span>
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
          placeholder={"Search document ID or emoji"}
        />
        <button className="btn compact-btn" type="button" onClick={search} disabled={busy}>
          {busy ? <Loader2 size={14} className="spin" /> : "Search"}
        </button>
      </div>
      {error && <div className="picker-error">{error}</div>}
      <div className="picker-results emoji-picker-results">
        {rows.map((row) => (
          <button
            key={row.DocumentID}
            className={`picker-row emoji-picker-row ${value === row.DocumentID ? "selected" : ""}`}
            type="button"
            onClick={() => onChange(row.DocumentID)}
          >
            <EmojiThumb row={row} />
            <span className="mono">{row.DocumentID}</span>
            <span>{row.SetTitle || "—"}</span>
          </button>
        ))}
        {rows.length === 0 && !busy ? <div className="picker-empty">{"No results"}</div> : null}
      </div>
    </div>
  );
}

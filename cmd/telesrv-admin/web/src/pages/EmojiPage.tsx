import { Check, ChevronRight, Copy, Loader2, RefreshCw, Search } from "lucide-react";
import { useEffect, useState } from "react";
import { api, errorMessage } from "../api";
import { copyToClipboard } from "../clipboard";
import { StaticLottie } from "../components/StaticLottie";
import { Alert, Metric, PageFrame, QueryPanel } from "../components/ui";
import type { EmojiListResponse, EmojiRow } from "../types";

function formatBytes(value: number): string {
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`;
  return `${(value / (1024 * 1024)).toFixed(1)} MB`;
}

function isAnimated(mime: string): boolean {
  const m = mime.toLowerCase();
  return m.includes("tgsticker") || m.includes("lottie") || m.includes("json");
}

function EmojiPreview({ row }: { row: EmojiRow }) {
  const [failed, setFailed] = useState(!isAnimated(row.MimeType));

  useEffect(() => {
    setFailed(!isAnimated(row.MimeType));
  }, [row.DocumentID, row.MimeType]);

  if (failed) {
    return <div className="emoji-glyph">{row.Alt || "🙂"}</div>;
  }
  // Render a static first frame (plays only on hover) so a full grid of emoji
  // does not keep every Lottie canvas animating and lag the page.
  return (
    <StaticLottie
      className="emoji-anim"
      cacheKey={row.DocumentID}
      loader={() => api.emojiAnimation(row.DocumentID)}
      onError={() => setFailed(true)}
    />
  );
}

function EmojiCard({ row }: { row: EmojiRow }) {
  const [copied, setCopied] = useState(false);

  async function copy() {
    try {
      await copyToClipboard(row.DocumentID);
      setCopied(true);
      setTimeout(() => setCopied(false), 1200);
    } catch {
      // Clipboard is best-effort.
    }
  }

  return (
    <div className="emoji-card">
      <div className="emoji-preview"><EmojiPreview row={row} /></div>
      <div className="emoji-meta">
        <span className="emoji-alt">{row.Alt || "—"}</span>
        <button className="emoji-id" type="button" onClick={copy} title={"Copy document ID"}>
          <span className="mono">{row.DocumentID}</span>
          {copied ? <Check size={12} /> : <Copy size={12} />}
        </button>
        <span className="emoji-sub">{row.SetTitle || "No set"} · {formatBytes(row.Size)}</span>
      </div>
    </div>
  );
}

export function EmojiPage() {
  const [q, setQ] = useState("");
  const [data, setData] = useState<EmojiListResponse | null>(null);
  const [cursor, setCursor] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function load(next = false) {
    setBusy(true);
    setError("");
    const params = new URLSearchParams();
    if (q.trim()) {
      params.set("q", q.trim());
    } else if (next) {
      params.set("before_id", String(cursor));
    }
    try {
      const result = await api.emoji(params);
      setData(result);
      setCursor(result.next_before_id);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    void load(false);
  }, []);

  const rows = data?.rows ?? [];

  return (
    <PageFrame
      title={"Custom Emoji"}
      eyebrow={data?.listing === false ? "Search results" : "Custom emoji catalog"}
      actions={
        <button className="btn" type="button" onClick={() => load(false)} disabled={busy}>
          <RefreshCw size={15} /> {"Refresh"}
        </button>
      }
    >
      {error && <Alert>{error}</Alert>}
      <div className="metric-row">
        <Metric label={"Emoji on page"} value={String(rows.length)} />
      </div>
      <QueryPanel>
        <form className="toolbar" onSubmit={(event) => { event.preventDefault(); void load(false); }}>
          <label className="searchbox">
            <Search size={15} />
            <input value={q} onChange={(event) => setQ(event.target.value)} placeholder={"Document ID or emoji"} />
          </label>
          <button className="btn primary icon-text" type="submit" disabled={busy}>
            {busy ? <Loader2 size={15} className="spin" /> : <Search size={15} />} {"Search"}
          </button>
          {data?.listing && data.has_more && (
            <button className="btn icon-text" type="button" onClick={() => load(true)} disabled={busy}>
              <ChevronRight size={15} /> {"Next page"}
            </button>
          )}
        </form>
      </QueryPanel>
      <p className="about-text">{"Document IDs here can be pasted into the Emoji status field on account, bot and channel profiles."}</p>
      {rows.length === 0 ? (
        <div className="empty-panel">{"No results"}</div>
      ) : (
        <div className="emoji-grid">
          {rows.map((row) => <EmojiCard key={row.DocumentID} row={row} />)}
        </div>
      )}
    </PageFrame>
  );
}

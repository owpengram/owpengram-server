import {
  Activity,
  AlertTriangle,
  BadgeCheck,
  Bot,
  Cpu,
  Database,
  Film,
  Flag,
  HardDrive,
  MemoryStick,
  Radio,
  Smile,
  Sticker,
  Users,
  UsersRound
} from "lucide-react";
import { type ReactNode, useEffect, useState } from "react";
import { api } from "../api";
import { Alert } from "../components/ui";
import type { Navigate } from "../routing";
import { formatBytes, formatQuantity } from "../lib/format";
import type { DashboardResponse } from "../types";

export function Dashboard({ navigate }: { navigate: Navigate }) {
  const [data, setData] = useState<DashboardResponse | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;
    async function load() {
      try {
        const res = await api.dashboard();
        if (!cancelled) setData(res);
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : "Failed to load dashboard");
      }
    }
    void load();
    // Refreshed periodically rather than once: counts and host load are the
    // kind of numbers an operator leaves this page open to watch.
    const timer = window.setInterval(() => void load(), 15000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, []);

  const counts = data?.counts;
  const storage = data?.storage;
  const host = data?.host;

  return (
    <div className="dashboard-layout">
      {error && <Alert>{error}</Alert>}

      <Section title="Needs attention">
        <StatTile
          icon={<Flag />}
          label="Pending reports"
          value={counts ? formatQuantity(String(counts.PendingReports)) : "…"}
          tone={counts && counts.PendingReports > 0 ? "warn" : "good"}
          href="/moderation"
          navigate={navigate}
        />
        <StatTile
          icon={<BadgeCheck />}
          label="Verification requests"
          value={counts ? formatQuantity(String(counts.PendingVerifications)) : "…"}
          tone={counts && counts.PendingVerifications > 0 ? "warn" : "good"}
          href="/verification"
          navigate={navigate}
        />
      </Section>

      <Section title="People &amp; chats">
        <StatTile icon={<Users />} label="Users" value={counts ? formatQuantity(String(counts.Users)) : "…"} href="/accounts" navigate={navigate} />
        <StatTile
          icon={<Activity />}
          label="Online now"
          value={counts ? formatQuantity(String(counts.OnlineUsers)) : "…"}
          sub="last 5 min"
          href="/accounts"
          navigate={navigate}
        />
        <StatTile icon={<Bot />} label="Bots" value={counts ? formatQuantity(String(counts.Bots)) : "…"} href="/bots" navigate={navigate} />
        <StatTile
          icon={<Radio />}
          label="Channels"
          value={counts ? formatQuantity(String(counts.BroadcastChannels)) : "…"}
          href="/channels"
          navigate={navigate}
        />
        <StatTile
          icon={<UsersRound />}
          label="Supergroups"
          value={counts ? formatQuantity(String(counts.Supergroups)) : "…"}
          href="/channels"
          navigate={navigate}
        />
      </Section>

      <Section title="Content">
        <StatTile
          icon={<Sticker />}
          label="Sticker packs"
          value={counts ? formatQuantity(String(counts.StickerSets)) : "…"}
          href="/stickers"
          navigate={navigate}
        />
        <StatTile
          icon={<Smile />}
          label="Emoji packs"
          value={counts ? formatQuantity(String(counts.EmojiSets)) : "…"}
          href="/emoji"
          navigate={navigate}
        />
        <StatTile
          icon={<Film />}
          label="GIFs"
          value={counts ? formatQuantity(String(counts.Gifs)) : "…"}
          sub="saved by users"
          href="/gif-catalog"
          navigate={navigate}
        />
        <StatTile
          icon={<Database />}
          label="Media storage used"
          value={storage ? formatBytes(storage.PhysicalBytes) : "…"}
          sub={storage ? `${storage.BackendKind} backend` : undefined}
          href="/storage"
          navigate={navigate}
        />
      </Section>

      <Section title="Server health" hint={host?.Ready ? undefined : "waiting for first sample…"}>
        <UsageTile
          icon={<Cpu />}
          label="CPU load"
          percent={host?.Ready ? host.CPUPercent : undefined}
          valueText={host?.Ready ? `${host.CPUPercent.toFixed(0)}%` : "…"}
        />
        <UsageTile
          icon={<MemoryStick />}
          label="RAM used"
          percent={host?.Ready && host.MemTotalBytes > 0 ? (host.MemUsedBytes / host.MemTotalBytes) * 100 : undefined}
          valueText={host?.Ready ? formatBytes(String(host.MemUsedBytes)) : "…"}
          sub={host?.Ready ? `of ${formatBytes(String(host.MemTotalBytes))}` : undefined}
        />
        <UsageTile
          icon={<HardDrive />}
          label="Disk free"
          percent={
            host?.Ready && host.DiskReady && host.DiskTotalBytes > 0
              ? ((host.DiskTotalBytes - host.DiskFreeBytes) / host.DiskTotalBytes) * 100
              : undefined
          }
          valueText={host?.Ready && host.DiskReady ? formatBytes(String(host.DiskFreeBytes)) : "…"}
          sub={host?.Ready && host.DiskReady ? `of ${formatBytes(String(host.DiskTotalBytes))}` : "no reading yet"}
          warnAbove={85}
        />
      </Section>
    </div>
  );
}

function Section({ title, hint, children }: { title: string; hint?: string; children: ReactNode }) {
  return (
    <div className="dashboard-section">
      <div className="dashboard-section-title">
        {title}
        {hint && <span>{hint}</span>}
      </div>
      <div className="dashboard-grid">{children}</div>
    </div>
  );
}

type Tone = "neutral" | "good" | "warn" | "danger";

function StatTile({
  icon,
  label,
  value,
  sub,
  tone = "neutral",
  href,
  navigate
}: {
  icon: ReactNode;
  label: string;
  value: string;
  sub?: string;
  tone?: Tone;
  href?: string;
  navigate?: Navigate;
}) {
  const toneClass = tone === "neutral" ? "" : ` ${tone}`;
  const body = (
    <>
      <div className="stat-tile-head">
        <span className="stat-tile-icon">{icon}</span>
        {tone === "warn" && <AlertTriangle size={15} className="stat-tile-open" />}
      </div>
      <div className="stat-tile-value">{value}</div>
      <div className="stat-tile-label">{label}</div>
      {sub && <div className="stat-tile-sub">{sub}</div>}
    </>
  );
  if (href && navigate) {
    return (
      <a
        className={`stat-tile clickable${toneClass}`}
        href={href}
        onClick={(event) => {
          event.preventDefault();
          navigate(href);
        }}
      >
        {body}
      </a>
    );
  }
  return <div className={`stat-tile${toneClass}`}>{body}</div>;
}

// UsageTile renders a host metric with a fill bar instead of a click target --
// there's no page a CPU/RAM/disk reading opens to, unlike every entity/queue
// tile above.
function UsageTile({
  icon,
  label,
  percent,
  valueText,
  sub,
  warnAbove = 90
}: {
  icon: ReactNode;
  label: string;
  percent?: number;
  valueText: string;
  sub?: string;
  warnAbove?: number;
}) {
  const clamped = percent === undefined ? 0 : Math.max(0, Math.min(100, percent));
  const tone: Tone = percent === undefined ? "neutral" : percent >= warnAbove ? "danger" : percent >= warnAbove - 15 ? "warn" : "neutral";
  const toneClass = tone === "neutral" ? "" : ` ${tone}`;
  return (
    <div className={`stat-tile${toneClass}`}>
      <div className="stat-tile-head">
        <span className="stat-tile-icon">{icon}</span>
      </div>
      <div className="stat-tile-value">{valueText}</div>
      <div className="stat-tile-label">{label}</div>
      {sub && <div className="stat-tile-sub">{sub}</div>}
      <div className="stat-tile-bar">
        <span style={{ width: `${clamped}%` }} />
      </div>
    </div>
  );
}

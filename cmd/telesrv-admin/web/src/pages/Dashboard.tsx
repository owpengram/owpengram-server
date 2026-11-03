import {
  ArrowDownToLine,
  ArrowRight,
  ArrowUpFromLine,
  ChevronRight,
  Cpu,
  Database,
  FaceRobot,
  Gear,
  HardDrive,
  Link,
  Megaphone,
  SealCheck,
  ShieldExclamation
} from "@gravity-ui/icons";
import { type ReactNode, useEffect, useMemo, useState } from "react";
import { api } from "../api";
import { ActionButton } from "../components/ActionButton";
import { AddServerLinkModal } from "../components/AddServerLinkModal";
import { useServerUpdate } from "../components/pageHeader";
import { RestartOverlay, useAdminRestartWatcher } from "./ServerSettingsPage";
import { BarLineChart, Meter, Sparkline } from "../components/charts";
import { Alert } from "../components/ui";
import { cacheGet, cacheKeys, cacheSet } from "../lib/cache";
import { formatBytes, formatQuantity } from "../lib/format";
import {
  permissionBotsRead,
  permissionBroadcastsRead,
  permissionModerationReview,
  permissionServerManage,
  permissionVerificationReview,
  useCan
} from "../permissions";
import type { Navigate } from "../routing";
import type { DashboardActivity, DashboardResponse } from "../types";

const num = (v: number | undefined) => (v === undefined ? "" : formatQuantity(String(v)));

function shortDay(date: string): string {
  const d = new Date(`${date}T00:00:00Z`);
  // English like the rest of the panel, whatever the browser's locale.
  return d.toLocaleDateString("en-GB", { day: "numeric", month: "short", timeZone: "UTC" });
}

export function Dashboard({ navigate }: { navigate: Navigate }) {
  // Both seeded from this session's last values, so the overview opens on
  // numbers; the server answers both from memory too (statscache.go).
  const [data, setData] = useState<DashboardResponse | null>(() => cacheGet<DashboardResponse>(cacheKeys.dashboard) ?? null);
  const [activity, setActivity] = useState<DashboardActivity | null>(
    () => cacheGet<DashboardActivity>(cacheKeys.dashboardActivity) ?? null
  );
  const [error, setError] = useState("");
  const [shareOpen, setShareOpen] = useState(false);
  const session = useServerUpdate();
  // New commits on the branch this server was built from -- the same check
  // as Settings > Services' Update button, surfaced here so nobody has to go
  // looking for it.
  const [commitsBehind, setCommitsBehind] = useState(0);
  // Update runs from here, so the restart is waited out here too and the
  // panel comes back on the overview, not on Server settings.
  const restartWatcher = useAdminRestartWatcher();
  const version = session.version;

  const canBroadcast = useCan(permissionBroadcastsRead);
  const canBots = useCan(permissionBotsRead);
  const canServer = useCan(permissionServerManage);
  const canModeration = useCan(permissionModerationReview);
  const canVerification = useCan(permissionVerificationReview);

  useEffect(() => {
    if (!canServer) return;
    let cancelled = false;
    async function checkCommits() {
      try {
        const result = await api.checkServerUpdates();
        if (!cancelled) setCommitsBehind(result.commits_behind);
      } catch {
        // No git checkout (an archive install) or no network: nothing to show.
      }
    }
    void checkCommits();
    const timer = window.setInterval(() => void checkCommits(), 10 * 60 * 1000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [canServer]);

  useEffect(() => {
    let cancelled = false;
    // The two load independently: counts and host load are cheap and come
    // back first; the history may take longer and must not hold them up.
    async function loadCounts() {
      try {
        const res = await api.dashboard();
        cacheSet(cacheKeys.dashboard, res);
        if (!cancelled) setData(res);
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : "Failed to load the overview");
      }
    }
    async function loadActivity() {
      try {
        const res = await api.dashboardActivity();
        cacheSet(cacheKeys.dashboardActivity, res);
        if (!cancelled) setActivity(res);
      } catch {
        // The chart just stays empty; the counts above are what matter most.
      }
    }
    void loadCounts();
    void loadActivity();
    // Host load and online count are worth watching live; history is per day.
    const fast = window.setInterval(() => void loadCounts(), 5000);
    const slow = window.setInterval(() => void loadActivity(), 120000);
    return () => {
      cancelled = true;
      window.clearInterval(fast);
      window.clearInterval(slow);
    };
  }, []);

  // The polled overview has the freshest answer; the session's copy is from sign-in.
  const update = data?.update ?? session.update;
  const counts = data?.counts;
  const storage = data?.storage;
  const host = data?.host;
  const history = data?.host_history ?? [];
  const days = activity?.days ?? [];

  const newUsers30 = days.reduce((sum, d) => sum + d.new_users, 0);
  const messages30 = days.reduce((sum, d) => sum + d.messages, 0);
  const messagesToday = days.length ? days[days.length - 1].messages : undefined;
  const chartData = useMemo(
    () => days.map((d) => ({ key: d.date, label: shortDay(d.date), bar: d.messages, line: d.new_users })),
    [days]
  );

  const memPct = host?.Ready && host.MemTotalBytes > 0 ? (host.MemUsedBytes / host.MemTotalBytes) * 100 : 0;
  const diskUsedPct =
    host?.Ready && host.DiskReady && host.DiskTotalBytes > 0 ? ((host.DiskTotalBytes - host.DiskFreeBytes) / host.DiskTotalBytes) * 100 : 0;
  const pending = (counts?.PendingReports ?? 0) + (counts?.PendingVerifications ?? 0);

  return (
    <div className="overview">
      {error && <Alert>{error}</Alert>}

      {canServer && commitsBehind > 0 && (
        <section className="update-banner" role="status">
          <span className="update-banner-icon"><ArrowDownToLine /></span>
          <div className="update-banner-text">
            <strong>{`${commitsBehind} new commit${commitsBehind === 1 ? "" : "s"} for this server`}</strong>
            <span>{"Update pulls them, rebuilds and restarts the server."}</span>
          </div>
          <ActionButton
            tone="warn"
            icon={<ArrowDownToLine />}
            label={`Update (${commitsBehind})`}
            path="/api/actions/update-server"
            payload={() => ({})}
            onDone={() => void restartWatcher.watch()}
          />
        </section>
      )}

      {update?.available && (
        <section className="update-banner" role="status">
          <span className="update-banner-icon"><ArrowUpFromLine /></span>
          <div className="update-banner-text">
            <strong>{`Server update available${update.latest ? `: ${update.latest}` : ""}`}</strong>
            <span>{version ? `This server runs ${version}.` : "A newer release is out."}</span>
          </div>
          {update.url && (
            <a className="btn" href={update.url} target="_blank" rel="noopener noreferrer">{"What's new"}</a>
          )}
          {canServer && (
            <button className="btn primary" type="button" onClick={() => navigate("/server-settings?tab=services")}>
              {"Update now"}
            </button>
          )}
        </section>
      )}

      <div className="overview-kpis">
        <Kpi
          label="Accounts"
          value={num(counts?.Users)}
          loading={!counts}
          delta={activity ? `+${formatQuantity(String(newUsers30))} in 30 days` : undefined}
          onClick={() => navigate("/accounts")}
          chart={<Sparkline values={days.map((d) => d.new_users)} label="New accounts per day" />}
        />
        <Kpi
          label="Active today"
          value={num(activity?.active_day)}
          loading={!activity}
          delta={activity ? `${formatQuantity(String(activity.active_week))} this week · ${formatQuantity(String(activity.active_month))} this month` : undefined}
        />
        <Kpi
          label="Messages today"
          value={num(messagesToday)}
          loading={!activity}
          delta={activity ? `${formatQuantity(String(messages30))} in 30 days` : undefined}
          chart={<Sparkline values={days.map((d) => d.messages)} label="Messages per day" />}
        />
        <Kpi
          label="Online now"
          value={num(counts?.OnlineUsers)}
          loading={!counts}
          tone="good"
          delta="seen in the last 5 minutes"
          live
        />
      </div>

      <div className="overview-main">
        <section className="overview-card overview-activity">
          <header className="overview-card-head">
            <h2>{"Activity"}</h2>
            <span className="overview-card-hint">{"Last 30 days, UTC"}</span>
          </header>
          {activity ? (
            <BarLineChart
              data={chartData}
              barName="Messages"
              lineName="New accounts"
              tooltip={(d) => (
                <>
                  <strong>{d.label}</strong>
                  <span><i className="dot dot--bar" />{formatQuantity(String(d.bar))} {"messages"}</span>
                  <span><i className="dot dot--line" />{formatQuantity(String(d.line ?? 0))} {"new accounts"}</span>
                </>
              )}
            />
          ) : (
            <div className="overview-chart-placeholder"><span className="skeleton" /></div>
          )}
        </section>

        <div className="overview-side">
          {(canModeration || canVerification) && (
          <section className="overview-card">
            <header className="overview-card-head">
              <h2>{"Needs attention"}</h2>
              {counts && pending === 0 && <span className="overview-card-hint">{"All clear"}</span>}
            </header>
            <div className="attention-list">
              {canModeration && (
                <AttentionRow
                  icon={<ShieldExclamation />}
                  label="Reports"
                  count={counts?.PendingReports}
                  onOpen={() => navigate("/moderation")}
                />
              )}
              {canVerification && (
                <AttentionRow
                  icon={<SealCheck />}
                  label="Verification requests"
                  count={counts?.PendingVerifications}
                  onOpen={() => navigate("/verification")}
                />
              )}
            </div>
          </section>
          )}

          {(canBroadcast || canBots || canServer) && (
          <section className="overview-card">
            <header className="overview-card-head">
              <h2>{"Quick actions"}</h2>
            </header>
            <div className="quick-actions">
              {canBroadcast && <QuickAction icon={<Megaphone />} label="Send a broadcast" onClick={() => navigate("/broadcasts")} />}
              {canBots && <QuickAction icon={<FaceRobot />} label="Create a bot" onClick={() => navigate("/bots")} />}
              {canServer && <QuickAction icon={<Link />} label="Share this server" onClick={() => setShareOpen(true)} />}
              {canServer && <QuickAction icon={<Gear />} label="Server settings" onClick={() => navigate("/server-settings")} />}
            </div>
          </section>
          )}
        </div>
      </div>

      <div className="overview-health">
        <section className="overview-card">
          <header className="overview-card-head">
            <h2><Cpu /> {"CPU"}</h2>
            <span className="overview-value">{host?.Ready ? `${host.CPUPercent.toFixed(0)}%` : "—"}</span>
          </header>
          <Sparkline values={history.map((s) => s.cpu)} max={100} height={56} label="CPU load, last 15 minutes" />
          <span className="overview-card-hint">{"Last 15 minutes"}</span>
        </section>
        <section className="overview-card">
          <header className="overview-card-head">
            <h2>{"Memory"}</h2>
            <span className="overview-value">
              {host?.Ready ? `${formatBytes(String(host.MemUsedBytes))} / ${formatBytes(String(host.MemTotalBytes))}` : "—"}
            </span>
          </header>
          <Sparkline
            values={history.map((s) => s.mem)}
            max={host?.MemTotalBytes || undefined}
            height={56}
            tone={memPct >= 90 ? "warn" : "brand"}
            label="Memory used, last 15 minutes"
          />
          <span className="overview-card-hint">{host?.Ready ? `${memPct.toFixed(0)}% used` : "Waiting for the first sample"}</span>
        </section>
        <section className="overview-card">
          <header className="overview-card-head">
            <h2><HardDrive /> {"Disk"}</h2>
            <span className="overview-value">
              {host?.Ready && host.DiskReady ? `${formatBytes(String(host.DiskFreeBytes))} free` : "—"}
            </span>
          </header>
          <Meter percent={diskUsedPct} />
          <span className="overview-card-hint">
            {host?.Ready && host.DiskReady ? `${diskUsedPct.toFixed(0)}% of ${formatBytes(String(host.DiskTotalBytes))} used` : "No reading yet"}
          </span>
        </section>
        <section className="overview-card clickable" onClick={() => navigate("/storage")}>
          <header className="overview-card-head">
            <h2><Database /> {"Media"}</h2>
            <span className="overview-value">{storage ? formatBytes(storage.PhysicalBytes) : "—"}</span>
          </header>
          <div className="inventory">
            <InventoryItem label="Photos" value={storage ? formatQuantity(storage.PhotoCount) : ""} />
            <InventoryItem label="Documents" value={storage ? formatQuantity(storage.DocumentCount) : ""} />
            <InventoryItem label="Backend" value={storage?.BackendKind ?? ""} />
          </div>
        </section>
      </div>

      <section className="overview-card">
        <header className="overview-card-head">
          <h2>{"On this server"}</h2>
        </header>
        <div className="inventory inventory--wide">
          <InventoryItem label="Bots" value={num(counts?.Bots)} onClick={() => navigate("/bots")} />
          <InventoryItem label="Channels" value={num(counts?.BroadcastChannels)} onClick={() => navigate("/channels")} />
          <InventoryItem label="Groups" value={num(counts?.Supergroups)} onClick={() => navigate("/channels")} />
          <InventoryItem label="Sticker packs" value={num(counts?.StickerSets)} onClick={() => navigate("/stickers")} />
          <InventoryItem label="Emoji packs" value={num(counts?.EmojiSets)} onClick={() => navigate("/emoji")} />
          <InventoryItem label="GIFs" value={num(counts?.Gifs)} onClick={() => navigate("/gif-catalog")} />
        </div>
      </section>

      {shareOpen && <AddServerLinkModal onClose={() => setShareOpen(false)} />}
      {restartWatcher.waiting && <RestartOverlay timedOut={false} onDismiss={restartWatcher.dismiss} logLines={restartWatcher.logLines} />}
      {restartWatcher.timedOut && <RestartOverlay timedOut={true} onDismiss={restartWatcher.dismiss} logLines={restartWatcher.logLines} />}
    </div>
  );
}

function Kpi({
  label,
  value,
  delta,
  chart,
  loading,
  tone,
  live,
  onClick
}: {
  label: string;
  value: string;
  delta?: string;
  chart?: ReactNode;
  loading: boolean;
  tone?: "good";
  live?: boolean;
  onClick?: () => void;
}) {
  return (
    <section className={`overview-card kpi${onClick ? " clickable" : ""}`} onClick={onClick}>
      <span className="kpi-label">
        {live && <i className="live-dot" aria-hidden="true" />}
        {label}
      </span>
      <strong className={`kpi-value${tone ? ` ${tone}` : ""}`}>
        {loading ? <span className="skeleton skeleton-value" aria-label="Loading" /> : value}
      </strong>
      <span className="kpi-delta">{delta ?? " "}</span>
      {chart && <div className="kpi-chart">{chart}</div>}
    </section>
  );
}

function AttentionRow({ icon, label, count, onOpen }: { icon: ReactNode; label: string; count?: number; onOpen: () => void }) {
  const waiting = (count ?? 0) > 0;
  return (
    <button type="button" className={`attention-row${waiting ? " waiting" : ""}`} onClick={onOpen}>
      <span className="attention-icon">{icon}</span>
      <span className="attention-label">{label}</span>
      <span className="attention-count">{count === undefined ? "" : formatQuantity(String(count))}</span>
      <ChevronRight />
    </button>
  );
}

function QuickAction({ icon, label, onClick }: { icon: ReactNode; label: string; onClick: () => void }) {
  return (
    <button type="button" className="quick-action" onClick={onClick}>
      <span className="quick-action-icon">{icon}</span>
      <span>{label}</span>
      <ArrowRight className="quick-action-arrow" />
    </button>
  );
}

function InventoryItem({ label, value, onClick }: { label: string; value: string; onClick?: () => void }) {
  const body = (
    <>
      <span>{label}</span>
      <strong>{value || "—"}</strong>
    </>
  );
  return onClick ? (
    <button type="button" className="inventory-item clickable" onClick={onClick}>{body}</button>
  ) : (
    <div className="inventory-item">{body}</div>
  );
}

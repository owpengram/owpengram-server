import { ArrowLeft, Calculator, RefreshCw, SlidersHorizontal, User } from "lucide-react";
import { useEffect, useState } from "react";
import { api, errorMessage } from "../api";
import { ActionButton } from "../components/ActionButton";
import { Alert, Badge, EmptyRow, LoadingSurface, Metric, PageFrame, SectionHead, SplitLayout, Summary } from "../components/ui";
import { displayUsername, formatDate, formatQuantity, formatSigned, toNumeric } from "../lib/format";
import type { Navigate } from "../routing";
import type { AccountRatingDetail, AccountRatingEventKind, AccountRatingRow } from "../types";
import { LevelBadge, RatingProgress, levelProgress } from "./AccountRatingsPage";

const eventKindLabels: Record<AccountRatingEventKind, string> = {
  stars: "Stars",
  activity: "Activity",
  moderation: "Moderation",
  manual: "Manual",
  recompute: "Recompute"
};

export function AccountRatingDetailPage({ userID, navigate }: { userID: string; navigate: Navigate }) {
  const [detail, setDetail] = useState<AccountRatingDetail | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [adjustment, setAdjustment] = useState("");

  async function load() {
    setBusy(true);
    setError("");
    try {
      setDetail(await api.accountRating(userID));
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    void load();
  }, [userID]);

  if (error && !detail) {
    return <Alert>{error}</Alert>;
  }
  if (!detail) {
    return <LoadingSurface label={busy ? "Loading rating..." : "Waiting for data"} />;
  }

  const rating = detail.rating;
  const events = detail.events ?? [];
  const pending = toNumeric(rating.PendingStars);
  const progress = levelProgress(rating);
  // user_id / amount are `,string` int64 fields on the backend, so they stay
  // decimal strings and never pass through a float.
  const payloadUserID = rating.UserID || userID;

  return (
    <PageFrame
      title={`Rating: ${displayUsername(rating.Username) || rating.FirstName || rating.UserID}`}
      eyebrow="Account rating"
      actions={
        <>
          <button className="btn icon-text" type="button" onClick={() => navigate("/account-ratings")}>
            <ArrowLeft size={15} /> Back to list
          </button>
          <button className="btn icon-text" type="button" onClick={load} disabled={busy}>
            <RefreshCw size={15} className={busy ? "spin" : ""} /> Refresh
          </button>
        </>
      }
    >
      {error && <Alert>{error}</Alert>}
      <SplitLayout
        main={
          <div className="stacked-sections">
            <section className="entity-head">
              <div>
                <div className="entity-title">{displayUsername(rating.Username) || rating.FirstName || "Unnamed"}</div>
                <div className="entity-subtitle">User ID: {rating.UserID}</div>
              </div>
              <div className="entity-badges">
                <LevelBadge level={rating.Level} />
                {pending !== 0 && <Badge tone="warn">{`Pending ${formatSigned(rating.PendingStars)}`}</Badge>}
              </div>
            </section>

            <div className="metric-row">
              <Metric label="Stars" value={formatQuantity(rating.Stars)} mono />
              <Metric label="Level" value={String(rating.Level)} tone="good" />
              <Metric
                label="Next level"
                value={rating.HasNextLevel ? formatQuantity(rating.NextLevelStars) : "Max level"}
                mono={rating.HasNextLevel}
              />
              <Metric
                label="To next level"
                value={rating.HasNextLevel ? formatQuantity(String(progress.remaining)) : "-"}
                mono
                tone={rating.HasNextLevel && progress.percent >= 80 ? "good" : "neutral"}
              />
            </div>

            <section className="section-block">
              <SectionHead title="Score breakdown" text="How the current score is composed" />
              <Breakdown rating={rating} />
              <div className="summary-grid">
                <Summary label="Current level stars" value={formatQuantity(rating.CurrentLevelStars)} mono />
                <Summary
                  label="Next level stars"
                  value={rating.HasNextLevel ? formatQuantity(rating.NextLevelStars) : "Max level"}
                  mono={rating.HasNextLevel}
                />
                <Summary label="Computed at" value={formatDate(rating.ComputedAt) || "-"} />
                <Summary label="Updated at" value={formatDate(rating.UpdatedAt) || "-"} />
              </div>
              <div className="progress-wide">
                <RatingProgress row={rating} />
              </div>
            </section>

            {pending !== 0 && (
              <section className="section-block">
                <SectionHead title="Pending change" text="Queued and not yet applied to the score" />
                <div className="summary-grid">
                  <Summary label="Pending" value={formatSigned(rating.PendingStars)} mono />
                  <Summary label="Pending since" value={formatDate(rating.PendingDate) || "-"} />
                </div>
              </section>
            )}

            <section className="section-block">
              <SectionHead title="Events" text="History of changes to this score" />
              <div className="table-wrap">
                <table className="data-table">
                  <thead>
                    <tr>
                      <th>ID</th>
                      <th>Kind</th>
                      <th>Amount</th>
                      <th>Reason</th>
                      <th>Actor</th>
                      <th>Time</th>
                    </tr>
                  </thead>
                  <tbody>
                    {events.map((row) => (
                      <tr key={row.ID}>
                        <td className="mono">{row.ID}</td>
                        <td><EventKind kind={row.Kind} /></td>
                        <td className="mono">{formatSigned(row.Amount)}</td>
                        <td className="truncate">{row.Reason || "-"}</td>
                        <td>{row.Actor || "-"}</td>
                        <td>{formatDate(row.CreatedAt) || "-"}</td>
                      </tr>
                    ))}
                    {events.length === 0 && <EmptyRow colSpan={6} />}
                  </tbody>
                </table>
              </div>
            </section>
          </div>
        }
        side={
          <section className="action-dock">
            <div className="dock-title">Actions</div>
            <button className="btn icon-text" type="button" onClick={() => navigate(`/accounts/${rating.UserID}`)}>
              <User size={15} /> Open account
            </button>
            <div className="action-stack">
              <ActionButton
                label="Recompute"
                icon={<Calculator size={15} />}
                tone="neutral"
                path="/api/actions/recompute-account-rating"
                payload={() => ({ user_id: payloadUserID })}
                onDone={load}
              />
            </div>
            <p className="bot-create-note">Recomputes the score from ledger and activity data right now, ignoring the schedule.</p>
            <div className="dock-title">Manual adjustment</div>
            <label className="duration-field">
              <span>Amount</span>
              <input
                value={adjustment}
                onChange={(event) => setAdjustment(event.target.value)}
                type="number"
                step="1"
                placeholder="-500"
              />
            </label>
            <div className="action-stack">
              <ActionButton
                label="Adjust"
                icon={<SlidersHorizontal size={15} />}
                tone="warn"
                path="/api/actions/adjust-account-rating"
                payload={() => ({
                  user_id: payloadUserID,
                  amount: String(Number.parseInt(adjustment.trim() || "0", 10) || 0)
                })}
                onDone={() => {
                  setAdjustment("");
                  void load();
                }}
              />
            </div>
            <p className="bot-create-note">Adds a manual component to the score. Use a negative number to subtract.</p>
          </section>
        }
      />
    </PageFrame>
  );
}

function Breakdown({ rating }: { rating: AccountRatingRow }) {
  // PenaltyComponent is stored as a positive magnitude and subtracted by the
  // scorer, so it is shown (and summed) as a negative contribution.
  const components = [
    { key: "stars", label: "Stars", hint: "Net Stars received minus spent", value: toNumeric(rating.StarsComponent) },
    { key: "activity", label: "Activity", hint: "Messages sent and account age", value: toNumeric(rating.ActivityComponent) },
    { key: "penalty", label: "Penalty", hint: "Moderation cases, scam/fake flags", value: -toNumeric(rating.PenaltyComponent) },
    { key: "manual", label: "Manual", hint: "Operator adjustments", value: toNumeric(rating.ManualComponent) }
  ];
  const scale = Math.max(1, ...components.map((item) => Math.abs(item.value)));
  // The score is clamped at zero, and a delayed increase sits in PendingStars
  // instead of the score, so both cases are expected rather than drift.
  const sum = Math.max(0, components.reduce((total, item) => total + item.value, 0));
  const total = toNumeric(rating.Stars);
  const pending = toNumeric(rating.PendingStars);

  return (
    <>
      <div className="breakdown-list">
        {components.map((item) => {
          const percent = Math.min(100, (Math.abs(item.value) / scale) * 100);
          const tone = item.value < 0 ? "danger" : item.value > 0 ? "good" : "";
          return (
            <div className="breakdown-row" key={item.key}>
              <div className="breakdown-label">
                <strong>{item.label}</strong>
                <small>{item.hint}</small>
              </div>
              <div className={`progress-bar ${tone}`} role="img" aria-label={String(item.value)}>
                <span style={{ width: `${percent}%` }} />
              </div>
              <div className={`breakdown-value mono ${tone}`}>{formatSigned(String(item.value))}</div>
            </div>
          );
        })}
        <div className="breakdown-row total">
          <div className="breakdown-label"><strong>Total</strong></div>
          <div className="breakdown-value mono">{formatQuantity(rating.Stars)}</div>
        </div>
      </div>
      {pending === 0 && sum !== total && (
        <Alert>{`Components sum to ${formatQuantity(String(sum))} but the score is ${formatQuantity(rating.Stars)}.`}</Alert>
      )}
      {pending !== 0 && <p className="bot-create-note">{`A pending change of ${formatSigned(rating.PendingStars)} is not reflected above yet.`}</p>}
    </>
  );
}

function EventKind({ kind }: { kind: AccountRatingEventKind }) {
  const tone = kind === "moderation" ? "danger" : kind === "manual" ? "warn" : kind === "recompute" ? "neutral" : "good";
  return <Badge tone={tone}>{eventKindLabels[kind] ?? kind}</Badge>;
}

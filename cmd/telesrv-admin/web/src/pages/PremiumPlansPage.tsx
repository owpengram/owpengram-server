import { Loader2, Plus, RefreshCw, Search } from "../components/icons";
import { useEffect, useState } from "react";
import { api, errorMessage } from "../api";
import { ActionButton } from "../components/ActionButton";
import { Alert, Badge, PageFrame } from "../components/ui";
import type { PremiumPaymentIntent, PremiumPlan } from "../types";

type PlanDraft = {
  durationDays: string;
  amountStars: string;
  label: string;
  sortOrder: string;
  enabled: boolean;
};

function draftFromPlan(plan: PremiumPlan): PlanDraft {
  return {
    durationDays: String(plan.DurationDays),
    amountStars: String(plan.AmountStars),
    label: plan.Label,
    sortOrder: String(plan.SortOrder),
    enabled: plan.Enabled
  };
}

const emptyDraft: PlanDraft = { durationDays: "30", amountStars: "100", label: "", sortOrder: "0", enabled: true };

// Premium plan catalog management: prices/durations shown to buyers via
// payments.getPremiumGiftCodeOptions and payments.getPaymentForm, plus a
// lookup-and-reverse form for a paid Stars purchase (internal/app/premium).
export function PremiumPlansPage() {
  const [plans, setPlans] = useState<PremiumPlan[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [drafts, setDrafts] = useState<Record<number, PlanDraft>>({});
  const [newMonths, setNewMonths] = useState("3");
  const [newDraft, setNewDraft] = useState<PlanDraft>(emptyDraft);

  async function load() {
    setBusy(true);
    setError("");
    try {
      const rows = (await api.premiumPlans()).rows ?? [];
      setPlans(rows);
      setDrafts(Object.fromEntries(rows.map((plan) => [plan.Months, draftFromPlan(plan)])));
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => { void load(); }, []);

  function updateDraft(months: number, patch: Partial<PlanDraft>) {
    setDrafts((prev) => ({ ...prev, [months]: { ...(prev[months] ?? emptyDraft), ...patch } }));
  }

  const sorted = [...plans].sort((x, y) => x.SortOrder - y.SortOrder || x.Months - y.Months);
  // The dearest price per month among visible plans (usually the shortest
  // one) is the baseline each card shows its discount against, as a store
  // page would.
  const baseMonthly = Math.max(0, ...plans.filter((p) => p.Enabled && p.Months > 0).map((p) => p.AmountStars / p.Months));

  return (
    <PageFrame
      title={"Premium plans"}
      actions={
        <button className="icon-btn" type="button" aria-label={"Refresh"} title={"Refresh"} onClick={() => void load()} disabled={busy}>
          {busy ? <Loader2 size={15} className="spin" /> : <RefreshCw size={15} />}
        </button>
      }
    >
      {error && <Alert>{error}</Alert>}
      <p className="plans-intro">{"What buyers see in Settings → Premium and in the Premium gift storefront. Edit a card and press Save; nothing changes until you do."}</p>
      <div className="plan-grid">
        {sorted.map((plan) => {
          const draft = drafts[plan.Months] ?? draftFromPlan(plan);
          const dirty =
            Number(draft.durationDays) !== plan.DurationDays ||
            Number(draft.amountStars) !== plan.AmountStars ||
            draft.label !== plan.Label ||
            Number(draft.sortOrder) !== plan.SortOrder;
          const perMonth = plan.Months > 0 ? Number(draft.amountStars) / plan.Months : 0;
          const saving = baseMonthly > 0 && perMonth > 0 ? Math.round((1 - perMonth / baseMonthly) * 100) : 0;
          const payload = (enabled: boolean) => ({
            months: plan.Months,
            duration_days: Number(draft.durationDays),
            amount_stars: Number(draft.amountStars),
            label: draft.label,
            sort_order: Number(draft.sortOrder),
            enabled,
            expected_version: plan.Version
          });
          return (
            <article key={plan.Months} className={`plan-card ${plan.Enabled ? "" : "off"} ${dirty ? "dirty" : ""}`}>
              <header className="plan-card-head">
                <div>
                  <span className="plan-months">{plan.Months}</span>
                  <span className="plan-months-unit">{plan.Months === 1 ? "month" : "months"}</span>
                </div>
                <div className="plan-card-badges">
                  {saving > 0 && <span className="plan-save">{`−${saving}%`}</span>}
                  {!plan.Enabled && <Badge tone="danger">{"Hidden"}</Badge>}
                </div>
              </header>
              <div className="plan-price">
                <strong>{`★ ${Number(draft.amountStars || 0).toLocaleString()}`}</strong>
                {perMonth > 0 && <span>{`★ ${Math.round(perMonth).toLocaleString()} / month`}</span>}
              </div>
              <div className="plan-fields">
                <label>
                  <span>{"Price, Stars"}</span>
                  <input type="number" min="1" value={draft.amountStars} onChange={(event) => updateDraft(plan.Months, { amountStars: event.target.value })} />
                </label>
                <label>
                  <span>{"Lasts, days"}</span>
                  <input type="number" min="1" value={draft.durationDays} onChange={(event) => updateDraft(plan.Months, { durationDays: event.target.value })} />
                </label>
                <label className="wide">
                  <span>{"Label"}</span>
                  <input value={draft.label} placeholder={`${plan.Months} months`} onChange={(event) => updateDraft(plan.Months, { label: event.target.value })} />
                </label>
                <label>
                  <span>{"Order"}</span>
                  <input type="number" value={draft.sortOrder} onChange={(event) => updateDraft(plan.Months, { sortOrder: event.target.value })} />
                </label>
              </div>
              <footer className="plan-card-foot">
                <span className="plan-meta">{`${plan.ManagedBy || "console"} · v${plan.Version}`}</span>
                <div className="plan-card-actions">
                  <ActionButton
                    compact
                    tone="neutral"
                    label={plan.Enabled ? "Hide" : "Show"}
                    path="/api/actions/premium-upsert-plan"
                    payload={() => payload(!plan.Enabled)}
                    onDone={() => void load()}
                  />
                  {dirty && (
                    <ActionButton compact tone="primary" label={"Save"} path="/api/actions/premium-upsert-plan" payload={() => payload(plan.Enabled)} onDone={() => void load()} />
                  )}
                </div>
              </footer>
            </article>
          );
        })}

        <article className="plan-card plan-new">
          <header className="plan-card-head">
            <div>
              <span className="plan-months-unit">{"New plan"}</span>
            </div>
            <Plus size={16} />
          </header>
          <div className="plan-fields">
            <label>
              <span>{"Months"}</span>
              <input type="number" min="1" max="120" value={newMonths} onChange={(event) => setNewMonths(event.target.value)} />
            </label>
            <label>
              <span>{"Lasts, days"}</span>
              <input type="number" min="1" value={newDraft.durationDays} onChange={(event) => setNewDraft((p) => ({ ...p, durationDays: event.target.value }))} />
            </label>
            <label>
              <span>{"Price, Stars"}</span>
              <input type="number" min="1" value={newDraft.amountStars} onChange={(event) => setNewDraft((p) => ({ ...p, amountStars: event.target.value }))} />
            </label>
            <label>
              <span>{"Order"}</span>
              <input type="number" value={newDraft.sortOrder} onChange={(event) => setNewDraft((p) => ({ ...p, sortOrder: event.target.value }))} />
            </label>
            <label className="wide">
              <span>{"Label"}</span>
              <input value={newDraft.label} placeholder={"e.g. 3 months"} onChange={(event) => setNewDraft((p) => ({ ...p, label: event.target.value }))} />
            </label>
          </div>
          <footer className="plan-card-foot">
            <label className="gift-switch">
              <input type="checkbox" checked={newDraft.enabled} onChange={(event) => setNewDraft((p) => ({ ...p, enabled: event.target.checked }))} />
              <span className="gift-switch-track" aria-hidden="true"><span /></span>
              <span>{"Visible"}</span>
            </label>
            <ActionButton
              compact
              tone="primary"
              label={"Add plan"}
              icon={<Plus size={14} />}
              path="/api/actions/premium-upsert-plan"
              payload={() => ({
                months: Number(newMonths),
                duration_days: Number(newDraft.durationDays),
                amount_stars: Number(newDraft.amountStars),
                label: newDraft.label,
                sort_order: Number(newDraft.sortOrder),
                enabled: newDraft.enabled,
                expected_version: 0
              })}
              onDone={() => {
                setNewDraft(emptyDraft);
                void load();
              }}
            />
          </footer>
        </article>
      </div>
      {plans.length === 0 && !busy && <div className="plans-empty">{"No plans yet. Add the first one above."}</div>}

      <RefundSection />
    </PageFrame>
  );
}

function RefundSection() {
  const [paymentID, setPaymentID] = useState("");
  const [payment, setPayment] = useState<PremiumPaymentIntent | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function lookup() {
    if (!paymentID.trim()) return;
    setBusy(true);
    setError("");
    setPayment(null);
    try {
      setPayment(await api.premiumPayment(paymentID.trim()));
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="refund-panel">
      <header>
        <h2>{"Refund a purchase"}</h2>
        <p>{"Look up a paid Premium purchase by its payment intent ID and return the Stars."}</p>
      </header>
      <form className="refund-search" onSubmit={(event) => { event.preventDefault(); void lookup(); }}>
        <label className="searchbox">
          <Search size={15} />
          <input value={paymentID} onChange={(event) => setPaymentID(event.target.value)} placeholder={"Payment intent ID"} />
        </label>
        <button className="btn icon-text" type="submit" disabled={busy || !paymentID.trim()}>
          {busy ? <Loader2 size={15} className="spin" /> : <Search size={15} />} {"Look up"}
        </button>
      </form>
      {error && <Alert>{error}</Alert>}
      {payment && (
        <div className="refund-card">
          <dl>
            <div><dt>{"Kind"}</dt><dd>{payment.Kind}</dd></div>
            <div><dt>{"Buyer"}</dt><dd className="mono">{payment.BuyerUserID}</dd></div>
            <div><dt>{"Recipient"}</dt><dd className="mono">{payment.RecipientUserID}</dd></div>
            <div><dt>{"Months"}</dt><dd>{payment.Months}</dd></div>
            <div><dt>{"Amount"}</dt><dd>{`★ ${payment.AmountStars}`}</dd></div>
            <div><dt>{"Status"}</dt><dd><Badge tone={payment.Status === "paid" ? "good" : "neutral"}>{payment.Status}</Badge></dd></div>
          </dl>
          {payment.Status === "paid" ? (
            <ActionButton
              tone="danger"
              label={"Refund this purchase"}
              path="/api/actions/premium-refund"
              payload={() => ({ payment_intent_id: payment.ID })}
              onDone={() => void lookup()}
            />
          ) : (
            <span className="refund-note">{`Can't be refunded (status: ${payment.Status})`}</span>
          )}
        </div>
      )}
    </section>
  );
}

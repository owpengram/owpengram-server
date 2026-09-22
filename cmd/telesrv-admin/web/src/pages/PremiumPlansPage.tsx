import { Loader2, Plus, RefreshCw, Search } from "lucide-react";
import { useEffect, useState } from "react";
import { api, errorMessage } from "../api";
import { ActionButton } from "../components/ActionButton";
import { Alert, Badge, EmptyRow, PageFrame } from "../components/ui";
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

  return (
    <PageFrame
      title={"Premium plans"}
      eyebrow={"Prices and durations offered by Settings → Premium and the Premium gift storefront"}
      actions={
        <button className="btn" type="button" onClick={() => void load()} disabled={busy}>
          {busy ? <Loader2 size={15} className="spin" /> : <RefreshCw size={15} />} {"Refresh"}
        </button>
      }
    >
      {error && <Alert>{error}</Alert>}
      <div className="table-wrap">
        <table className="data-table">
          <thead>
            <tr>
              <th>{"Months"}</th>
              <th>{"Duration (days)"}</th>
              <th>{"Price (Stars)"}</th>
              <th>{"Label"}</th>
              <th>{"Sort order"}</th>
              <th>{"Status"}</th>
              <th>{"Owner"}</th>
              <th>{"Version"}</th>
              <th>{"Actions"}</th>
            </tr>
          </thead>
          <tbody>
            {plans.map((plan) => {
              const draft = drafts[plan.Months] ?? draftFromPlan(plan);
              return (
                <tr key={plan.Months}>
                  <td className="mono">{plan.Months}</td>
                  <td>
                    <input
                      type="number"
                      className="small-input"
                      value={draft.durationDays}
                      onChange={(event) => updateDraft(plan.Months, { durationDays: event.target.value })}
                    />
                  </td>
                  <td>
                    <input
                      type="number"
                      className="small-input"
                      value={draft.amountStars}
                      onChange={(event) => updateDraft(plan.Months, { amountStars: event.target.value })}
                    />
                  </td>
                  <td>
                    <input
                      className="small-input"
                      value={draft.label}
                      onChange={(event) => updateDraft(plan.Months, { label: event.target.value })}
                    />
                  </td>
                  <td>
                    <input
                      type="number"
                      className="small-input"
                      value={draft.sortOrder}
                      onChange={(event) => updateDraft(plan.Months, { sortOrder: event.target.value })}
                    />
                  </td>
                  <td>{plan.Enabled ? <Badge tone="good">{"Enabled"}</Badge> : <Badge tone="danger">{"Disabled"}</Badge>}</td>
                  <td>{plan.ManagedBy}</td>
                  <td className="mono">{plan.Version}</td>
                  <td>
                    <div className="gift-table-actions">
                      <ActionButton
                        compact
                        tone="neutral"
                        label={"Save"}
                        path="/api/actions/premium-upsert-plan"
                        payload={() => ({
                          months: plan.Months,
                          duration_days: Number(draft.durationDays),
                          amount_stars: Number(draft.amountStars),
                          label: draft.label,
                          sort_order: Number(draft.sortOrder),
                          enabled: draft.enabled,
                          expected_version: plan.Version
                        })}
                        onDone={() => void load()}
                      />
                      <ActionButton
                        compact
                        tone="neutral"
                        label={plan.Enabled ? "Disable" : "Enable"}
                        path="/api/actions/premium-upsert-plan"
                        payload={() => ({
                          months: plan.Months,
                          duration_days: Number(draft.durationDays),
                          amount_stars: Number(draft.amountStars),
                          label: draft.label,
                          sort_order: Number(draft.sortOrder),
                          enabled: !plan.Enabled,
                          expected_version: plan.Version
                        })}
                        onDone={() => void load()}
                      />
                    </div>
                  </td>
                </tr>
              );
            })}
            {plans.length === 0 && !busy && <EmptyRow colSpan={9} />}
          </tbody>
        </table>
      </div>

      <section className="section-block">
        <h2>{"Add a plan"}</h2>
        <div className="card-body">
          <div className="gift-fields-grid">
            <label><span>{"Months"}</span><input type="number" min="1" max="120" value={newMonths} onChange={(event) => setNewMonths(event.target.value)} /></label>
            <label><span>{"Duration (days)"}</span><input type="number" value={newDraft.durationDays} onChange={(event) => setNewDraft((p) => ({ ...p, durationDays: event.target.value }))} /></label>
            <label><span>{"Price (Stars)"}</span><input type="number" value={newDraft.amountStars} onChange={(event) => setNewDraft((p) => ({ ...p, amountStars: event.target.value }))} /></label>
            <label><span>{"Sort order"}</span><input type="number" value={newDraft.sortOrder} onChange={(event) => setNewDraft((p) => ({ ...p, sortOrder: event.target.value }))} /></label>
          </div>
          <label className="form-field"><span>{"Label"}</span><input value={newDraft.label} placeholder={"e.g. 3 months"} onChange={(event) => setNewDraft((p) => ({ ...p, label: event.target.value }))} /></label>
          <label className="gift-switch">
            <input type="checkbox" checked={newDraft.enabled} onChange={(event) => setNewDraft((p) => ({ ...p, enabled: event.target.checked }))} />
            <span className="gift-switch-track" aria-hidden="true"><span /></span>
            <span>{"Enabled"}</span>
          </label>
          <ActionButton
            tone="primary"
            label={"Add plan"}
            icon={<Plus size={15} />}
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
            onDone={() => { setNewDraft(emptyDraft); void load(); }}
          />
        </div>
      </section>

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
    <section className="section-block">
      <h2>{"Refund a purchase"}</h2>
      <div className="card-body">
        <form className="toolbar" onSubmit={(event) => { event.preventDefault(); void lookup(); }}>
          <label className="searchbox">
            <Search size={15} />
            <input value={paymentID} onChange={(event) => setPaymentID(event.target.value)} placeholder={"Payment intent ID"} />
          </label>
          <button className="btn icon-text" type="submit" disabled={busy}>
            {busy ? <Loader2 size={15} className="spin" /> : <Search size={15} />} {"Look up"}
          </button>
        </form>
        {error && <Alert>{error}</Alert>}
        {payment && (
          <div className="attr-block">
            <div className="result-line"><span>{"Kind"}</span><strong>{payment.Kind}</strong></div>
            <div className="result-line"><span>{"Buyer"}</span><strong>{payment.BuyerUserID}</strong></div>
            <div className="result-line"><span>{"Recipient"}</span><strong>{payment.RecipientUserID}</strong></div>
            <div className="result-line"><span>{"Months"}</span><strong>{payment.Months}</strong></div>
            <div className="result-line"><span>{"Amount (Stars)"}</span><strong>{payment.AmountStars}</strong></div>
            <div className="result-line"><span>{"Status"}</span><strong>{payment.Status}</strong></div>
            {payment.Status === "paid" ? (
              <ActionButton
                tone="danger"
                label={"Refund this purchase"}
                path="/api/actions/premium-refund"
                payload={() => ({ payment_intent_id: payment.ID })}
                onDone={() => void lookup()}
              />
            ) : (
              <Badge>{`Not refundable (status: ${payment.Status})`}</Badge>
            )}
          </div>
        )}
      </div>
    </section>
  );
}

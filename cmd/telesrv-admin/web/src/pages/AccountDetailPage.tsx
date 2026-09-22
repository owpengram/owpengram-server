import { ArrowLeft, BadgeCheck, CircleAlert, ImagePlus, MonitorSmartphone, ScrollText, Settings2, Sparkles, Star, UserRound } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { api, errorMessage } from "../api";
import { AvatarModal } from "../components/AvatarModal";
import { ActionButton } from "../components/ActionButton";
import { Avatar } from "../components/Avatar";
import { AuthorizationTable } from "../components/AuthorizationTable";
import { Alert, AuditTable, Badge, LoadingSurface, PageFrame, SectionHead, Summary, UsernameCell } from "../components/ui";
import { ScamFakeActions, ScamFakeBadges } from "../components/flags";
import { ColorAction, EmojiStatusAction, LoginEmailAction, PhoneAction, ProfileNameAction, SupportAction, UsernameAction } from "../components/attributes";
import { displayName, displayPhone, displayUsername, formatDate, formatUnix, toInt } from "../lib/format";
import type { Navigate } from "../routing";
import type { AccountDetail } from "../types";

type Tab = "profile" | "devices" | "actions";

export function AccountDetailPage({ id, navigate }: { id: number; navigate: Navigate }) {
  const [detail, setDetail] = useState<AccountDetail | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [tab, setTab] = useState<Tab>("profile");
  const [months, setMonths] = useState("1");
  const [starsAmount, setStarsAmount] = useState("100");
  const [freezeUntil, setFreezeUntil] = useState(() => toDateTimeLocal(new Date(Date.now() + 7 * 86400_000)));
  const [freezeAppealURL, setFreezeAppealURL] = useState("");
  const [avatarModalOpen, setAvatarModalOpen] = useState(false);
  const [avatarVersion, setAvatarVersion] = useState(0);

  async function load() {
    setBusy(true);
    setError("");
    try {
      const next = await api.account(id);
      setDetail(next);
      if (next.Restriction.Frozen) {
        if (next.Restriction.Until) {
          setFreezeUntil(toDateTimeLocal(new Date(next.Restriction.Until)));
        }
        setFreezeAppealURL(next.Restriction.AppealURL || "");
      }
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    void load();
    setTab("profile");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);

  if (error) {
    return <Alert>{error}</Alert>;
  }
  if (!detail) {
    return <LoadingSurface label={busy ? "Loading account detail" : "Waiting for data"} />;
  }

  const account = detail.Account;
  const tabs: Array<{ key: Tab; label: string; icon: ReactNode }> = [
    { key: "profile", label: "Profile & Status", icon: <UserRound size={15} /> },
    { key: "devices", label: "Authorized Devices", icon: <MonitorSmartphone size={15} /> },
    { key: "actions", label: "Actions & Management", icon: <Settings2 size={15} /> }
  ];

  return (
    <PageFrame
      title={`Account #${account.ID}`}
      eyebrow={"Account Profile"}
      actions={<button className="btn icon-text" onClick={() => navigate("/accounts")}><ArrowLeft size={15} /> {"Back to list"}</button>}
    >
      <section className="entity-head">
        <div className="entity-head-main">
          <div className="avatar-edit-slot">
            <Avatar id={account.ID} firstName={account.FirstName} lastName={account.LastName} username={account.Username} size={64} refreshKey={avatarVersion || undefined} />
            <button
              className="icon-btn avatar-edit-btn"
              type="button"
              aria-label={"Change avatar"}
              title={"Change avatar"}
              onClick={() => setAvatarModalOpen(true)}
            >
              <ImagePlus size={13} />
            </button>
          </div>
          <div>
            <div className="entity-title">{displayName(account)}</div>
            <div className="entity-subtitle">{displayUsername(account.Username) || "No username"} · {displayPhone(account.Phone) || "No phone"}</div>
            {account.Collectibles?.length > 0 && (
              <div className="entity-subtitle">
                <UsernameCell username="" collectibles={account.Collectibles} />
              </div>
            )}
          </div>
        </div>
        <div className="entity-badges">
          {account.PremiumUntil > 0 ? <Badge tone="good">{"Premium"}</Badge> : <Badge>{"Not premium"}</Badge>}
          {detail.Verified ? <Badge tone="good">{"Verified"}</Badge> : <Badge>{"Not verified"}</Badge>}
          <ScamFakeBadges scam={detail.Scam} fake={detail.Fake} />
          {account.Frozen ? <Badge tone="danger">{"Account frozen"}</Badge> : <Badge>{"Account active"}</Badge>}
        </div>
      </section>

      <div className="toolbar" role="group" aria-label={"Account sections"}>
        {tabs.map((item) => (
          <button
            key={item.key}
            className={`btn icon-text ${tab === item.key ? "primary" : ""}`}
            type="button"
            aria-pressed={tab === item.key}
            onClick={() => setTab(item.key)}
          >
            {item.icon} {item.label}
          </button>
        ))}
      </div>

      {tab === "profile" && (
        <div className="stacked-sections">
          <div className="summary-grid">
            <Summary label={"User ID"} value={String(account.ID)} mono />
            <Summary label={"Last active"} value={formatUnix(detail.LastSeenAt) || "-"} />
            <Summary label={"Premium expires"} value={account.PremiumUntil > 0 ? formatUnix(account.PremiumUntil) : "None"} />
            <Summary label={"Updated"} value={formatDate(account.UpdatedAt) || "-"} />
            <Summary label={"Authorized devices"} value={String(detail.Authorizations.length)} />
            <Summary label={"Account flags"} value={`support=${detail.Support} bot=${detail.Bot}`} />
            <Summary label={"Restriction"} value={detail.HasRestriction ? detail.Restriction.Reason || "Restricted" : "None"} />
            <Summary label={"Frozen since"} value={detail.Restriction.Since ? formatDate(detail.Restriction.Since) : "None"} />
            <Summary label={"Appeal deadline"} value={detail.Restriction.Until ? formatDate(detail.Restriction.Until) : "None"} />
            <Summary label={"Appeal URL"} value={detail.Restriction.AppealURL || "None"} />
            <Summary label={"Created"} value={formatDate(account.CreatedAt) || "-"} />
          </div>
          {detail.About && <p className="about-text">{detail.About}</p>}
        </div>
      )}

      {tab === "devices" && (
        <section className="section-block">
          <SectionHead title={"Authorized Devices"} text={`${detail.Authorizations.length} authorizations`} />
          <AuthorizationTable rows={detail.Authorizations} userID={account.ID} onDone={load} />
        </section>
      )}

      {tab === "actions" && (
        <div className="stacked-sections">
          <div className="action-groups">
            <section className="section-block">
              <SectionHead title={"Freeze & Restriction"} text={"Blocks sign-in and marks the account for appeal review."} />
              <div className="card-body">
                <div className="attr-block">
                  <label className="duration-field">
                    <span>{"Appeal deadline"}</span>
                    <input
                      aria-label={"Freeze appeal deadline"}
                      value={freezeUntil}
                      onChange={(event) => setFreezeUntil(event.target.value)}
                      type="datetime-local"
                    />
                  </label>
                  <label className="duration-field">
                    <span>{"Appeal URL"}</span>
                    <input
                      aria-label={"Freeze appeal URL"}
                      value={freezeAppealURL}
                      onChange={(event) => setFreezeAppealURL(event.target.value)}
                      type="url"
                      placeholder="https://..."
                    />
                  </label>
                  <ActionButton
                    label={account.Frozen ? "Update freeze" : "Freeze account"}
                    icon={<CircleAlert size={15} />}
                    tone="danger"
                    path="/api/actions/set-frozen"
                    payload={() => ({
                      user_id: account.ID,
                      frozen: true,
                      freeze_until: new Date(freezeUntil).toISOString(),
                      freeze_appeal_url: freezeAppealURL.trim()
                    })}
                    onDone={load}
                  />
                  {account.Frozen && (
                    <ActionButton
                      label={"Unfreeze account"}
                      icon={<CircleAlert size={15} />}
                      path="/api/actions/set-frozen"
                      payload={() => ({ user_id: account.ID, frozen: false })}
                      onDone={load}
                    />
                  )}
                </div>
              </div>
            </section>

            <section className="section-block">
              <SectionHead title={"Premium"} />
              <div className="card-body">
                <div className="attr-block">
                  <label className="duration-field">
                    <span>{"Premium duration (months)"}</span>
                    <input
                      aria-label={"Set premium duration in months"}
                      value={months}
                      onChange={(event) => setMonths(event.target.value)}
                      type="number"
                      min="1"
                      max="120"
                    />
                  </label>
                  <div className="action-stack">
                    <ActionButton
                      label={"Set premium"}
                      icon={<Sparkles size={15} />}
                      tone="warn"
                      path="/api/actions/grant-premium"
                      payload={() => ({ user_id: account.ID, months: toInt(months) })}
                      onDone={load}
                    />
                    <ActionButton
                      label={"Clear premium"}
                      icon={<Sparkles size={15} />}
                      tone="warn"
                      path="/api/actions/grant-premium"
                      payload={() => ({ user_id: account.ID, months: 0 })}
                      onDone={load}
                    />
                  </div>
                </div>
              </div>
            </section>

            <section className="section-block">
              <SectionHead title={"Stars"} />
              <div className="card-body">
                <div className="attr-block">
                  <label className="duration-field">
                    <span>{"Amount to credit"}</span>
                    <input
                      aria-label={"Stars amount to credit"}
                      value={starsAmount}
                      onChange={(event) => setStarsAmount(event.target.value)}
                      type="number"
                      min="1"
                      max="1000000000"
                    />
                  </label>
                  <div className="action-stack">
                    <ActionButton
                      label={"Credit stars"}
                      icon={<Star size={15} />}
                      tone="warn"
                      path="/api/actions/grant-stars"
                      payload={() => ({ user_id: account.ID, amount: toInt(starsAmount) })}
                      onDone={load}
                    />
                  </div>
                </div>
              </div>
            </section>

            <section className="section-block">
              <SectionHead title={"Verification & Moderation Flags"} />
              <div className="card-body">
                <div className="action-stack">
                  <ActionButton
                    label={detail.Verified ? "Clear verified" : "Set verified"}
                    icon={<BadgeCheck size={15} />}
                    tone="warn"
                    path="/api/actions/set-verified"
                    payload={() => ({ user_id: account.ID, verified: !detail.Verified })}
                    onDone={load}
                  />
                  <ScamFakeActions idKey="user_id" id={account.ID} path="/api/actions/set-account-flags" scam={detail.Scam} fake={detail.Fake} onDone={load} />
                  <SupportAction id={account.ID} support={detail.Support} onDone={load} />
                </div>
              </div>
            </section>

            <section className="section-block">
              <SectionHead title={"Username"} />
              <div className="card-body">
                <UsernameAction idKey="user_id" id={account.ID} path="/api/actions/set-account-username" current={account.Username} onDone={load} />
              </div>
            </section>

            <section className="section-block">
              <SectionHead title={"Name"} />
              <div className="card-body">
                <ProfileNameAction id={account.ID} path="/api/actions/set-account-profile" currentFirstName={account.FirstName} currentLastName={account.LastName} onDone={load} />
              </div>
            </section>

            <section className="section-block">
              <SectionHead title={"Phone Number"} />
              <div className="card-body">
                <PhoneAction id={account.ID} path="/api/actions/set-account-phone" current={account.Phone} onDone={load} />
              </div>
            </section>

            <section className="section-block">
              <SectionHead title={"Login Email"} text={"The email used for sign-in / password-recovery, not a contact address."} />
              <div className="card-body">
                <LoginEmailAction id={account.ID} path="/api/actions/set-account-login-email" current={account.LoginEmail} onDone={load} />
              </div>
            </section>

            <section className="section-block">
              <SectionHead title={"Profile Color"} />
              <div className="card-body">
                <ColorAction idKey="user_id" id={account.ID} path="/api/actions/set-account-color" onDone={load} />
              </div>
            </section>

            <section className="section-block">
              <SectionHead title={"Emoji Status"} />
              <div className="card-body">
                <EmojiStatusAction idKey="user_id" id={account.ID} path="/api/actions/set-account-emoji-status" onDone={load} />
              </div>
            </section>
          </div>

          <section className="section-block">
            <SectionHead title={"Recent Admin Actions"} text={"Last 30 audit rows"} action={<ScrollText size={16} />} />
            <AuditTable rows={detail.AuditLogs} />
          </section>
        </div>
      )}

      {avatarModalOpen && (
        <AvatarModal
          kind="user"
          id={account.ID}
          onClose={() => setAvatarModalOpen(false)}
          onDone={() => {
            setAvatarVersion((v) => v + 1);
            void load();
          }}
        />
      )}
    </PageFrame>
  );
}

function toDateTimeLocal(date: Date): string {
  const local = new Date(date.getTime() - date.getTimezoneOffset() * 60_000);
  return local.toISOString().slice(0, 16);
}

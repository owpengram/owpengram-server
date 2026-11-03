import { ArrowLeft, BadgeCheck, CircleAlert, ImagePlus, ScrollText, Sparkles, Star } from "../components/icons";
import { Comments, Display, Gear, Gift, Lock, Person, Picture, SquareListUl } from "@gravity-ui/icons";
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
import { PrivateGate } from "../components/PrivateGate";
import { AccountChats } from "../components/chat/AccountChats";
import { AccountMediaGallery, GiftsGallery, PhotosGallery, StoriesGallery, useProfileContent } from "../components/chat/ProfileGalleries";
import type { Navigate } from "../routing";
import type { AccountDetail } from "../types";

type Tab = "info" | "chats" | "media" | "posts" | "photos" | "gifts" | "devices" | "manage";

// Tabs behind the password re-check. They share one PrivateGate for the
// whole visit to this page, so switching between them does not ask again.
const PRIVATE_TABS: Tab[] = ["chats", "media", "posts", "photos", "gifts"];
const TABS: Tab[] = ["info", "chats", "media", "posts", "photos", "gifts", "devices", "manage"];

function tabFrom(value: string | null | undefined): Tab {
  return TABS.includes(value as Tab) ? (value as Tab) : "info";
}

export function AccountDetailPage({ id, navigate, initialTab }: { id: number; navigate: Navigate; initialTab?: string | null }) {
  const [detail, setDetail] = useState<AccountDetail | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [tab, setTabState] = useState<Tab>(() => tabFrom(initialTab));
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
    setTabState(tabFrom(initialTab));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);

  // The tab is mirrored into the URL without a navigation, so a reload or a
  // shared link lands on it -- but the private tabs still ask for the
  // password, since the grant never leaves this page's memory.
  function setTab(next: Tab) {
    setTabState(next);
    const url = new URL(window.location.href);
    if (next === "info") url.searchParams.delete("tab");
    else url.searchParams.set("tab", next);
    window.history.replaceState(window.history.state, "", url.pathname + url.search);
  }

  if (error) {
    return <Alert>{error}</Alert>;
  }
  if (!detail) {
    return <LoadingSurface label={busy ? "Loading account detail" : "Waiting for data"} />;
  }

  const account = detail.Account;
  const tabs: Array<{ key: Tab; label: string; icon: ReactNode }> = [
    { key: "info", label: "Info", icon: <Person /> },
    { key: "chats", label: "Chats", icon: <Comments /> },
    { key: "media", label: "Media", icon: <Picture /> },
    { key: "posts", label: "Posts", icon: <SquareListUl /> },
    { key: "photos", label: "Photos", icon: <Picture /> },
    { key: "gifts", label: "Gifts", icon: <Gift /> },
    { key: "devices", label: "Devices", icon: <Display /> },
    { key: "manage", label: "Manage", icon: <Gear /> }
  ];
  const isPrivate = PRIVATE_TABS.includes(tab);
  const lastSeen = formatUnix(detail.LastSeenAt);

  return (
    <PageFrame
      title={displayName(account) || `Account #${account.ID}`}
      actions={<button className="btn icon-text" onClick={() => navigate("/accounts")}><ArrowLeft size={15} /> {"Back to list"}</button>}
    >
      <section className="profile-hero">
        <div className="avatar-edit-slot profile-hero-avatar">
          <Avatar id={account.ID} firstName={account.FirstName} lastName={account.LastName} username={account.Username} size={96} refreshKey={avatarVersion || undefined} />
          <button className="icon-btn avatar-edit-btn" type="button" aria-label={"Change avatar"} title={"Change avatar"} onClick={() => setAvatarModalOpen(true)}>
            <ImagePlus size={13} />
          </button>
        </div>
        <div className="profile-hero-main">
          <h2 className="profile-hero-name">
            {displayName(account) || "Deleted account"}
            {detail.Verified && <BadgeCheck size={18} className="profile-verified" aria-label={"Verified"} />}
            {account.PremiumUntil > 0 && <Star size={17} className="profile-premium" aria-label={"Premium"} />}
          </h2>
          <div className="profile-hero-status">{lastSeen ? `last seen ${lastSeen}` : "last seen a long time ago"}</div>
          <div className="profile-hero-badges">
            {account.Frozen && <Badge tone="danger">{"Frozen"}</Badge>}
            <ScamFakeBadges scam={detail.Scam} fake={detail.Fake} />
            {detail.Support && <Badge tone="good">{"Support"}</Badge>}
            {detail.Bot && <Badge>{"Bot"}</Badge>}
          </div>
        </div>
        <dl className="profile-hero-facts">
          <div>
            <dt>{"Username"}</dt>
            <dd>{displayUsername(account.Username) || "—"}</dd>
          </div>
          <div>
            <dt>{"Phone"}</dt>
            <dd>{displayPhone(account.Phone) || "—"}</dd>
          </div>
          <div>
            <dt>{"User ID"}</dt>
            <dd className="mono">{account.ID}</dd>
          </div>
          {detail.About && (
            <div className="wide">
              <dt>{"Bio"}</dt>
              <dd>{detail.About}</dd>
            </div>
          )}
          {account.Collectibles?.length > 0 && (
            <div className="wide">
              <dt>{"Collectible usernames"}</dt>
              <dd><UsernameCell username="" collectibles={account.Collectibles} /></dd>
            </div>
          )}
        </dl>
      </section>

      <nav className="profile-tabs" aria-label={"Account sections"}>
        {tabs.map((item) => (
          <button key={item.key} type="button" className={tab === item.key ? "active" : ""} aria-pressed={tab === item.key} onClick={() => setTab(item.key)}>
            {item.icon}
            {item.label}
            {PRIVATE_TABS.includes(item.key) && <Lock className="profile-tab-lock" />}
          </button>
        ))}
      </nav>

      <div hidden={!isPrivate}>
        <PrivateGate kind="user" id={account.ID} what={"This account's chats, media, posts, photos and gifts"}>
          <AccountPrivate userID={account.ID} tab={tab} navigate={navigate} />
        </PrivateGate>
      </div>

      {tab === "info" && (
        <div className="stacked-sections">
          <div className="summary-grid">
            <Summary label={"User ID"} value={String(account.ID)} mono />
            <Summary label={"Last active"} value={lastSeen || "-"} />
            <Summary label={"Premium expires"} value={account.PremiumUntil > 0 ? formatUnix(account.PremiumUntil) : "None"} />
            <Summary label={"Login email"} value={account.LoginEmail || "None"} />
            <Summary label={"Authorized devices"} value={String(detail.Authorizations.length)} />
            <Summary label={"Restriction"} value={detail.HasRestriction ? detail.Restriction.Reason || "Restricted" : "None"} />
            <Summary label={"Frozen since"} value={detail.Restriction.Since ? formatDate(detail.Restriction.Since) : "None"} />
            <Summary label={"Appeal deadline"} value={detail.Restriction.Until ? formatDate(detail.Restriction.Until) : "None"} />
            <Summary label={"Appeal URL"} value={detail.Restriction.AppealURL || "None"} />
            <Summary label={"Created"} value={formatDate(account.CreatedAt) || "-"} />
            <Summary label={"Updated"} value={formatDate(account.UpdatedAt) || "-"} />
          </div>
          <section className="section-block">
            <SectionHead title={"Recent Admin Actions"} text={"Last 30 audit rows, including every private-content view"} action={<ScrollText size={16} />} />
            <AuditTable rows={detail.AuditLogs} />
          </section>
        </div>
      )}

      {tab === "devices" && (
        <section className="section-block">
          <SectionHead title={"Authorized Devices"} text={`${detail.Authorizations.length} authorizations`} />
          <AuthorizationTable rows={detail.Authorizations} userID={account.ID} onDone={load} />
        </section>
      )}

      {tab === "manage" && (
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

// AccountPrivate is everything behind the gate, loaded once per unlock.
function AccountPrivate({ userID, tab, navigate }: { userID: number; tab: Tab; navigate: Navigate }) {
  const { content, error } = useProfileContent("user", userID);
  const needsContent = tab === "posts" || tab === "photos" || tab === "gifts";
  return (
    <>
      {tab === "chats" && <AccountChats userID={userID} navigate={navigate} />}
      {tab === "media" && <AccountMediaGallery userID={userID} />}
      {needsContent && error && <Alert>{error}</Alert>}
      {needsContent && !content && !error && <LoadingSurface label={"Loading"} />}
      {content && tab === "posts" && <StoriesGallery content={content} />}
      {content && tab === "photos" && <PhotosGallery content={content} />}
      {content && tab === "gifts" && <GiftsGallery content={content} navigate={navigate} />}
    </>
  );
}

function toDateTimeLocal(date: Date): string {
  const local = new Date(date.getTime() - date.getTimezoneOffset() * 60_000);
  return local.toISOString().slice(0, 16);
}

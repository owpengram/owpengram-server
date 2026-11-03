import { ArrowLeft, BadgeCheck, Copy, ImagePlus, ScrollText, Settings2, Trash2, UserRound } from "../components/icons";
import { useEffect, useState, type ReactNode } from "react";
import { api, errorMessage } from "../api";
import { ActionButton } from "../components/ActionButton";
import { Avatar } from "../components/Avatar";
import { AvatarModal } from "../components/AvatarModal";
import { CopyBotTokenModal } from "../components/CopyBotTokenModal";
import { Alert, AuditTable, Badge, LoadingSurface, PageFrame, SectionHead, Summary } from "../components/ui";
import { ScamFakeActions, ScamFakeBadges } from "../components/flags";
import { ColorAction, EmojiStatusAction, UsernameAction } from "../components/attributes";
import { displayUsername, formatDate } from "../lib/format";
import type { Navigate } from "../routing";
import type { BotDetail } from "../types";

type Tab = "profile" | "actions";

export function BotDetailPage({ id, navigate }: { id: number; navigate: Navigate }) {
  const [detail, setDetail] = useState<BotDetail | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [tab, setTab] = useState<Tab>("profile");
  const [avatarModalOpen, setAvatarModalOpen] = useState(false);
  const [avatarVersion, setAvatarVersion] = useState(0);
  const [copyTokenModalOpen, setCopyTokenModalOpen] = useState(false);

  async function load() {
    setBusy(true);
    setError("");
    try {
      setDetail(await api.bot(id));
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
    return <LoadingSurface label={busy ? "Loading bot detail" : "Waiting for data"} />;
  }

  const bot = detail.Bot;
  const tabs: Array<{ key: Tab; label: string; icon: ReactNode }> = [
    { key: "profile", label: "Profile & Status", icon: <UserRound size={15} /> },
    { key: "actions", label: "Actions & Management", icon: <Settings2 size={15} /> }
  ];

  return (
    <PageFrame
      title={`Bot #${bot.ID}`}
      eyebrow={"Bot Profile"}
      actions={<button className="btn icon-text" onClick={() => navigate("/bots")}><ArrowLeft size={15} /> {"Back to list"}</button>}
    >
      <section className="entity-head">
        <div className="entity-head-main">
          <div className="avatar-edit-slot">
            <Avatar id={bot.ID} firstName={bot.FirstName} username={bot.Username} size={64} refreshKey={avatarVersion || undefined} />
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
            <div className="entity-title">{bot.FirstName || "Unnamed bot"}</div>
            <div className="entity-subtitle">{displayUsername(bot.Username) || "No username"}</div>
          </div>
        </div>
        <div className="entity-badges">
          <Badge tone={bot.System ? "warn" : "neutral"}>{bot.System ? "System" : "User"}</Badge>
          {bot.Verified ? <Badge tone="good">{"Verified"}</Badge> : <Badge>{"Not verified"}</Badge>}
          <ScamFakeBadges scam={bot.Scam} fake={bot.Fake} />
        </div>
      </section>

      <div className="toolbar" role="group" aria-label={"Bot sections"}>
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
            <Summary label={"Bot ID"} value={String(bot.ID)} mono />
            <Summary label={"Owner"} value={bot.OwnerUserID > 0 ? `${bot.OwnerUserID} ${displayUsername(detail.OwnerUsername)}`.trim() : "None"} />
            <Summary label={"Type"} value={bot.System ? "System" : "User"} />
            <Summary label={"Updated"} value={formatDate(bot.UpdatedAt) || "-"} />
            <Summary label={"Created"} value={formatDate(bot.CreatedAt) || "-"} />
          </div>
          {detail.About && <p className="about-text">{detail.About}</p>}
          {detail.Description && detail.Description.trim() !== detail.About.trim() && <p className="about-text">{detail.Description}</p>}
        </div>
      )}

      {tab === "actions" && (
        <div className="stacked-sections">
          <div className="action-groups">
            <section className="section-block">
              <SectionHead title={"Verification & Moderation Flags"} />
              <div className="card-body">
                <div className="action-stack">
                  <ActionButton
                    label={bot.Verified ? "Clear verified" : "Set verified"}
                    icon={<BadgeCheck size={15} />}
                    tone="neutral"
                    path="/api/actions/set-verified"
                    payload={() => ({ user_id: bot.ID, verified: !bot.Verified })}
                    onDone={load}
                  />
                  <ScamFakeActions idKey="user_id" id={bot.ID} path="/api/actions/set-account-flags" scam={bot.Scam} fake={bot.Fake} onDone={load} />
                </div>
              </div>
            </section>

            <section className="section-block">
              <SectionHead title={"Username"} />
              <div className="card-body">
                <UsernameAction idKey="user_id" id={bot.ID} path="/api/actions/set-account-username" current={bot.Username} onDone={load} />
              </div>
            </section>

            <section className="section-block">
              <SectionHead title={"Profile Color"} />
              <div className="card-body">
                <ColorAction idKey="user_id" id={bot.ID} path="/api/actions/set-account-color" onDone={load} />
              </div>
            </section>

            <section className="section-block">
              <SectionHead title={"Emoji Status"} />
              <div className="card-body">
                <EmojiStatusAction idKey="user_id" id={bot.ID} path="/api/actions/set-account-emoji-status" onDone={load} />
              </div>
            </section>

            {!bot.System && (
              <section className="section-block">
                <SectionHead title={"Credentials"} />
                <div className="card-body">
                  <div className="action-stack">
                    <button className="btn icon-text" type="button" onClick={() => setCopyTokenModalOpen(true)}>
                      <Copy size={15} /> {"Copy token"}
                    </button>
                  </div>
                  <p className="bot-create-note">{"Copies straight to the clipboard through a dedicated confirmation step -- the token itself is never shown on this page."}</p>
                </div>
              </section>
            )}

            <section className="section-block">
              <SectionHead title={"Danger Zone"} />
              <div className="card-body">
                {bot.System ? (
                  <p className="bot-create-note">{"System bots are built in and cannot be deleted."}</p>
                ) : (
                  <div className="danger-zone">
                    <ActionButton
                      label={"Delete bot"}
                      icon={<Trash2 size={15} />}
                      tone="danger"
                      path="/api/actions/delete-bot"
                      payload={() => ({ bot_user_id: bot.ID })}
                      onDone={() => navigate("/bots")}
                    />
                    <p className="bot-create-note">{"Permanently deletes this user-created bot and invalidates its token. This cannot be undone."}</p>
                  </div>
                )}
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
          id={bot.ID}
          onClose={() => setAvatarModalOpen(false)}
          onDone={() => {
            setAvatarVersion((v) => v + 1);
            void load();
          }}
        />
      )}

      {copyTokenModalOpen && (
        <CopyBotTokenModal botID={bot.ID} onClose={() => setCopyTokenModalOpen(false)} />
      )}
    </PageFrame>
  );
}

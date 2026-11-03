import { ArrowLeft, BadgeCheck, ImagePlus, ScrollText, Settings2, UserRound } from "../components/icons";
import { useEffect, useState, type ReactNode } from "react";
import { api, errorMessage } from "../api";
import { ActionButton } from "../components/ActionButton";
import { Avatar } from "../components/Avatar";
import { AvatarModal } from "../components/AvatarModal";
import { Alert, AuditTable, Badge, JsonBlock, LoadingSurface, PageFrame, SectionHead, Summary } from "../components/ui";
import { ScamFakeActions, ScamFakeBadges } from "../components/flags";
import { ChannelSettingsAction, ColorAction, EmojiStatusAction, UsernameAction } from "../components/attributes";
import { channelKind, displayUsername, formatDate, formatUnix } from "../lib/format";
import type { Navigate } from "../routing";
import type { ChannelDetail } from "../types";

type Tab = "profile" | "actions";

export function ChannelDetailPage({ id, navigate }: { id: number; navigate: Navigate }) {
  const [detail, setDetail] = useState<ChannelDetail | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [tab, setTab] = useState<Tab>("profile");
  const [avatarModalOpen, setAvatarModalOpen] = useState(false);
  const [avatarVersion, setAvatarVersion] = useState(0);

  async function load() {
    setBusy(true);
    setError("");
    try {
      setDetail(await api.channel(id));
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
    return <LoadingSurface label={busy ? "Loading channel detail" : "Waiting for data"} />;
  }

  const ch = detail.Channel;
  const tabs: Array<{ key: Tab; label: string; icon: ReactNode }> = [
    { key: "profile", label: "Profile & Status", icon: <UserRound size={15} /> },
    { key: "actions", label: "Actions & Management", icon: <Settings2 size={15} /> }
  ];

  return (
    <PageFrame
      title={`${channelKind(ch)} #${ch.ID}`}
      eyebrow={"Channel Profile"}
      actions={<button className="btn icon-text" onClick={() => navigate("/channels")}><ArrowLeft size={15} /> {"Back to list"}</button>}
    >
      <section className="entity-head">
        <div className="entity-head-main">
          <div className="avatar-edit-slot">
            <Avatar id={ch.ID} kind="channel" title={ch.Title} size={64} refreshKey={avatarVersion || undefined} />
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
            <div className="entity-title">{ch.Title || "-"}</div>
            <div className="entity-subtitle">{displayUsername(ch.Username) || "No username"} · {`Creator ${ch.CreatorUserID}`}</div>
          </div>
        </div>
        <div className="entity-badges">
          <Badge>{channelKind(ch)}</Badge>
          {ch.Verified ? <Badge tone="good">{"Verified"}</Badge> : <Badge>{"Not verified"}</Badge>}
          <ScamFakeBadges scam={ch.Scam} fake={ch.Fake} />
          {ch.Deleted ? <Badge tone="danger">{"Deleted"}</Badge> : <Badge>{"Valid"}</Badge>}
        </div>
      </section>

      <div className="toolbar" role="group" aria-label={"Channel sections"}>
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
            <Summary label={"Channel ID"} value={String(ch.ID)} mono />
            <Summary label="access_hash" value={String(ch.AccessHash)} mono />
            <Summary label={"Members"} value={`${ch.ParticipantsCount} / ${"Admins"} ${ch.AdminsCount}`} />
            <Summary label={"Moderation"} value={`Banned ${ch.BannedCount} / Kicked ${ch.KickedCount}`} />
            <Summary label={"Channel flags"} value={`broadcast=${ch.Broadcast} megagroup=${ch.Megagroup} forum=${ch.Forum}`} />
            <Summary label="top / pinned / PTS" value={`${ch.TopMessageID} / ${ch.PinnedMessageID} / ${ch.PTS}`} />
            <Summary label={"Created"} value={formatUnix(ch.Date) || "-"} />
            <Summary label={"Updated"} value={formatDate(ch.UpdatedAt) || "-"} />
          </div>
          {ch.About && <p className="about-text">{ch.About}</p>}
          <section className="section-block">
            <SectionHead title={"Channel Raw Row"} text={"Database read-only snapshot"} />
            <JsonBlock value={detail.ChannelJSON} />
          </section>
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
                    label={ch.Verified ? "Clear verified" : "Set verified"}
                    icon={<BadgeCheck size={15} />}
                    tone="warn"
                    path="/api/actions/set-channel-verified"
                    payload={() => ({ channel_id: ch.ID, verified: !ch.Verified })}
                    onDone={load}
                  />
                  <ScamFakeActions idKey="channel_id" id={ch.ID} path="/api/actions/set-channel-flags" scam={ch.Scam} fake={ch.Fake} onDone={load} />
                </div>
              </div>
            </section>

            <section className="section-block">
              <SectionHead title={"Settings"} />
              <div className="card-body">
                <ChannelSettingsAction channel={ch} onDone={load} />
              </div>
            </section>

            <section className="section-block">
              <SectionHead title={"Username"} />
              <div className="card-body">
                <UsernameAction idKey="channel_id" id={ch.ID} path="/api/actions/set-channel-username" current={ch.Username} onDone={load} />
              </div>
            </section>

            <section className="section-block">
              <SectionHead title={"Profile Color"} />
              <div className="card-body">
                <ColorAction idKey="channel_id" id={ch.ID} path="/api/actions/set-channel-color" onDone={load} />
              </div>
            </section>

            <section className="section-block">
              <SectionHead title={"Emoji Status"} />
              <div className="card-body">
                <EmojiStatusAction idKey="channel_id" id={ch.ID} path="/api/actions/set-channel-emoji-status" onDone={load} />
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
          kind="channel"
          id={ch.ID}
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

import { ArrowLeft, BadgeCheck, ImagePlus, ScrollText } from "../components/icons";
import { Comments, Gear, Gift, Lock, Person, Picture, Xmark } from "@gravity-ui/icons";
import { useEffect, useState, type ReactNode } from "react";
import { api, errorMessage } from "../api";
import { ActionButton } from "../components/ActionButton";
import { Avatar } from "../components/Avatar";
import { AvatarModal } from "../components/AvatarModal";
import { Alert, AuditTable, Badge, JsonBlock, LoadingSurface, PageFrame, SectionHead, Summary } from "../components/ui";
import { ScamFakeActions, ScamFakeBadges } from "../components/flags";
import { ChannelSettingsAction, ColorAction, EmojiStatusAction, UsernameAction } from "../components/attributes";
import { channelKind, displayUsername, formatDate, formatUnix } from "../lib/format";
import { PrivateGate, useViewGrant } from "../components/PrivateGate";
import { ChatView } from "../components/chat/ChatView";
import { GiftsGallery, PhotosGallery, useProfileContent } from "../components/chat/ProfileGalleries";
import { privateView, type ChatMessage } from "../lib/privateView";
import type { Navigate } from "../routing";
import type { ChannelDetail } from "../types";

type Tab = "info" | "messages" | "photos" | "gifts" | "manage";

const PRIVATE_TABS: Tab[] = ["messages", "photos", "gifts"];
const TABS: Tab[] = ["info", "messages", "photos", "gifts", "manage"];

function tabFrom(value: string | null | undefined): Tab {
  return TABS.includes(value as Tab) ? (value as Tab) : "info";
}

export function ChannelDetailPage({ id, navigate, initialTab }: { id: number; navigate: Navigate; initialTab?: string | null }) {
  const [detail, setDetail] = useState<ChannelDetail | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [tab, setTabState] = useState<Tab>(() => tabFrom(initialTab));
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
    setTabState(tabFrom(initialTab));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);

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
    return <LoadingSurface label={busy ? "Loading channel detail" : "Waiting for data"} />;
  }

  const ch = detail.Channel;
  const broadcast = ch.Broadcast && !ch.Megagroup;
  const tabs: Array<{ key: Tab; label: string; icon: ReactNode }> = [
    { key: "info", label: "Info", icon: <Person /> },
    { key: "messages", label: broadcast ? "Posts" : "Messages", icon: <Comments /> },
    { key: "photos", label: "Photos", icon: <Picture /> },
    { key: "gifts", label: "Gifts", icon: <Gift /> },
    { key: "manage", label: "Manage", icon: <Gear /> }
  ];
  const isPrivate = PRIVATE_TABS.includes(tab);
  const members = `${ch.ParticipantsCount.toLocaleString()} ${broadcast ? "subscriber" : "member"}${ch.ParticipantsCount === 1 ? "" : "s"}`;

  return (
    <PageFrame
      title={ch.Title || `${channelKind(ch)} #${ch.ID}`}
      actions={<button className="btn icon-text" onClick={() => navigate("/channels")}><ArrowLeft size={15} /> {"Back to list"}</button>}
    >
      <section className="profile-hero">
        <div className="avatar-edit-slot profile-hero-avatar">
          <Avatar id={ch.ID} kind="channel" title={ch.Title} size={96} refreshKey={avatarVersion || undefined} />
          <button className="icon-btn avatar-edit-btn" type="button" aria-label={"Change avatar"} title={"Change avatar"} onClick={() => setAvatarModalOpen(true)}>
            <ImagePlus size={13} />
          </button>
        </div>
        <div className="profile-hero-main">
          <h2 className="profile-hero-name">
            {ch.Title || "Untitled"}
            {ch.Verified && <BadgeCheck size={18} className="profile-verified" aria-label={"Verified"} />}
          </h2>
          <div className="profile-hero-status">{`${channelKind(ch)} · ${members}`}</div>
          <div className="profile-hero-badges">
            <ScamFakeBadges scam={ch.Scam} fake={ch.Fake} />
            {ch.Deleted && <Badge tone="danger">{"Deleted"}</Badge>}
            {ch.NoForwards && <Badge>{"Protected content"}</Badge>}
          </div>
        </div>
        <dl className="profile-hero-facts">
          <div>
            <dt>{"Link"}</dt>
            <dd>{displayUsername(ch.Username) || "Private"}</dd>
          </div>
          <div>
            <dt>{"Creator"}</dt>
            <dd>
              {ch.CreatorUserID ? (
                <button type="button" className="link-btn" onClick={() => navigate(`/accounts/${ch.CreatorUserID}`)}>{`User ${ch.CreatorUserID}`}</button>
              ) : (
                "—"
              )}
            </dd>
          </div>
          <div>
            <dt>{"ID"}</dt>
            <dd className="mono">{ch.ID}</dd>
          </div>
          {ch.About && (
            <div className="wide">
              <dt>{"Description"}</dt>
              <dd>{ch.About}</dd>
            </div>
          )}
        </dl>
      </section>

      <nav className="profile-tabs" aria-label={"Channel sections"}>
        {tabs.map((item) => (
          <button key={item.key} type="button" className={tab === item.key ? "active" : ""} aria-pressed={tab === item.key} onClick={() => setTab(item.key)}>
            {item.icon}
            {item.label}
            {PRIVATE_TABS.includes(item.key) && <Lock className="profile-tab-lock" />}
          </button>
        ))}
      </nav>

      <div hidden={!isPrivate}>
        <PrivateGate kind="channel" id={ch.ID} what={broadcast ? "This channel's posts, comments, photos and gifts" : "This group's messages, photos and gifts"}>
          <ChannelPrivate channelID={ch.ID} broadcast={broadcast} tab={tab} navigate={navigate} />
        </PrivateGate>
      </div>

      {tab === "info" && (
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
          <section className="section-block">
            <SectionHead title={"Recent Admin Actions"} text={"Last 30 audit rows, including every private-content view"} action={<ScrollText size={16} />} />
            <AuditTable rows={detail.AuditLogs} />
          </section>
          <section className="section-block">
            <SectionHead title={"Channel Raw Row"} text={"Database read-only snapshot"} />
            <JsonBlock value={detail.ChannelJSON} />
          </section>
        </div>
      )}

      {tab === "manage" && (
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

// ChannelPrivate is everything behind the gate: the feed (with a comments
// drawer under channel posts), profile photos and gifts.
function ChannelPrivate({ channelID, broadcast, tab, navigate }: { channelID: number; broadcast: boolean; tab: Tab; navigate: Navigate }) {
  const { grant } = useViewGrant();
  const { content, error } = useProfileContent("channel", channelID);
  const [thread, setThread] = useState<ChatMessage | null>(null);
  return (
    <>
      {tab === "messages" && (
        <div className={`channel-feed ${thread ? "with-thread" : ""}`}>
          <section className="messenger-chat">
            <ChatView
              mode={broadcast ? "channel" : "group"}
              load={(cursor) => privateView.channelMessages(channelID, cursor, grant)}
              isOut={() => false}
              onOpenComments={setThread}
              emptyText={broadcast ? "No posts." : "No messages."}
            />
          </section>
          {thread && (
            <aside className="messenger-chat channel-thread">
              <header className="messenger-chat-head">
                <div className="messenger-chat-title">
                  <strong>{"Comments"}</strong>
                  <span>{thread.text ? thread.text.slice(0, 80) : `Post #${thread.id}`}</span>
                </div>
                <button className="icon-btn" type="button" aria-label={"Close comments"} onClick={() => setThread(null)}>
                  <Xmark />
                </button>
              </header>
              <ChatView
                key={thread.id}
                mode="group"
                load={(cursor) => privateView.channelComments(channelID, thread.id, cursor, grant).then((r) => r.page)}
                isOut={() => false}
                emptyText={"No comments."}
              />
            </aside>
          )}
        </div>
      )}
      {(tab === "photos" || tab === "gifts") && error && <Alert>{error}</Alert>}
      {(tab === "photos" || tab === "gifts") && !content && !error && <LoadingSurface label={"Loading"} />}
      {content && tab === "photos" && <PhotosGallery content={content} />}
      {content && tab === "gifts" && <GiftsGallery content={content} navigate={navigate} />}
    </>
  );
}

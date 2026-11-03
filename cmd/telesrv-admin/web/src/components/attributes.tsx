import { AtSign, LifeBuoy, Mail, Palette, Phone, Settings2, Smile, UserRound } from "./icons";
import { useEffect, useState } from "react";
import { ActionButton } from "./ActionButton";
import { toInt } from "../lib/format";
import type { ChannelRow } from "../types";

type IDKey = "user_id" | "channel_id";

// SupportAction toggles the official-support flag (users/bots only).
export function SupportAction({ id, support, onDone }: { id: number; support: boolean; onDone: () => void }) {
  return (
    <ActionButton
      label={support ? "Clear support" : "Mark as support"}
      icon={<LifeBuoy size={15} />}
      tone="neutral"
      path="/api/actions/set-support"
      payload={() => ({ user_id: id, support: !support })}
      onDone={onDone}
    />
  );
}

// UsernameAction sets or clears (empty) a username.
export function UsernameAction({ idKey, id, path, current, onDone }: {
  idKey: IDKey;
  id: number;
  path: string;
  current: string;
  onDone: () => void;
}) {
  const [username, setUsername] = useState(current.replace(/^@/, ""));
  return (
    <div className="attr-block">
      <label className="duration-field">
        <span>{"Username"}</span>
        <input value={username} onChange={(e) => setUsername(e.target.value)} placeholder="username" />
      </label>
      <ActionButton
        label={"Set username"}
        icon={<AtSign size={15} />}
        tone="neutral"
        path={path}
        payload={() => ({ [idKey]: id, username: username.trim().replace(/^@/, "") })}
        onDone={onDone}
      />
    </div>
  );
}

// ProfileNameAction force-sets a user's first and last name.
export function ProfileNameAction({ id, path, currentFirstName, currentLastName, onDone }: {
  id: number;
  path: string;
  currentFirstName: string;
  currentLastName: string;
  onDone: () => void;
}) {
  const [firstName, setFirstName] = useState(currentFirstName);
  const [lastName, setLastName] = useState(currentLastName);
  return (
    <div className="attr-block">
      <label className="duration-field">
        <span>{"First name"}</span>
        <input value={firstName} onChange={(e) => setFirstName(e.target.value)} placeholder="First name" />
      </label>
      <label className="duration-field">
        <span>{"Last name"}</span>
        <input value={lastName} onChange={(e) => setLastName(e.target.value)} placeholder="Last name" />
      </label>
      <ActionButton
        label={"Set name"}
        icon={<UserRound size={15} />}
        tone="neutral"
        path={path}
        payload={() => ({ user_id: id, first_name: firstName.trim(), last_name: lastName.trim() })}
        onDone={onDone}
      />
    </div>
  );
}

// PhoneAction force-sets a user's phone number. The backend rejects a value
// already tied to another account.
export function PhoneAction({ id, path, current, onDone }: {
  id: number;
  path: string;
  current: string;
  onDone: () => void;
}) {
  const [phone, setPhone] = useState(current);
  return (
    <div className="attr-block">
      <label className="duration-field">
        <span>{"Phone number"}</span>
        <input value={phone} onChange={(e) => setPhone(e.target.value)} placeholder="15551234567" />
      </label>
      <ActionButton
        label={"Set phone"}
        icon={<Phone size={15} />}
        tone="warn"
        path={path}
        payload={() => ({ user_id: id, phone: phone.trim() })}
        onDone={onDone}
      />
    </div>
  );
}

// LoginEmailAction sets, or (left empty) clears, the login/signup email
// factor. The backend rejects an email already tied to another account.
export function LoginEmailAction({ id, path, current, onDone }: {
  id: number;
  path: string;
  current: string;
  onDone: () => void;
}) {
  const [email, setEmail] = useState(current);
  return (
    <div className="attr-block">
      <label className="duration-field">
        <span>{"Login email"}</span>
        <input value={email} onChange={(e) => setEmail(e.target.value)} placeholder="name@example.com (empty clears it)" type="email" />
      </label>
      <ActionButton
        label={email.trim() ? "Set login email" : "Clear login email"}
        icon={<Mail size={15} />}
        tone="warn"
        path={path}
        payload={() => ({ user_id: id, email: email.trim() })}
        onDone={onDone}
      />
    </div>
  );
}

// ColorAction sets or clears a name/profile color (Layer 228 peer color).
export function ColorAction({ idKey, id, path, onDone }: {
  idKey: IDKey;
  id: number;
  path: string;
  onDone: () => void;
}) {
  const [forProfile, setForProfile] = useState(false);
  const [hasColor, setHasColor] = useState(true);
  const [color, setColor] = useState("0");
  const [bgEmoji, setBgEmoji] = useState("");
  return (
    <div className="attr-block">
      <label className="checkline"><input type="checkbox" checked={forProfile} onChange={(e) => setForProfile(e.target.checked)} /> {"Profile color"}</label>
      <label className="checkline"><input type="checkbox" checked={hasColor} onChange={(e) => setHasColor(e.target.checked)} /> {"Enable color"}</label>
      <label className="duration-field">
        <span>{"Color index"}</span>
        <input type="number" min="0" max="20" value={color} onChange={(e) => setColor(e.target.value)} />
      </label>
      <label className="duration-field">
        <span>{"Background emoji ID"}</span>
        <input value={bgEmoji} onChange={(e) => setBgEmoji(e.target.value)} placeholder="0" />
      </label>
      <ActionButton
        label={"Set color"}
        icon={<Palette size={15} />}
        tone="neutral"
        path={path}
        payload={() => ({
          [idKey]: id,
          for_profile: forProfile,
          has_color: hasColor,
          color: toInt(color),
          background_emoji_id: (bgEmoji.trim() || "0")
        })}
        onDone={onDone}
      />
    </div>
  );
}

// EmojiStatusAction sets (document id) or clears (empty) an emoji status.
export function EmojiStatusAction({ idKey, id, path, onDone }: {
  idKey: IDKey;
  id: number;
  path: string;
  onDone: () => void;
}) {
  const [documentID, setDocumentID] = useState("");
  const [until, setUntil] = useState("0");
  return (
    <div className="attr-block">
      <label className="duration-field">
        <span>{"Emoji document ID"}</span>
        <input value={documentID} onChange={(e) => setDocumentID(e.target.value)} placeholder="0 = clear" />
      </label>
      <label className="duration-field">
        <span>{"Until (unix, 0 = permanent)"}</span>
        <input type="number" min="0" value={until} onChange={(e) => setUntil(e.target.value)} />
      </label>
      <ActionButton
        label={"Set emoji status"}
        icon={<Smile size={15} />}
        tone="neutral"
        path={path}
        payload={() => ({ [idKey]: id, document_id: (documentID.trim() || "0"), until: toInt(until) })}
        onDone={onDone}
      />
    </div>
  );
}

// ChannelSettingsAction force-applies moderation settings to a channel/supergroup.
export function ChannelSettingsAction({ channel, onDone }: { channel: ChannelRow; onDone: () => void }) {
  const [gigagroup, setGigagroup] = useState(channel.Gigagroup);
  const [antispam, setAntispam] = useState(channel.AntiSpam);
  const [hidden, setHidden] = useState(channel.ParticipantsHidden);
  const [noforwards, setNoforwards] = useState(channel.NoForwards);
  const [joinToSend, setJoinToSend] = useState(channel.JoinToSend);
  const [joinRequest, setJoinRequest] = useState(channel.JoinRequest);
  const [slowmode, setSlowmode] = useState(String(channel.SlowmodeSeconds));

  // Re-sync the toggles with the persisted state whenever the channel reloads
  // (e.g. after applying a change), so previously-applied settings stay checked.
  useEffect(() => {
    setGigagroup(channel.Gigagroup);
    setAntispam(channel.AntiSpam);
    setHidden(channel.ParticipantsHidden);
    setNoforwards(channel.NoForwards);
    setJoinToSend(channel.JoinToSend);
    setJoinRequest(channel.JoinRequest);
    setSlowmode(String(channel.SlowmodeSeconds));
  }, [channel]);

  // Send only the fields the admin actually changed. The backend applies a
  // partial patch (nil = leave unchanged), so an unrelated setting is never
  // reset when another one is applied.
  function buildPatch() {
    const patch: Record<string, unknown> = { channel_id: channel.ID };
    if (gigagroup !== channel.Gigagroup) patch.gigagroup = gigagroup;
    if (antispam !== channel.AntiSpam) patch.antispam = antispam;
    if (hidden !== channel.ParticipantsHidden) patch.participants_hidden = hidden;
    if (noforwards !== channel.NoForwards) patch.noforwards = noforwards;
    if (joinToSend !== channel.JoinToSend) patch.join_to_send = joinToSend;
    if (joinRequest !== channel.JoinRequest) patch.join_request = joinRequest;
    if (toInt(slowmode) !== channel.SlowmodeSeconds) patch.slowmode_seconds = toInt(slowmode);
    return patch;
  }
  return (
    <div className="attr-block">
      <label className="checkline"><input type="checkbox" checked={gigagroup} onChange={(e) => setGigagroup(e.target.checked)} /> {"Gigagroup"}</label>
      <label className="checkline"><input type="checkbox" checked={antispam} onChange={(e) => setAntispam(e.target.checked)} /> {"Aggressive anti-spam"}</label>
      <label className="checkline"><input type="checkbox" checked={hidden} onChange={(e) => setHidden(e.target.checked)} /> {"Hide members"}</label>
      <label className="checkline"><input type="checkbox" checked={noforwards} onChange={(e) => setNoforwards(e.target.checked)} /> {"Restrict forwarding"}</label>
      <label className="checkline"><input type="checkbox" checked={joinToSend} onChange={(e) => setJoinToSend(e.target.checked)} /> {"Join to send messages"}</label>
      <label className="checkline"><input type="checkbox" checked={joinRequest} onChange={(e) => setJoinRequest(e.target.checked)} /> {"Join by request"}</label>
      <label className="duration-field">
        <span>{"Slowmode (seconds)"}</span>
        <input type="number" min="0" max="86400" value={slowmode} onChange={(e) => setSlowmode(e.target.value)} />
      </label>
      <ActionButton
        label={"Apply settings"}
        icon={<Settings2 size={15} />}
        tone="warn"
        path="/api/actions/set-channel-settings"
        payload={buildPatch}
        onDone={onDone}
      />
    </div>
  );
}

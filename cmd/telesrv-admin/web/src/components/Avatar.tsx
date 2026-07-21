import { useEffect, useState } from "react";

// Mirrors internal/web/server.go's publicAvatarGradients + initials() exactly,
// so admin-console avatars look identical to the public preview cards.
const AVATAR_GRADIENTS: [string, string][] = [
  ["#FF885E", "#FF516A"],
  ["#FFCD6A", "#FFA85C"],
  ["#82B1FF", "#665FFF"],
  ["#A0DE7E", "#54CB68"],
  ["#53EDD6", "#28C9B7"],
  ["#72D5FD", "#2A9EF1"],
  ["#E0A2F3", "#D669ED"]
];

function avatarGradient(id: number): [string, string] {
  const n = Math.abs(id) % AVATAR_GRADIENTS.length;
  return AVATAR_GRADIENTS[n];
}

function firstCodePoint(word: string): string {
  const chars = Array.from(word);
  return chars.length > 0 ? chars[0] : "";
}

function avatarInitials(firstName: string, lastName: string, username: string): string {
  const title = `${firstName} ${lastName}`.trim();
  const words = title.split(/\s+/).filter(Boolean);
  const source = words.length > 0 ? words : (username ? [username] : []);
  if (source.length === 0) return "T";
  let out = firstCodePoint(source[0]);
  if (source.length > 1) {
    out += firstCodePoint(source[source.length - 1]);
  }
  return out.toUpperCase();
}

export function Avatar({
  userID,
  firstName,
  lastName,
  username = "",
  size = 34
}: {
  userID: number;
  firstName: string;
  lastName: string;
  username?: string;
  size?: number;
}) {
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    setFailed(false);
  }, [userID]);

  if (failed) {
    const [from, to] = avatarGradient(userID);
    return (
      <div
        className="avatar-fallback"
        style={{ width: size, height: size, background: `linear-gradient(135deg, ${from}, ${to})`, fontSize: Math.round(size * 0.42) }}
      >
        {avatarInitials(firstName, lastName, username)}
      </div>
    );
  }

  return (
    <img
      className="avatar-photo-img"
      src={`/api/accounts/${userID}/avatar`}
      alt=""
      loading="lazy"
      style={{ width: size, height: size }}
      onError={() => setFailed(true)}
    />
  );
}

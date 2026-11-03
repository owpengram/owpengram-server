import { ShieldAlert, ShieldX } from "./icons";
import { ActionButton } from "./ActionButton";
import { Badge } from "./ui";

// ScamFakeBadges renders the SCAM/FAKE moderation labels when set.
export function ScamFakeBadges({ scam, fake }: { scam: boolean; fake: boolean }) {
  if (!scam && !fake) {
    return null;
  }
  return (
    <>
      {scam && <Badge tone="danger">{"SCAM"}</Badge>}
      {fake && <Badge tone="danger">{"FAKE"}</Badge>}
    </>
  );
}

// ScamFakeActions renders the two toggles. scam and fake are mutually exclusive
// (a peer is never both in Telegram), so enabling one clears the other; the
// combined setter always receives the full desired state.
export function ScamFakeActions({
  idKey,
  id,
  path,
  scam,
  fake,
  onDone
}: {
  idKey: "user_id" | "channel_id";
  id: number;
  path: string;
  scam: boolean;
  fake: boolean;
  onDone: () => void;
}) {
  return (
    <div className="action-stack">
      <ActionButton
        label={scam ? "Clear SCAM" : "Mark as SCAM"}
        icon={<ShieldAlert size={15} />}
        tone="danger"
        path={path}
        payload={() => ({ [idKey]: id, scam: !scam, fake: !scam ? false : fake })}
        onDone={onDone}
      />
      <ActionButton
        label={fake ? "Clear FAKE" : "Mark as FAKE"}
        icon={<ShieldX size={15} />}
        tone="danger"
        path={path}
        payload={() => ({ [idKey]: id, fake: !fake, scam: !fake ? false : scam })}
        onDone={onDone}
      />
    </div>
  );
}

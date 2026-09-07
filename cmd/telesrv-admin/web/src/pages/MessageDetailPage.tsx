import { ArrowLeft, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { api, errorMessage } from "../api";
import { ActionButton } from "../components/ActionButton";
import { Alert, Badge, EmptyRow, JsonBlock, LoadingSurface, PageFrame, SectionHead, SplitLayout, Summary } from "../components/ui";
import { MessageView } from "../components/MessageView";
import { formatDate, formatUnix } from "../lib/format";
import type { Navigate } from "../routing";
import type { MessageDetail } from "../types";

export function MessageDetailPage({ ownerUserID, msgID, navigate }: { ownerUserID: number; msgID: number; navigate: Navigate }) {
  const [detail, setDetail] = useState<MessageDetail | null>(null);
  const [error, setError] = useState("");

  async function load() {
    setError("");
    try {
      setDetail(await api.message(ownerUserID, msgID));
    } catch (err) {
      setError(errorMessage(err));
    }
  }

  useEffect(() => {
    void load();
  }, [ownerUserID, msgID]);

  if (error) {
    return <Alert>{error}</Alert>;
  }
  if (!detail) {
    return <LoadingSurface label={"Loading"} />;
  }

  const msg = detail.Message;
  return (
    <PageFrame
      title={`Message #${msg.BoxID}`}
      eyebrow={"Message Detail"}
      actions={<button className="btn icon-text" onClick={() => navigate("/messages/private")}><ArrowLeft size={15} /> {"Back to private messages"}</button>}
    >
      <SplitLayout
        main={
          <div className="stacked-sections">
            <MessageView
              body={msg.Body}
              media={msg.Media}
              sender={`From ${msg.FromUserID}`}
              meta={`${msg.Outgoing ? "Sent to" : "Received from"} ${msg.PeerID} · ${formatUnix(msg.Date)}`}
              badges={
                <>
                  {msg.Deleted ? <Badge tone="danger">{"Deleted"}</Badge> : <Badge>{"Live"}</Badge>}
                  <Badge>{msg.Outgoing ? "Outgoing" : "Incoming"}</Badge>
                </>
              }
            />
            <div className="summary-grid">
              <Summary label={"Message box ID"} value={String(msg.BoxID)} mono />
              <Summary label={"Private message ID"} value={String(msg.PrivateMessageID)} mono />
              <Summary label={"Message sender"} value={String(msg.MessageSenderID)} mono />
              <Summary label={"pts"} value={String(msg.PTS)} mono />
            </div>
            {/* The stored rows stay reachable, but folded: they answer "why is
                this message in this state", which is a rarer question than
                "what does it say". */}
            <details className="raw-details">
              <summary>{"Stored rows (JSON)"}</summary>
              <div className="stacked-sections">
                <section className="section-block">
                  <SectionHead title={"Message Box"} text={"message_boxes read-only snapshot"} />
                  <JsonBlock value={detail.MessageJSON} />
                </section>
                <div className="raw-grid">
                  <section className="section-block">
                    <SectionHead title={"Dialog Row"} text={"dialogs read-only snapshot"} />
                    <JsonBlock value={detail.DialogJSON} />
                  </section>
                  <section className="section-block">
                    <SectionHead title={"Private Message Row"} text={"private_messages read-only snapshot"} />
                    <JsonBlock value={detail.PrivateJSON} />
                  </section>
                </div>
              </div>
            </details>
            <section className="section-block">
              <SectionHead title={"Update Events"} text={"durable user_update_events"} />
              <div className="table-wrap">
                <table className="data-table">
                  <thead><tr><th>PTS</th><th>{"Count"}</th><th>{"Type"}</th><th>{"Time"}</th></tr></thead>
                  <tbody>
                    {detail.UpdateEvents.map((row) => <tr key={`${row.PTS}-${row.Type}`}><td>{row.PTS}</td><td>{row.PTSCount}</td><td>{row.Type}</td><td>{formatUnix(row.Date)}</td></tr>)}
                    {detail.UpdateEvents.length === 0 && <EmptyRow colSpan={4} />}
                  </tbody>
                </table>
              </div>
            </section>
            <section className="section-block">
              <SectionHead title={"Dispatch Queue"} text={"online/offline dispatch_outbox"} />
              <div className="table-wrap">
                <table className="data-table">
                  <thead><tr><th>ID</th><th>{"User ID"}</th><th>PTS</th><th>{"Type"}</th><th>{"Status"}</th><th>{"Attempts"}</th><th>{"Updated"}</th></tr></thead>
                  <tbody>
                    {detail.Outbox.map((row) => <tr key={row.ID}><td>{row.ID}</td><td>{row.TargetUserID}</td><td>{row.PTS}</td><td>{row.EventType}</td><td>{row.Status}</td><td>{row.Attempts}</td><td>{formatDate(row.UpdatedAt)}</td></tr>)}
                    {detail.Outbox.length === 0 && <EmptyRow colSpan={7} />}
                  </tbody>
                </table>
              </div>
            </section>
          </div>
        }
        side={
          <section className="action-dock">
            <div className="dock-title">{"Operations"}</div>
            <ActionButton
              label={"Delete this message"}
              icon={<Trash2 size={15} />}
              path="/api/actions/delete-messages"
              payload={() => ({ owner_user_id: msg.OwnerUserID, peer_id: msg.PeerID, ids: [msg.BoxID], revoke: true })}
              onDone={load}
            />
          </section>
        }
      />
    </PageFrame>
  );
}

import { ArrowLeft } from "lucide-react";
import { useEffect, useState } from "react";
import { api, errorMessage } from "../api";
import { MessageView } from "../components/MessageView";
import { Alert, Badge, EmptyRow, JsonBlock, LoadingSurface, PageFrame, SectionHead, Summary } from "../components/ui";
import { formatUnix } from "../lib/format";
import type { Navigate } from "../routing";
import type { GroupMessageDetail } from "../types";

export function GroupMessageDetailPage({ channelID, msgID, navigate }: { channelID: number; msgID: number; navigate: Navigate }) {
  const [detail, setDetail] = useState<GroupMessageDetail | null>(null);
  const [error, setError] = useState("");

  async function load() {
    setError("");
    try {
      setDetail(await api.groupMessage(channelID, msgID));
    } catch (err) {
      setError(errorMessage(err));
    }
  }

  useEffect(() => {
    void load();
  }, [channelID, msgID]);

  if (error) {
    return <Alert>{error}</Alert>;
  }
  if (!detail) {
    return <LoadingSurface label={"Loading"} />;
  }

  const msg = detail.Message;
  return (
    <PageFrame
      title={`Group Message #${msg.ID}`}
      eyebrow={"Message Detail"}
      actions={<button className="btn icon-text" onClick={() => navigate("/messages/groups")}><ArrowLeft size={15} /> {"Back to group messages"}</button>}
    >
      <div className="stacked-sections">
        <MessageView
          body={msg.Body}
          media={msg.Media}
          sender={msg.Post ? `Channel post in ${msg.ChannelID}` : `From ${msg.SenderUserID}`}
          meta={`${formatUnix(msg.Date)}${msg.EditDate ? ` · edited ${formatUnix(msg.EditDate)}` : ""}${msg.ViewsCount ? ` · ${msg.ViewsCount} views` : ""}`}
          badges={
            <>
              {msg.Deleted ? <Badge tone="danger">{"Deleted"}</Badge> : <Badge>{"Live"}</Badge>}
              {msg.Pinned && <Badge tone="warn">{"Pinned"}</Badge>}
              {msg.Post && <Badge>{"Channel post"}</Badge>}
            </>
          }
        />
        <div className="summary-grid">
          <Summary label={"Message ID"} value={String(msg.ID)} mono />
          <Summary label={"Channel / Group"} value={String(msg.ChannelID)} mono />
          <Summary label="From Peer" value={`${msg.FromPeerType}:${msg.FromPeerID}`} mono />
          <Summary label={"pts"} value={String(msg.PTS)} mono />
        </div>
        <details className="raw-details">
          <summary>{"Stored rows (JSON)"}</summary>
          <div className="stacked-sections">
            <section className="section-block">
              <SectionHead title={"Channel Message Row"} text={"channel_messages read-only snapshot"} />
              <JsonBlock value={detail.MessageJSON} />
            </section>
            <section className="section-block">
              <SectionHead title={"Channel Row"} text={"channels read-only snapshot"} />
              <JsonBlock value={detail.ChannelJSON} />
            </section>
          </div>
        </details>
        <section className="section-block">
          <SectionHead title={"Channel Update Events"} text={"durable channel_update_events"} />
          <div className="table-wrap">
            <table className="data-table">
              <thead><tr><th>PTS</th><th>{"Count"}</th><th>{"Type"}</th><th>{"Message ID"}</th><th>{"Sender"}</th><th>{"Time"}</th></tr></thead>
              <tbody>
                {detail.UpdateEvents.map((row) => (
                  <tr key={`${row.PTS}-${row.Type}-${row.MessageID}`}>
                    <td>{row.PTS}</td>
                    <td>{row.PTSCount}</td>
                    <td>{row.Type}</td>
                    <td>{row.MessageID}</td>
                    <td>{row.SenderUserID}</td>
                    <td>{formatUnix(row.Date)}</td>
                  </tr>
                ))}
                {detail.UpdateEvents.length === 0 && <EmptyRow colSpan={6} />}
              </tbody>
            </table>
          </div>
        </section>
        <section className="section-block">
          <SectionHead title={"Event JSON"} />
          <div className="raw-grid">
            {detail.UpdateEvents.map((row) => (
              <JsonBlock key={`${row.PTS}-${row.Type}-json`} value={row.JSON} />
            ))}
            {detail.UpdateEvents.length === 0 && <div className="empty-panel">{"No results"}</div>}
          </div>
        </section>
      </div>
    </PageFrame>
  );
}

import { Check, Copy, X } from "lucide-react";
import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { api, errorMessage } from "../api";
import { copyToClipboard } from "../clipboard";
import { Alert, LoadingSurface } from "./ui";

// AddServerLinkModal shows a ready-made owpg://addserver link for this
// exact server (host+port only -- see the Go handler's doc comment for why
// name/description/key/DC are deliberately never embedded in it) -- an
// operator hands this out (a website button, a QR code, a message
// elsewhere) and the desktop/Android client's "Add Server" form opens
// pre-filled from it, fetching the rest straight from this server itself.
export function AddServerLinkModal({ onClose }: { onClose: () => void }) {
  const [link, setLink] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    let cancelled = false;
    api.addServerLink()
      .then((result) => {
        if (!cancelled) setLink(result.link);
      })
      .catch((err) => {
        if (!cancelled) setError(errorMessage(err));
      });
    return () => {
      cancelled = true;
    };
  }, []);

  async function copy() {
    if (!link) return;
    try {
      await copyToClipboard(link);
      setCopied(true);
    } catch (err) {
      setError(errorMessage(err));
    }
  }

  return createPortal(
    <div className="modal-backdrop" role="presentation">
      <section className="modal command-modal add-server-link-modal" role="dialog" aria-modal="true" aria-label={"Share server"}>
        <div className="modal-head">
          <div>
            <div className="eyebrow">{"Server"}</div>
            <h2>{"Share server"}</h2>
          </div>
          <button className="icon-btn" type="button" onClick={onClose} aria-label={"Close"}><X size={15} /></button>
        </div>
        <div className="command-body">
          <p>{"Share this link (a button, a QR code, a message) so anyone with the OwpenGram client can add this server in one tap. It only carries the address and port -- the client fetches the name, description, and key directly from the server itself, so the link can never be tampered with to point someone at a fake identity for this address."}</p>
          {error && <Alert>{error}</Alert>}
          {!link && !error && <LoadingSurface label={"Building link..."} />}
          {link && (
            <div className="add-server-link-field">
              <label className="form-field">
                <span>{"owpg:// link"}</span>
                <textarea value={link} readOnly rows={2} onFocus={(event) => event.currentTarget.select()} />
              </label>
              <button className="btn primary icon-text" type="button" onClick={() => void copy()}>
                <Copy size={15} /> {copied ? "Copy again" : "Copy link"}
              </button>
              {copied && (
                <div className="secret-reveal">
                  <div className="secret-reveal-label"><Check size={14} /> {"Copied to clipboard."}</div>
                </div>
              )}
            </div>
          )}
        </div>
        <div className="modal-actions">
          <button className="btn" type="button" onClick={onClose}>{"Close"}</button>
        </div>
      </section>
    </div>,
    document.body
  );
}

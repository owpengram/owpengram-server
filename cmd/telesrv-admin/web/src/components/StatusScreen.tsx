import { ArrowLeft, type LucideIcon } from "lucide-react";
import type { ReactNode } from "react";
import type { Navigate } from "../routing";

// A full-height "this page is not for you" screen, in place of an alert bar
// bolted to the top of an otherwise empty page frame.
//
// The status code is set large and ghosted behind the message rather than
// spelled out in the text: an operator recognises 403 at a glance, and the
// words are then free to say the useful part -- which right is missing and who
// can grant it.
export function StatusScreen({
  code,
  icon: Icon,
  title,
  children,
  detail,
  navigate
}: {
  code: string;
  icon: LucideIcon;
  title: string;
  children: ReactNode;
  // The machine-readable thing behind the message: a permission name, a config
  // key. Shown in mono, because it is what someone will have to copy.
  detail?: string;
  navigate?: Navigate;
}) {
  return (
    <section className="status-screen">
      <span className="status-screen-code" aria-hidden="true">{code}</span>
      <div className="status-screen-body">
        <span className="status-screen-icon"><Icon size={26} /></span>
        <h1>{title}</h1>
        <p>{children}</p>
        {detail && <code className="status-screen-detail">{detail}</code>}
        {navigate && (
          <button
            className="btn primary icon-text"
            type="button"
            onClick={() => navigate("/")}
          >
            <ArrowLeft size={15} /> {"Back to overview"}
          </button>
        )}
      </div>
    </section>
  );
}

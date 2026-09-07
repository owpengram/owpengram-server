import type { ReactNode } from "react";
import type { Navigate } from "../routing";

export function AppLink({
  href,
  navigate,
  className,
  title,
  children
}: {
  href: string;
  navigate: Navigate;
  className?: string;
  title?: string;
  children: ReactNode;
}) {
  return (
    <a
      className={className}
      href={href}
      title={title}
      onClick={(event) => {
        event.preventDefault();
        navigate(href);
      }}
    >
      {children}
    </a>
  );
}

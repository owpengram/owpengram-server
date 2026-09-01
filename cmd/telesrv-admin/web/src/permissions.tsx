import { ShieldOff } from "lucide-react";
import { createContext, useContext, useMemo, type ReactNode } from "react";
import { Alert, PageFrame } from "./components/ui";
// Permission names exactly as the backend spells them
// (cmd/telesrv-admin/security.go). "*" is the wildcard an operator configures for
// a full-access session.
export const permissionAll = "*";
export const permissionPremiumManage = "premium.manage";
export const permissionBotTokenRead = "bots.token.read";
export const permissionVerificationReview = "verification.review";
export const permissionVerificationRevoke = "verification.revoke";
// Third-party verification is a separate mechanism and therefore a separate pair of
// rights: review reads the section and decides applications, manage owns the
// verifier roster, the icon catalogue and taking a granted mark away.
export const permissionBotVerificationReview = "botverification.review";
export const permissionBotVerificationManage = "botverification.manage";
// Server Settings: identity, .env, restart/update. One right, not
// review/manage -- see the constant's doc comment in security.go.
export const permissionServerManage = "server.manage";

// GET /api/session is read once at boot; the panel keeps the answer here so a
// section the session may not use is hidden instead of rendered into a 403. This
// is a convenience for the operator, not a security boundary: every route is
// checked again server-side.
type SessionFlags = {
  permissions: readonly string[];
  // Mirrors AdminSession.hide_third_party_verification. Deliberately NOT folded
  // into the permission list: it applies regardless of what the session was
  // granted (even "*"), because the feature is not fully finished rather than
  // merely restricted.
  hideThirdPartyVerification: boolean;
};

const PermissionsContext = createContext<SessionFlags>({ permissions: [], hideThirdPartyVerification: true });

export function PermissionsProvider({
  permissions,
  hideThirdPartyVerification = true,
  children
}: {
  permissions: readonly string[];
  hideThirdPartyVerification?: boolean;
  children: ReactNode;
}) {
  const value = useMemo(() => ({ permissions, hideThirdPartyVerification }), [permissions, hideThirdPartyVerification]);
  return <PermissionsContext.Provider value={value}>{children}</PermissionsContext.Provider>;
}

export function usePermissions(): { permissions: readonly string[]; can: (permission: string) => boolean } {
  const { permissions } = useContext(PermissionsContext);
  return useMemo(
    () => ({
      permissions,
      can: (permission: string) => permissions.includes(permissionAll) || permissions.includes(permission)
    }),
    [permissions]
  );
}

export function useCan(permission: string): boolean {
  return usePermissions().can(permission);
}

// useThirdPartyVerificationHidden reports the server's
// TELESRV_HIDE_THIRD_PARTY_VERIFICATION setting (default true). Unlike
// useCan, this is never overridden by a "*" session -- see SessionFlags.
export function useThirdPartyVerificationHidden(): boolean {
  return useContext(PermissionsContext).hideThirdPartyVerification;
}

// PermissionGate is what a direct URL hits: without the right the operator gets
// an explanation naming the missing permission, not an empty table that looks
// like "no data".
export function PermissionGate({ permission, children }: { permission: string; children: ReactNode }) {
  const { can } = usePermissions();
  if (can(permission)) {
    return <>{children}</>;
  }
  return <PermissionDenied permission={permission} />;
}

export function PermissionDenied({ permission }: { permission: string }) {
  return (
    <PageFrame title={"Not enough rights"} eyebrow={"Console / Access"}>
      <Alert>{`This session was not granted the ${permission} permission, so the section stays closed.`}</Alert>
      <section className="section-block">
        <div className="entity-head">
          <div>
            <div className="entity-title"><ShieldOff size={16} /> {"Section unavailable"}</div>
            <div className="entity-subtitle">{"Ask an operator to add the permission to TELESRV_ADMIN_UI_PERMISSIONS and sign in again."}</div>
          </div>
        </div>
      </section>
    </PageFrame>
  );
}

// ThirdPartyVerificationHiddenGate is what a direct URL to a third-party
// verification page hits while the feature is hidden -- distinct from
// PermissionGate because no permission grant (not even "*") changes this.
export function ThirdPartyVerificationHiddenGate({ children }: { children: ReactNode }) {
  const hidden = useThirdPartyVerificationHidden();
  if (!hidden) {
    return <>{children}</>;
  }
  return (
    <PageFrame title={"Feature hidden"} eyebrow={"Console / Third-party marks"}>
      <Alert>{"Third-party bot verification is hidden on this server (TELESRV_HIDE_THIRD_PARTY_VERIFICATION=true)."}</Alert>
      <section className="section-block">
        <div className="entity-head">
          <div>
            <div className="entity-title"><ShieldOff size={16} /> {"Not fully finished"}</div>
            <div className="entity-subtitle">{"This feature may cause unstable server behavior and is hidden by default. Set TELESRV_HIDE_THIRD_PARTY_VERIFICATION=false to re-enable it."}</div>
          </div>
        </div>
      </section>
    </PageFrame>
  );
}

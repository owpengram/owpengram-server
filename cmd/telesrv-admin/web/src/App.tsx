import { useEffect, useState } from "react";
import { api } from "./api";
import { BootScreen, Shell } from "./components/Layout";
import { SetupWizard } from "./components/SetupWizard";
import { LoginPage } from "./pages/LoginPage";
import { permissionAll, permissionServerManage, PermissionsProvider } from "./permissions";
import { Routes } from "./pages/Routes";
import { currentRoute, type RouteState } from "./routing";
import type { AdminSession } from "./types";

export function App() {
  // One GET /api/session at boot carries both the actor and the permission set the
  // signed session was issued with.
  const [session, setSession] = useState<AdminSession | null | undefined>(undefined);
  const [route, setRoute] = useState<RouteState>(() => currentRoute());

  useEffect(() => {
    const onPopState = () => setRoute(currentRoute());
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, []);

  useEffect(() => {
    api.session()
      .then((next) => setSession(next))
      // A 401 and an unreachable backend both end at the login screen; there is
      // nothing the panel can render without a session.
      .catch(() => setSession(null));
  }, []);

  const navigate = (href: string) => {
    window.history.pushState(null, "", href);
    setRoute(currentRoute());
  };

  if (session === undefined) {
    return <BootScreen />;
  }

  if (session === null) {
    return (
      <LoginPage
        onLogin={(next) => {
          // A stale/expired session can be caught on any deep link (a
          // bookmark, a page refresh mid-review), landing whoever it belongs
          // to on the login form without them having navigated there --
          // replaceState rather than a plain navigate() so signing back in
          // doesn't leave a "login" entry in browser history to land back on
          // via Back. Every login opens on the dashboard, not wherever the
          // expired session happened to be.
          window.history.replaceState(null, "", "/");
          setRoute(currentRoute());
          setSession(next);
        }}
      />
    );
  }

  // The wizard only ever shows to whoever can actually act on it -- a
  // limited operator signing in before setup is finished just sees the
  // normal (mostly empty) shell instead of a wizard whose every step would
  // 403. setup_completed undefined (an admin binary old enough to predate
  // the field) reads as "done", same convention as the type's doc comment.
  const canRunSetupWizard = (session.permissions ?? []).some(
    (permission) => permission === permissionAll || permission === permissionServerManage
  );
  if (session.setup_completed === false && canRunSetupWizard) {
    return <SetupWizard />;
  }

  return (
    <PermissionsProvider permissions={session.permissions ?? []} hideThirdPartyVerification={session.hide_third_party_verification ?? true}>
      <Shell actor={session.actor} apiLayers={session.api_layers} build={session.build} route={route} navigate={navigate} onLogout={() => setSession(null)}>
        <Routes route={route} navigate={navigate} />
      </Shell>
    </PermissionsProvider>
  );
}

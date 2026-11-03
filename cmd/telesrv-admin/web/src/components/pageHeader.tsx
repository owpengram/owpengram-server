import { createContext, useContext } from "react";

// The top bar owns the only page heading. A page does not draw its own title
// row; PageFrame portals its title and actions into these two slots instead,
// so the name of the screen is written once, in the same place on every page.
export type PageHeaderSlots = {
  title: HTMLElement | null;
  actions: HTMLElement | null;
};

export const PageHeaderContext = createContext<PageHeaderSlots>({ title: null, actions: null });

export function usePageHeaderSlots(): PageHeaderSlots {
  return useContext(PageHeaderContext);
}

// ServerUpdate is what the session knows about a newer server release (see
// internal/updatecheck); the overview turns it into a banner.
export type ServerUpdate = {
  version?: string;
  update?: { available: boolean; latest?: string; url?: string; notes?: string };
};

export const ServerUpdateContext = createContext<ServerUpdate>({});

export function useServerUpdate(): ServerUpdate {
  return useContext(ServerUpdateContext);
}

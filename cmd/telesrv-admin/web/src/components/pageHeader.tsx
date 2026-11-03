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

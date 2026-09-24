/**
 * Derived-state hooks.
 *
 * zustand v5 compares selector output by reference, so a selector that builds a
 * new array or object on every call would re-render forever. Every hook here
 * subscribes to a stable slice (`tabs`, `sessions`, `config`) and derives the
 * result with `useMemo`.
 */
import { useMemo } from "react";
import type { Pane, PaneID, Tab } from "./layout";
import type { Config, Profile } from "./protocol";
import { useApp } from "./store";

/** Pane model plus its owning tab id. */
export interface LocatedPane {
  pane: Pane;
  tab: Tab;
}

export function useLocatedPanes(): LocatedPane[] {
  const tabs = useApp((s) => s.tabs);
  return useMemo(() => {
    const out: LocatedPane[] = [];
    for (const tab of tabs) for (const pane of Object.values(tab.panes)) out.push({ pane, tab });
    return out;
  }, [tabs]);
}

export function useLocatedPane(paneId: PaneID): LocatedPane | null {
  const panes = useLocatedPanes();
  return useMemo(() => panes.find((p) => p.pane.id === paneId) ?? null, [panes, paneId]);
}

export function useActiveTab(): Tab | null {
  const tabs = useApp((s) => s.tabs);
  const activeTabId = useApp((s) => s.activeTabId);
  return useMemo(() => tabs.find((t) => t.id === activeTabId) ?? null, [tabs, activeTabId]);
}

export function useDefaultProfile(): Profile | null {
  const config = useApp((s) => s.config);
  const profiles = useApp((s) => s.profiles);
  return useMemo(() => pickUsable(config, profiles), [config, profiles]);
}

function pickUsable(config: Config | null, profiles: Profile[]): Profile | null {
  const wanted = config?.behavior.defaultProfile ?? "";
  const match = profiles.find((p) => p.id === wanted);
  if (match && match.available !== false) return match;
  return profiles.find((p) => p.available !== false) ?? profiles[0] ?? null;
}

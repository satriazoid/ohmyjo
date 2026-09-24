import { create } from "zustand";
import { api } from "./api";
import * as L from "./layout";
import type { Pane, PaneID, Tab } from "./layout";
import type { ClientMessage, Config, Profile, ServerMessage, SessionInfo, SidebarConfig } from "./protocol";
import { decodeBase64, encodeBase64 } from "./protocol";
import { terminalFor, writeToTerminal } from "./terminals";
import type { ConnectionState, Transport } from "./transport";

export type Overlay = "none" | "settings" | "palette" | "history";

export interface Notice {
  id: number;
  level: "info" | "warn" | "error";
  text: string;
}

/** Layout size reported by a pane before its session exists. */
interface PendingSize {
  cols: number;
  rows: number;
}

let counter = 0;
const nextId = (prefix: string) => `${prefix}${++counter}`;

/** Last reported config diagnostics, so reloads do not repeat warnings. */
let seenDiagnostics = "";

let transport: Transport | null = null;

/** Panes created before `created` arrives, keyed by request id. */
const pendingCreates = new Map<string, { tabId: string; paneId: PaneID }>();
/** Last measured size per session, used to seed a session on creation. */
const pendingSizes = new Map<string, PendingSize>();
const lastSize = new Map<PaneID, PendingSize>();

function findBySession(tabs: Tab[], sessionId: string): { tabId: string; paneId: PaneID } | null {
  for (const tab of tabs) {
    for (const pane of Object.values(tab.panes)) {
      if (pane.sessionId === sessionId) return { tabId: tab.id, paneId: pane.id };
    }
  }
  return null;
}

function paneById(tabs: Tab[], paneId: PaneID): { tab: Tab; pane: Pane } | null {
  for (const tab of tabs) {
    const pane = tab.panes[paneId];
    if (pane) return { tab, pane };
  }
  return null;
}

function newTabState(profile: string, cwd: string, title: string): Tab {
  const paneId = nextId("p");
  return {
    id: nextId("t"),
    title,
    root: { kind: "leaf", pane: paneId },
    panes: {
      [paneId]: { id: paneId, profile, cwd, title, status: "starting" },
    },
  };
}

/** Default profile: the configured one when usable, else the first available. */
function pickProfile(cfg: Config | null, profiles: Profile[], preferred?: string): string {
  if (preferred) return preferred;
  const wanted = cfg?.behavior.defaultProfile ?? "";
  const match = profiles.find((p) => p.id === wanted);
  if (match) return match.available === false ? (profiles.find((p) => p.available !== false)?.id ?? match.id) : match.id;
  return profiles.find((p) => p.available !== false)?.id ?? profiles[0]?.id ?? "cmd";
}

interface AppState {
  conn: ConnectionState;
  version: string;
  config: Config | null;
  configPath: string;
  profiles: Profile[];
  fonts: string[];
  tabs: Tab[];
  activeTabId: string;
  activePane: PaneID;
  sessions: Record<string, SessionInfo>;
  overlay: Overlay;
  notices: Notice[];
  /** Search bar visibility for the active pane. */
  searchOpen: boolean;
  /** Pane currently filling the stage, or "" when the split tree is shown. */
  maximizePane: PaneID | "";
  /** Font size the reset keybinding returns to (the loaded config's value). */
  baseFontSize: number;
  /** Command history per session id, newest first, as the backend reported it. */
  history: Record<string, string[]>;

  connect: (t: Transport) => void;

  newTab: (profileId?: string, cwd?: string) => void;
  /** Minimises a tab's shell to the background instead of killing it. */
  detachTab: (tabId: string) => void;
  /** Kills every session of a tab and drops it. */
  closeTab: (tabId: string) => void;
  selectTab: (tabId: string) => void;
  /** Moves `tabId` to sit where `beforeId` is, which is how drag reorder lands. */
  reorderTab: (tabId: string, beforeId: string) => void;
  /** Renames a tab; an empty name restores the shell-derived title. */
  renameTab: (tabId: string, title: string) => void;
  /** Brings a background session back into a fresh tab. */
  adoptSession: (sessionId: string) => void;
  /** Kills a session that no pane owns (a backgrounded shell). */
  killSession: (sessionId: string) => void;
  /** Maximizes the focused pane, or restores the split tree. */
  toggleMaximizePane: () => void;

  splitPane: (direction: L.SplitDir) => void;
  closePane: (paneId?: PaneID) => void;
  restartPane: (paneId?: PaneID) => void;
  duplicatePane: (paneId?: PaneID) => void;
  focusPane: (paneId: PaneID) => void;
  focusDirection: (dir: "left" | "right" | "up" | "down") => void;
  cyclePane: (delta: number) => void;
  cycleTab: (delta: number) => void;
  setRatio: (splitId: string, ratio: number) => void;

  updateConfig: (patch: (cfg: Config) => void, persist?: boolean) => void;
  updateSidebar: (patch: Partial<SidebarConfig>) => void;

  setOverlay: (overlay: Overlay) => void;
  setSearchOpen: (open: boolean) => void;
  /** Command history for a pane's shell, newest first. */
  terminalHistory: (paneId: PaneID) => string[];
  /** Asks the backend for a pane's history, optionally filtered. */
  requestHistory: (paneId: PaneID, query?: string) => void;
  /** Wipes a pane's history (and its session's record of it). */
  clearHistory: (paneId: PaneID) => void;
  /** Inserts a history entry into the focused pane without running it. */
  recallCommand: (command: string) => void;
  /** Clears the scrollback of the focused pane. */
  clearActiveTerminal: () => void;
  /** Types a snippet into the focused pane and presses Enter. */
  runSnippet: (command: string) => void;
  /** Kills every session and closes every tab (the "close all" command). */
  closeAllSessions: () => void;
  /** Steps the terminal font size, clamped to a legible range. */
  nudgeFont: (delta: number) => void;
  resetFont: () => void;
  notify: (level: Notice["level"], text: string) => void;
  dismiss: (id: number) => void;

  sessionOf: (paneId: PaneID) => SessionInfo | undefined;
}

export const useApp = create<AppState>((set, get) => {
  const send = (msg: Parameters<Transport["send"]>[0]) => transport?.send(msg);

  const patchPane = (tabId: string, paneId: PaneID, patch: Partial<Pane>) => {
    set((state) => ({
      tabs: state.tabs.map((tab) =>
        tab.id !== tabId
          ? tab
          : { ...tab, panes: { ...tab.panes, [paneId]: { ...tab.panes[paneId], ...patch } } },
      ),
    }));
  };

  const startSession = (tab: Tab, paneId: PaneID, reqId = nextId("r")) => {
    const pane = tab.panes[paneId];
    const size = lastSize.get(paneId) ?? { cols: 80, rows: 24 };
    pendingCreates.set(reqId, { tabId: tab.id, paneId });
    send({
      type: "create",
      reqId,
      profile: pane.profile,
      cols: size.cols,
      rows: size.rows,
      cwd: pane.cwd || undefined,
    });
  };

  const refreshProfiles = () => {
    void api
      .profiles()
      .then((profiles) => set({ profiles }))
      .catch(() => undefined);
  };

  const onMessage = (msg: ServerMessage) => {
    switch (msg.type) {
      case "ready":
        set({ version: msg.version });
        break;

      case "config": {
        const first = get().config === null;
        set({ config: msg.config, profiles: msg.config.profiles ?? [] });
        // "Reset font size" returns to the size the config file specified at
        // startup, so font nudges stay a session-local convenience.
        if (first) set({ baseFontSize: msg.config.appearance.fontSize });
        // A repaired config is otherwise indistinguishable from a good one.
        // Only differences are announced, so a hot reload that changes nothing
        // does not repeat the same warning forever.
        const diags = (msg.config.diagnostics ?? []).join(" | ");
        if (diags && diags !== seenDiagnostics) {
          seenDiagnostics = diags;
          get().notify("warn", `config: ${diags}`);
        } else if (!diags) {
          seenDiagnostics = "";
        }
        refreshProfiles();
        break;
      }

      case "history": {
        // The backend's own list is authoritative; replacing rather than
        // merging keeps a clear from reappearing on the next reply. It is also
        // what arrives right after a pane spawns, carrying the seeded history,
        // so a fresh tab has its recall list without waiting to be opened.
        set((state) => ({ history: { ...state.history, [msg.id]: msg.history } }));
        break;
      }

      case "historyEntry": {
        // One command completed in a pane. The panel's own fetch comes back
        // newest first, so the push has to prepend to agree with it; otherwise
        // the list would flip order the next time the panel opened. The payload
        // is a single string rather than the whole list, so this stays cheap.
        const cmd = msg.history[0];
        if (!cmd) break;
        const list = get().history[msg.id] ?? [];
        if (list[0] === cmd) break;
        set((state) => ({ history: { ...state.history, [msg.id]: [cmd, ...list] } }));
        break;
      }

      case "sessions": {
        const sessions: Record<string, SessionInfo> = {};
        for (const s of msg.sessions) sessions[s.id] = s;
        set({ sessions });
        break;
      }

      case "created": {
        const pending = pendingCreates.get(msg.reqId);
        pendingCreates.delete(msg.reqId);
        if (!pending) return;
        const size = lastSize.get(pending.paneId) ?? { cols: 80, rows: 24 };
        patchPane(pending.tabId, pending.paneId, {
          sessionId: msg.id,
          status: "running",
          title: msg.name,
          cwd: msg.cwd,
          error: undefined,
        });
        // The tab strip shows the shell's own title once the first shell in the
        // tab reports one, matching how a browser tab takes the page title —
        // unless the user renamed the tab, which then wins.
        set((state) => ({
          tabs: state.tabs.map((tab) =>
            tab.id === pending.tabId && !tab.renamed && L.firstPane(tab.root) === pending.paneId && msg.name
              ? { ...tab, title: msg.name }
              : tab,
          ),
        }));
        pendingSizes.set(msg.id, size);
        send({ type: "attach", id: msg.id, cols: size.cols, rows: size.rows });
        break;
      }

      case "attached": {
        const found = findBySession(get().tabs, msg.id);
        if (!found) return;
        patchPane(found.tabId, found.paneId, {
          title: msg.info.name,
          cwd: msg.info.cwd,
          status: msg.info.status === "exited" ? "exited" : "running",
        });
        if (msg.replay) writeToTerminal(found.paneId, decodeBase64(msg.replay));
        break;
      }

      case "output": {
        const found = findBySession(get().tabs, msg.id);
        if (found) writeToTerminal(found.paneId, decodeBase64(msg.data));
        break;
      }

      case "exit": {
        const found = findBySession(get().tabs, msg.id);
        if (found) patchPane(found.tabId, found.paneId, { status: "exited", exitCode: msg.code });
        break;
      }

      case "closed": {
        const found = findBySession(get().tabs, msg.id);
        if (found) patchPane(found.tabId, found.paneId, { sessionId: undefined, status: "exited" });
        break;
      }

      case "error": {
        get().notify("error", msg.message || msg.code);
        if (!msg.reqId) return;
        const pending = pendingCreates.get(msg.reqId);
        pendingCreates.delete(msg.reqId);
        if (pending) patchPane(pending.tabId, pending.paneId, { status: "exited", error: msg.message });
        break;
      }

      default:
        break;
    }
  };

  return {
    conn: "connecting",
    version: "",
    config: null,
    configPath: "",
    profiles: [],
    fonts: [],
    tabs: [],
    activeTabId: "",
    activePane: "",
    sessions: {},
    overlay: "none",
    notices: [],
    searchOpen: false,
    maximizePane: "",
    baseFontSize: 14,
    history: {},

    connect: (t) => {
      transport = t;
      t.onMessage(onMessage);
      t.onState((conn) => {
        set({ conn });
        if (conn !== "open") return;
        // A reconnected window must re-learn the config/session list and
        // re-attach its panes, because the backend kept the shells alive.
        send({ type: "hello", version: 1, client: "webview" });
        for (const tab of get().tabs) {
          for (const pane of Object.values(tab.panes)) {
            if (!pane.sessionId) continue;
            const size = lastSize.get(pane.id) ?? { cols: 80, rows: 24 };
            send({ type: "attach", id: pane.sessionId, cols: size.cols, rows: size.rows });
          }
        }
      });
      void api
        .fonts()
        .then((fonts) => set({ fonts }))
        .catch(() => undefined);
      void api
        .health()
        .then((h) => set({ configPath: h.configPath }))
        .catch(() => undefined);
    },

    newTab: (profileId, cwd) => {
      const { config, profiles } = get();
      const tab = newTabState(
        pickProfile(config, profiles, profileId),
        cwd ?? config?.behavior.defaultCwd ?? "",
        "Terminal",
      );
      const paneId = L.firstPane(tab.root);
      set((s) => ({ tabs: [...s.tabs, tab], activeTabId: tab.id, activePane: paneId }));
      startSession(tab, paneId);
    },

    closeTab: (tabId) => {
      const { tabs, activeTabId } = get();
      const tab = tabs.find((t) => t.id === tabId);
      if (!tab) return;
      for (const pane of Object.values(tab.panes)) {
        if (pane.sessionId) send({ type: "close", id: pane.sessionId });
        const size = lastSize.get(pane.id);
        if (size) lastSize.delete(pane.id);
      }
      const remaining = tabs.filter((t) => t.id !== tabId);
      if (remaining.length === 0) {
        // Startup is allowed to be empty, so closing the last tab lands on the
        // same empty stage rather than spawning a shell the user did not ask
        // for.
        set({ tabs: [], activeTabId: "", activePane: "", maximizePane: "" });
        return;
      }
      const nextActiveId =
        activeTabId === tabId ? remaining[remaining.length - 1].id : activeTabId;
      const nextActive = remaining.find((t) => t.id === nextActiveId) ?? remaining[0];
      set({ tabs: remaining, activeTabId: nextActive.id, activePane: L.firstPane(nextActive.root) });
    },

    selectTab: (tabId) => {
      const tab = get().tabs.find((t) => t.id === tabId);
      if (!tab) return;
      set({ activeTabId: tabId, activePane: L.firstPane(tab.root), maximizePane: "" });
    },

    reorderTab: (tabId, beforeId) => {
      if (tabId === beforeId) return;
      const tabs = [...get().tabs];
      const from = tabs.findIndex((t) => t.id === tabId);
      const to = tabs.findIndex((t) => t.id === beforeId);
      if (from < 0 || to < 0) return;
      const [moved] = tabs.splice(from, 1);
      tabs.splice(to, 0, moved);
      set({ tabs });
    },

    renameTab: (tabId, title) => {
      const trimmed = title.trim();
      set((state) => ({
        tabs: state.tabs.map((tab) =>
          tab.id === tabId
            ? // Clearing the name hands control back to the shell's own title.
              trimmed
              ? { ...tab, title: trimmed, renamed: true }
              : { ...tab, title: tab.panes[L.firstPane(tab.root)]?.title ?? "Terminal", renamed: false }
            : tab,
        ),
      }));
    },

    /**
     * Closes the tab but leaves its shells running: the sessions stop being
     * addressed by any pane, so they land in the sidebar's background list and
     * can be adopted into a new tab later. This is the spec's "close a tab
     * without killing its session".
     */
    detachTab: (tabId) => {
      const { tabs, activeTabId } = get();
      const tab = tabs.find((t) => t.id === tabId);
      if (!tab || !L.hasRunningPane(tab)) {
        get().closeTab(tabId);
        return;
      }
      for (const pane of Object.values(tab.panes)) {
        if (pane.sessionId) send({ type: "detach", id: pane.sessionId });
        lastSize.delete(pane.id);
      }
      const remaining = tabs.filter((t) => t.id !== tabId);
      if (remaining.length === 0) {
        set({ tabs: [], activeTabId: "", activePane: "", maximizePane: "" });
        return;
      }
      const nextActive = remaining.find((t) => t.id === activeTabId) ?? remaining[remaining.length - 1];
      set({
        tabs: remaining,
        activeTabId: nextActive.id,
        activePane: L.firstPane(nextActive.root),
        maximizePane: "",
      });
    },

    adoptSession: (sessionId) => {
      const live = get().sessions[sessionId];
      if (!live) return;
      const { config, profiles } = get();
      const tab = newTabState(
        profiles.some((p) => p.id === live.profile) ? live.profile : pickProfile(config, profiles),
        live.cwd,
        live.name || "Terminal",
      );
      const paneId = L.firstPane(tab.root);
      const size = lastSize.get(paneId) ?? { cols: 80, rows: 24 };
      // The session already exists: the pane adopts its id and attaches rather
      // than asking the backend to spawn a second shell.
      set((s) => ({
        tabs: [
          ...s.tabs,
          { ...tab, panes: { ...tab.panes, [paneId]: { ...tab.panes[paneId], sessionId, status: "running" } } },
        ],
        activeTabId: tab.id,
        activePane: paneId,
        maximizePane: "",
      }));
      pendingSizes.set(sessionId, size);
      send({ type: "attach", id: sessionId, cols: size.cols, rows: size.rows });
    },

    killSession: (sessionId) => {
      send({ type: "close", id: sessionId });
    },

    toggleMaximizePane: () => {
      const { maximizePane, activePane } = get();
      set({ maximizePane: maximizePane ? "" : activePane });
    },

    cycleTab: (delta) => {
      const { tabs, activeTabId } = get();
      if (tabs.length < 2) return;
      const idx = tabs.findIndex((t) => t.id === activeTabId);
      const next = tabs[(idx + delta + tabs.length) % tabs.length];
      get().selectTab(next.id);
    },

    splitPane: (direction) => {
      const { tabs, activeTabId, activePane, config, profiles } = get();
      const tab = tabs.find((t) => t.id === activeTabId);
      if (!tab) return;
      const target = activePane || L.firstPane(tab.root);
      const source = tab.panes[target];
      const paneId = nextId("p");
      const pane: Pane = {
        id: paneId,
        profile: source?.profile ?? pickProfile(config, profiles),
        cwd: source?.cwd ?? config?.behavior.defaultCwd ?? "",
        title: source?.title ?? "Terminal",
        status: "starting",
      };
      // `row` places the new pane to the right, `column` below.
      const root = L.splitNode(tab.root, target, direction, paneId, false, nextId("sp"));
      const nextTab: Tab = { ...tab, root, panes: { ...tab.panes, [paneId]: pane } };
      set((s) => ({
        tabs: s.tabs.map((t) => (t.id === tab.id ? nextTab : t)),
        activePane: paneId,
      }));
      startSession(nextTab, paneId);
    },

    closePane: (paneId) => {
      const { tabs, activeTabId, activePane } = get();
      const tab = tabs.find((t) => t.id === activeTabId);
      if (!tab) return;
      const target = paneId ?? activePane;
      const pane = tab.panes[target];
      if (!pane) return;
      if (pane.sessionId) send({ type: "close", id: pane.sessionId });
      lastSize.delete(target);
      const root = L.removeNode(tab.root, target);
      if (!root) {
        get().closeTab(tab.id);
        return;
      }
      const panes = { ...tab.panes };
      delete panes[target];
      set((s) => ({
        tabs: s.tabs.map((t) => (t.id === tab.id ? { ...t, root, panes } : t)),
        activePane: target === activePane ? L.firstPane(root) : activePane,
      }));
    },

    restartPane: (paneId) => {
      const { tabs, activeTabId, activePane } = get();
      const tab = tabs.find((t) => t.id === activeTabId);
      const target = paneId ?? activePane;
      const pane = tab?.panes[target];
      if (!tab || !pane) return;
      const reqId = nextId("r");
      const size = lastSize.get(target) ?? { cols: 80, rows: 24 };
      pendingCreates.set(reqId, { tabId: tab.id, paneId: target });
      patchPane(tab.id, target, { status: "starting", error: undefined });
      if (pane.sessionId) {
        send({ type: "restart", reqId, id: pane.sessionId, cols: size.cols, rows: size.rows });
      } else {
        startSession(tab, target, reqId);
      }
    },

    duplicatePane: (paneId) => {
      const { tabs, activeTabId, activePane } = get();
      const tab = tabs.find((t) => t.id === activeTabId);
      const source = tab?.panes[paneId ?? activePane];
      if (!source) return;
      // A duplicate opens as a new tab rather than a split: the spec's
      // "duplicate" means "same shell, same cwd, independent".
      get().newTab(source.profile, source.cwd);
    },

    focusPane: (paneId) => set({ activePane: paneId }),

    focusDirection: (dir) => {
      const { tabs, activeTabId, activePane } = get();
      const tab = tabs.find((t) => t.id === activeTabId);
      if (!tab || !activePane) return;
      const next = L.paneInDirection(tab.root, activePane, dir);
      if (next !== activePane) set({ activePane: next });
    },

    cyclePane: (delta) => {
      const { tabs, activeTabId, activePane } = get();
      const tab = tabs.find((t) => t.id === activeTabId);
      if (!tab || !activePane) return;
      set({ activePane: L.siblingPane(tab.root, activePane, delta) });
    },

    setRatio: (splitId, ratio) => {
      const { tabs, activeTabId } = get();
      const tab = tabs.find((t) => t.id === activeTabId);
      if (!tab) return;
      const root = L.setRatio(tab.root, splitId, ratio);
      set((s) => ({ tabs: s.tabs.map((t) => (t.id === tab.id ? { ...t, root } : t)) }));
    },

    updateConfig: (patch, persist = true) => {
      const cfg = get().config;
      if (!cfg) return;
      const next = JSON.parse(JSON.stringify(cfg)) as Config;
      patch(next);
      set({ config: next });
      if (persist) {
        void api
          .saveConfig(next)
          .then((saved) => set({ config: saved }))
          .catch((err: unknown) => get().notify("error", String(err)));
      }
    },

    updateSidebar: (patch) => {
      get().updateConfig((cfg) => {
        cfg.sidebar = { ...cfg.sidebar, ...patch };
      });
    },

    setOverlay: (overlay) => set({ overlay }),

    setSearchOpen: (open) => set({ searchOpen: open }),

    terminalHistory: (paneId) => {
      const found = paneById(get().tabs, paneId);
      if (!found?.pane.sessionId) return [];
      return get().history[found.pane.sessionId] ?? [];
    },

    requestHistory: (paneId, query) => {
      const found = paneById(get().tabs, paneId);
      if (!found?.pane.sessionId) return;
      send({ type: "history", id: found.pane.sessionId, query, limit: 200 });
    },

    clearHistory: (paneId) => {
      const found = paneById(get().tabs, paneId);
      if (!found?.pane.sessionId) return;
      send({ type: "historyClear", id: found.pane.sessionId });
    },

    /**
     * Inserts a recalled command at the prompt without running it, so the user
     * can edit it first. Typing it as input (rather than writing to xterm) is
     * what makes the shell's own line editor track the insertion.
     */
    recallCommand: (command) => sendInput(get().activePane, command),

    clearActiveTerminal: () => terminalFor(get().activePane)?.term.clear(),

    runSnippet: (command) => sendInput(get().activePane, command + "\r"),

    closeAllSessions: () => {
      for (const tab of get().tabs) {
        for (const pane of Object.values(tab.panes)) {
          if (pane.sessionId) send({ type: "close", id: pane.sessionId });
        }
      }
      // The point of this action is to end up with nothing running, so it must
      // not immediately spawn a replacement shell.
      set({ tabs: [], activeTabId: "", activePane: "", maximizePane: "" });
    },

    nudgeFont: (delta) => {
      const size = get().config?.appearance.fontSize;
      if (size === undefined) return;
      const next = Math.min(48, Math.max(6, size + delta));
      if (next === size) return;
      get().updateConfig((cfg) => {
        cfg.appearance.fontSize = next;
      });
    },

    resetFont: () => {
      const base = get().baseFontSize;
      get().updateConfig((cfg) => {
        cfg.appearance.fontSize = base;
      });
    },

    notify: (level, text) => {
      const id = ++counter;
      set((s) => ({ notices: [...s.notices, { id, level, text }] }));
      window.setTimeout(() => get().dismiss(id), level === "error" ? 8000 : 4000);
    },

    dismiss: (id) => set((s) => ({ notices: s.notices.filter((n) => n.id !== id) })),

    sessionOf: (paneId) => {
      const found = paneById(get().tabs, paneId);
      if (!found?.pane.sessionId) return undefined;
      return get().sessions[found.pane.sessionId];
    },
  };
});

/**
 * Reports a pane's measured size. Sizes are coalesced per animation frame:
 * a splitter drag produces dozens of resize callbacks per second and the PTY
 * only needs the final geometry of each frame.
 */
const resizeQueue = new Map<PaneID, PendingSize>();
let resizeFrame: number | null = null;

/** Sends one frame to the backend; the transport lives outside the store so
 *  terminal input never triggers a React render. */
export function sendMessage(msg: ClientMessage): void {
  transport?.send(msg);
}

export function reportResize(paneId: PaneID, cols: number, rows: number): void {
  lastSize.set(paneId, { cols, rows });
  const found = paneById(useApp.getState().tabs, paneId);
  if (found?.pane.sessionId) resizeQueue.set(paneId, { cols, rows });
  if (resizeFrame !== null) return;
  resizeFrame = window.requestAnimationFrame(() => {
    resizeFrame = null;
    const tabs = useApp.getState().tabs;
    for (const [id, size] of resizeQueue) {
      const pane = paneById(tabs, id);
      if (pane?.pane.sessionId) {
        sendMessage({ type: "resize", id: pane.pane.sessionId, cols: size.cols, rows: size.rows });
      }
    }
    resizeQueue.clear();
  });
}

/** Sends a raw string to a pane's shell, used by snippets and paste. */
export function sendInput(paneId: PaneID, text: string): void {
  const found = paneById(useApp.getState().tabs, paneId);
  if (!found?.pane.sessionId) return;
  sendMessage({
    type: "input",
    id: found.pane.sessionId,
    data: encodeBase64(new TextEncoder().encode(text)),
  });
}

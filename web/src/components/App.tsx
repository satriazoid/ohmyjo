import { useEffect, useState } from "react";
import { actionFor } from "../keys";
import * as L from "../layout";
import { useActiveTab } from "../selectors";
import { useApp } from "../store";
import { refitAll, terminalFor } from "../terminals";
import { applyTheme } from "../theme";
import { PaneTree } from "./Panes";
import { Sidebar } from "./Sidebar";
import { TabBar } from "./TabBar";
import { Notices } from "./Notices";
import { SettingsPanel } from "./Settings";
import { CommandPalette } from "./CommandPalette";
import { HistoryPanel } from "./HistoryPanel";
import { SearchBar } from "./Search";

/** Root layout: tab strip, optional side panel and the active tab's pane tree. */
export function App() {
  const config = useApp((s) => s.config);
  const conn = useApp((s) => s.conn);
  const overlay = useApp((s) => s.overlay);
  const activeTab = useActiveTab();
  const tabs = useApp((s) => s.tabs);
  const activeTabId = useApp((s) => s.activeTabId);
  const activePane = useApp((s) => s.activePane);
  const maximizePane = useApp((s) => s.maximizePane);
  const toggleMaximize = useApp((s) => s.toggleMaximizePane);
  const sidebar = config?.sidebar;

  // A maximized pane still has to stop being maximized when its pane is gone
  // (closed, or its tab switched); otherwise the stage would be empty.
  useEffect(() => {
    if (!maximizePane) return;
    if (!activeTab || !activeTab.panes[maximizePane]) toggleMaximize();
  }, [maximizePane, activeTab, toggleMaximize]);

  // Auto-hide collapses the sidebar to a strip that expands on hover; the pane
  // geometry only changes on the real toggle, so a hover does not reflow the
  // terminals.
  const [peek, setPeek] = useState(false);
  const autoHide = sidebar?.autoHide ?? false;
  const showSidebar = (sidebar?.visible ?? false) && (!autoHide || peek);

  // Theme changes arrive from the backend (config hot reload) and from local
  // edits; both paths land here, so this is the single place CSS vars are set.
  useEffect(() => {
    applyTheme(config?.themes?.[config.appearance.theme]);
  }, [config]);

  // WebView2 keeps Chromium's own shortcuts. A couple of them have no meaning in
  // a desktop terminal and are actively harmful here: F5 would reload the UI and
  // drop every visible pane's scrollback, and F6/F7 move focus and turn on caret
  // browsing inside the chrome.
  //
  // The Ctrl combinations are deliberately left alone. Ctrl+R, Ctrl+S, Ctrl+O and
  // Ctrl+U are all shell control characters (reverse search, flow control,
  // discard, kill-line), and Ctrl+P / Ctrl+Shift+R are the command palette and
  // restart-pane bindings, so claiming them would break the terminal to fix a
  // problem the user never had.
  useEffect(() => {
    const blocked = new Set(["f5", "f6", "f7"]);
    const onKeyDown = (ev: KeyboardEvent) => {
      if (!blocked.has(ev.key.toLowerCase())) return;
      ev.preventDefault();
      ev.stopPropagation();
    };
    const onWheel = (ev: WheelEvent) => {
      // Browser pinches and Ctrl+wheel zoom the whole app; the terminal has its
      // own font-size bindings instead.
      if (ev.ctrlKey) ev.preventDefault();
    };
    window.addEventListener("keydown", onKeyDown, true);
    window.addEventListener("wheel", onWheel, { passive: false, capture: true });
    return () => {
      window.removeEventListener("keydown", onKeyDown, true);
      window.removeEventListener("wheel", onWheel, { capture: true });
    };
  }, []);

  // The sidebar width changes the pane geometry without a window resize.
  useEffect(() => {
    const id = window.requestAnimationFrame(() => refitAll());
    return () => window.cancelAnimationFrame(id);
  }, [sidebar?.visible, sidebar?.width, sidebar?.side]);

  // The tab that was just revealed was hidden, so its terminal kept the size it
  // had when it went away and xterm had no reason to re-measure. One refit per
  // switch re-establishes the true geometry and reports it to the backend.
  useEffect(() => {
    const id = window.requestAnimationFrame(() => refitAll());
    return () => window.cancelAnimationFrame(id);
  }, [activeTabId]);

  useEffect(() => {
    if (!config) return;
    const onKeyDown = (ev: KeyboardEvent) => {
      // This listener is capture-phase, so it runs before the focused input's
      // own handler; without this guard a chord typed into the rename box or a
      // settings field would fire the bound action instead of reaching it.
      // xterm focuses a hidden textarea to receive keystrokes, so that one
      // target must keep flowing to the shell.
      const target = ev.target as HTMLElement | null;
      if (target?.classList.contains("xterm-helper-textarea")) {
        // fall through to the keybinding lookup
      } else {
        if (target?.isContentEditable) return;
        const tag = target?.tagName;
        if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return;
      }
      const action = actionFor(ev, config.keybindings);
      if (!action) return;
      const state = useApp.getState();
      switch (action) {
        case "newTab":
          state.newTab();
          break;
        case "closeTab":
          if (state.activeTabId) state.closeTab(state.activeTabId);
          break;
        case "detachTab":
          if (state.activeTabId) state.detachTab(state.activeTabId);
          break;
        case "maximizePane":
          state.toggleMaximizePane();
          break;
        case "nextTab":
          state.cycleTab(1);
          break;
        case "prevTab":
          state.cycleTab(-1);
          break;
        case "splitRight":
          state.splitPane("row");
          break;
        case "splitDown":
          state.splitPane("column");
          break;
        case "closePane":
          state.closePane();
          break;
        case "toggleSidebar":
          state.updateSidebar({ visible: !state.config?.sidebar.visible });
          break;
        case "settings":
          state.setOverlay(state.overlay === "settings" ? "none" : "settings");
          break;
        case "commandPalette":
          state.setOverlay(state.overlay === "palette" ? "none" : "palette");
          break;
        case "searchTerminal":
          state.setSearchOpen(true);
          break;
        case "history":
          state.setOverlay(state.overlay === "history" ? "none" : "history");
          break;
        case "fontIncrease":
          state.nudgeFont(1);
          break;
        case "fontDecrease":
          state.nudgeFont(-1);
          break;
        case "fontReset":
          state.resetFont();
          break;
        case "focusNextPane":
          state.cyclePane(1);
          break;
        case "focusPrevPane":
          state.cyclePane(-1);
          break;
        case "clearTerminal":
          terminalFor(state.activePane)?.term.clear();
          break;
        case "restartPane":
          state.restartPane();
          break;
        case "duplicatePane":
          state.duplicatePane();
          break;
        default:
          return;
      }
      // Only claim the chord once it is known to be bound, so unbound keys
      // still reach the shell.
      ev.preventDefault();
      ev.stopPropagation();
    };
    window.addEventListener("keydown", onKeyDown, true);
    return () => window.removeEventListener("keydown", onKeyDown, true);
  }, [config]);

  return (
    <div className={`app${sidebar?.side === "right" ? " app-sidebar-right" : ""}`}>
      <TabBar />
      <div
        className={`workspace${autoHide ? " workspace-autohide" : ""}${peek && autoHide ? " workspace-peek" : ""}`}
      >
        {/* Auto-hide reveals the sidebar from an edge strip, not from the whole
            workspace: a terminal fills the workspace, so hovering anywhere in it
            would otherwise pop the panel open mid-typing. While revealed the
            sidebar floats, so leaving it must retract it again — tracked on the
            sidebar itself because the strip is gone by then. */}
        {showSidebar && <Sidebar onMouseLeave={() => autoHide && setPeek(false)} />}
        {autoHide && !peek && sidebar?.visible ? (
          <div
            className={`sidebar-edge sidebar-edge-${sidebar.side}`}
            role="presentation"
            onMouseEnter={() => setPeek(true)}
          />
        ) : null}
        <main className="stage">
          {tabs.length > 0 ? (
            // Every tab is mounted at once. Only the active one is laid out;
            // the rest are hidden in CSS. Unmounting the inactive tabs is what
            // used to dispose their terminals on every switch — which cleared
            // the screen and threw away the scrollback, while the shells
            // themselves kept running and replayed into a terminal that had
            // only just been created.
            tabs.map((tab) => (
              <div
                key={tab.id}
                className={`tab-panes${tab.id === activeTabId ? "" : " tab-panes-hidden"}`}
              >
                <PaneTree
                  node={tab.root}
                  activePane={activePane}
                  paneCount={L.collectPanes(tab.root).length}
                  maximize={tab.id === activeTabId ? maximizePane : ""}
                />
              </div>
            ))
          ) : (
            // Startup is intentionally empty: no shell is spawned until the
            // user asks for one, so this stage has to offer the way in.
            <div className="empty-stage">
              <p>{conn === "open" ? "No terminal open" : "Connecting to the backend…"}</p>
              <button type="button" className="empty-stage-new" onClick={() => useApp.getState().newTab()}>
                New terminal
              </button>
              <p className="hint">
                <kbd>Ctrl</kbd>+<kbd>Shift</kbd>+<kbd>T</kbd> opens a new tab at any time.
              </p>
            </div>
          )}
        </main>
      </div>
      {overlay === "settings" && <SettingsPanel />}
      {overlay === "palette" && <CommandPalette />}
      {overlay === "history" && <HistoryPanel />}
      <SearchBar />
      <Notices />
      {conn === "closed" && <div className="conn-banner">Backend connection lost — retrying…</div>}
    </div>
  );
}

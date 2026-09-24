import { useEffect, useState } from "react";
import { hasRunningPane, type Tab } from "../layout";
import { isDesktopShell, shell } from "../shell";
import { useApp } from "../store";

/**
 * Browser-style tab strip: new-tab menu, middle-click close, double-click
 * rename, drag reorder and a dot marking tabs whose shell is still alive.
 */
export function TabBar() {
  const tabs = useApp((s) => s.tabs);
  const activeTabId = useApp((s) => s.activeTabId);
  const selectTab = useApp((s) => s.selectTab);
  const closeTab = useApp((s) => s.closeTab);
  const detachTab = useApp((s) => s.detachTab);
  const reorderTab = useApp((s) => s.reorderTab);
  const renameTab = useApp((s) => s.renameTab);
  const newTab = useApp((s) => s.newTab);
  const profiles = useApp((s) => s.profiles);
  const sidebarVisible = useApp((s) => s.config?.sidebar.visible ?? false);
  const [menuOpen, setMenuOpen] = useState(false);
  // The dropdown cannot be an absolutely positioned child of the tab strip:
  // `.tabs` scrolls horizontally, and an overflow container clips absolutely
  // positioned descendants, which made the menu invisible and placed the
  // backdrop under the pointer. Anchor it to the viewport instead, from the
  // button's own rect captured at open time.
  const [menuPos, setMenuPos] = useState<{ x: number; y: number } | null>(null);
  const [renaming, setRenaming] = useState<string | null>(null);
  const [dragTab, setDragTab] = useState<string | null>(null);
  // The native caption bar is gone, so the maximize/restore icon has to be
  // tracked here. It is re-read on every window resize because the user can
  // also maximize with Win+Up or by double-clicking the drag region.
  const desktop = isDesktopShell();
  const [maximized, setMaximized] = useState(false);

  useEffect(() => {
    if (!desktop) return;
    const sync = () => void shell.isMaximized().then(setMaximized);
    sync();
    window.addEventListener("resize", sync);
    return () => window.removeEventListener("resize", sync);
  }, [desktop]);

  const toggleMaximize = () => {
    void shell.toggleMaximize().then(setMaximized);
  };

  /**
   * The tab strip doubles as the window's drag handle. Clicks that belong to a
   * control must stay in the DOM, so anything interactive is excluded.
   */
  const startWindowDrag = (ev: React.MouseEvent) => {
    if (!desktop || ev.button !== 0) return;
    const target = ev.target as HTMLElement;
    if (target.closest("button, input, .tab, .window-controls")) return;
    ev.preventDefault();
    void shell.drag();
  };

  return (
    <div className="tabbar" onMouseDown={startWindowDrag} onDoubleClick={() => desktop && toggleMaximize()}>
      <div className="tabs">
        {tabs.map((tab) => (
          <div
            key={tab.id}
            className={`tab${tab.id === activeTabId ? " tab-active" : ""}`}
            draggable={renaming !== tab.id}
            onDragStart={() => setDragTab(tab.id)}
            onDragEnd={() => setDragTab(null)}
            onDragOver={(ev) => {
              if (dragTab && dragTab !== tab.id) ev.preventDefault();
            }}
            onDrop={(ev) => {
              ev.preventDefault();
              if (dragTab && dragTab !== tab.id) reorderTab(dragTab, tab.id);
              setDragTab(null);
            }}
            onClick={() => selectTab(tab.id)}
            onAuxClick={(ev) => {
              if (ev.button === 1) closeTab(tab.id);
            }}
            onDoubleClick={(ev) => {
              // The tab strip doubles as the window drag handle, so a rename
              // must not also reach the maximize toggle behind it.
              ev.stopPropagation();
              setRenaming(tab.id);
            }}
          >
            {renaming === tab.id ? (
              <input
                className="tab-rename"
                autoFocus
                defaultValue={tab.title}
                // Pre-selecting lets the new name be typed straight over the old
                // one, the way a file rename behaves.
                onFocus={(ev) => ev.currentTarget.select()}
                onBlur={(ev) => {
                  renameTab(tab.id, ev.target.value);
                  setRenaming(null);
                }}
                onKeyDown={(ev) => {
                  if (ev.key === "Enter") {
                    renameTab(tab.id, ev.currentTarget.value);
                    setRenaming(null);
                  } else if (ev.key === "Escape") {
                    setRenaming(null);
                  }
                  // Keep the rename keystrokes out of the pane/terminal handlers.
                  ev.stopPropagation();
                }}
              />
            ) : (
              <>
                <span className={`tab-dot${hasRunningPane(tab) ? " tab-dot-live" : ""}`} />
                <span className="tab-label" title={tabTooltip(tab)}>
                  {tab.title}
                </span>
                <span
                  className="tab-close"
                  role="button"
                  tabIndex={-1}
                  title="Close tab (Shift-click keeps the shells running)"
                  onClick={(ev) => {
                    ev.stopPropagation();
                    // Shift means "close the tab, keep the shells": the sessions
                    // move to the sidebar's background list.
                    if (ev.shiftKey) detachTab(tab.id);
                    else closeTab(tab.id);
                  }}
                >
                  ✕
                </span>
              </>
            )}
          </div>
        ))}
      </div>
      <div className="tab-new-wrap">
        <button
          type="button"
          className="tab-new"
          title="New tab"
          onClick={(ev) => {
            if (profiles.length <= 1) {
              newTab();
              return;
            }
            const box = ev.currentTarget.getBoundingClientRect();
            // Clamp so a right-edge strip never pushes the menu off screen.
            setMenuPos({
              x: Math.max(4, Math.min(box.left, window.innerWidth - 216)),
              y: box.bottom + 2,
            });
            setMenuOpen((v) => !v);
          }}
        >
          +
        </button>
        {menuOpen && menuPos && (
          <>
            <div className="menu-backdrop" onClick={() => setMenuOpen(false)} />
            <div className="menu" style={{ left: menuPos.x, top: menuPos.y }}>
              {profiles.map((p) => (
                <button
                  type="button"
                  key={p.id}
                  disabled={p.available === false}
                  className="menu-item"
                  onClick={() => {
                    setMenuOpen(false);
                    newTab(p.id);
                  }}
                >
                  <span className="swatch" style={{ background: p.color ?? "var(--ui-accent)" }} />
                  {p.name}
                  {p.available === false && <span className="menu-note">not installed</span>}
                </button>
              ))}
            </div>
          </>
        )}
      </div>
      <div className="tabbar-actions">
        <button
          type="button"
          // The toggle reflects whether the panel is currently enabled, so the
          // active state has to come from the config, not from local UI state.
          className={sidebarVisible ? "tabbar-action-active" : ""}
          aria-pressed={sidebarVisible}
          title={sidebarVisible ? "Hide sidebar" : "Show sidebar"}
          onClick={() => useApp.getState().updateSidebar({ visible: !sidebarVisible })}
        >
          ▤
        </button>
        <button type="button" title="Settings" onClick={() => useApp.getState().setOverlay("settings")}>
          ⚙
        </button>
      </div>
      {desktop && (
        <div className="window-controls">
          <button type="button" className="window-control" title="Minimize" onClick={() => void shell.minimize()}>
            <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
              <path d="M0 5h10" stroke="currentColor" strokeWidth="1" />
            </svg>
          </button>
          <button
            type="button"
            className="window-control"
            title={maximized ? "Restore" : "Maximize"}
            onClick={toggleMaximize}
          >
            <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
              {maximized ? (
                <>
                  <path d="M2.5 2.5h5v5h-5z" fill="none" stroke="currentColor" strokeWidth="1" />
                  <path d="M0.5 7.5v-7h7" fill="none" stroke="currentColor" strokeWidth="1" />
                </>
              ) : (
                <rect x="0.5" y="0.5" width="9" height="9" fill="none" stroke="currentColor" strokeWidth="1" />
              )}
            </svg>
          </button>
          <button
            type="button"
            className="window-control window-control-close"
            title="Close"
            onClick={() => void shell.close()}
          >
            <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
              <path d="M0 0l10 10M10 0L0 10" stroke="currentColor" strokeWidth="1" />
            </svg>
          </button>
        </div>
      )}
    </div>
  );
}

/** Hover preview: shell, working directory and pane count. */
function tabTooltip(tab: Tab): string {
  const panes = Object.values(tab.panes);
  const cwd = panes[0]?.cwd ? `\n${panes[0].cwd}` : "";
  const extra = panes.length > 1 ? `\n${panes.length} panes` : "";
  return `${panes[0]?.title ?? tab.title}${cwd}${extra}`;
}
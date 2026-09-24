import { useEffect, useState } from "react";
import { api } from "../api";
import { useLocatedPane, useLocatedPanes } from "../selectors";
import { sendInput, useApp } from "../store";
import type { DirListing, GitInfo } from "../protocol";

/**
 * Side panel with the modes from the spec: sessions, profiles, snippets,
 * explorer (with git status) and settings.
 */
export function Sidebar({ onMouseLeave }: { onMouseLeave?: () => void }) {
  const cfg = useApp((s) => s.config?.sidebar);
  const updateSidebar = useApp((s) => s.updateSidebar);
  if (!cfg) return null;

  return (
    <aside
      className={`sidebar sidebar-${cfg.side}`}
      style={{ width: cfg.width }}
      // Auto-hide floats this panel over the stage, so leaving it has to put it
      // away again; the owner tracks that state.
      onMouseLeave={onMouseLeave}
    >
      <div className="sidebar-modes">
        {(MODES as readonly string[]).map((mode) => (
          <button
            type="button"
            key={mode}
            className={cfg.mode === mode ? "mode-active" : ""}
            title={mode}
            onClick={() => updateSidebar({ mode: mode as SidebarMode })}
          >
            {MODE_ICON[mode]}
          </button>
        ))}
        <span className="spacer" />
        <button
          type="button"
          title={`Move to the ${cfg.side === "left" ? "right" : "left"}`}
          onClick={() => updateSidebar({ side: cfg.side === "left" ? "right" : "left" })}
        >
          ⇄
        </button>
        <button type="button" title="Hide sidebar" onClick={() => updateSidebar({ visible: false })}>
          ✕
        </button>
      </div>
      <div className="sidebar-body">
        {cfg.mode === "sessions" && <SessionsPanel />}
        {cfg.mode === "profiles" && <ProfilesPanel />}
        {cfg.mode === "snippets" && <SnippetsPanel />}
        {cfg.mode === "explorer" && <ExplorerPanel />}
        {cfg.mode === "settings" && <SettingsShortcut />}
      </div>
    </aside>
  );
}

type SidebarMode = "sessions" | "profiles" | "snippets" | "explorer" | "settings";

const MODES: SidebarMode[] = ["sessions", "profiles", "snippets", "explorer", "settings"];

const MODE_ICON: Record<string, string> = {
  sessions: "▤",
  profiles: "◧",
  snippets: "⌘",
  explorer: "🗀",
  settings: "⚙",
};

function SessionsPanel() {
  const sessions = useApp((s) => s.sessions);
  const located = useLocatedPanes();
  const activePane = useApp((s) => s.activePane);
  const focusPane = useApp((s) => s.focusPane);
  const restartPane = useApp((s) => s.restartPane);
  const duplicatePane = useApp((s) => s.duplicatePane);
  const selectTab = useApp((s) => s.selectTab);
  const activeTabId = useApp((s) => s.activeTabId);
  const adoptSession = useApp((s) => s.adoptSession);
  const killSession = useApp((s) => s.killSession);

  // A live session owned by no pane is one whose tab was closed but whose shell
  // was kept running; it is reachable again only from here. Exited sessions are
  // already accounted for by their pane, so only running ones are listed.
  const attached = new Set(located.map((l) => l.pane.sessionId).filter(Boolean));
  const background = Object.values(sessions).filter((s) => s.status === "running" && !attached.has(s.id));

  return (
    <div className="panel">
      <h3>Sessions</h3>
      <ul className="list">
        {located.map(({ pane }) => {
          const live = pane.sessionId ? sessions[pane.sessionId] : undefined;
          return (
            <li key={pane.id} className={pane.id === activePane ? "row-active" : ""}>
              <button
                type="button"
                onClick={() => {
                  const tab = useApp.getState().tabs.find((t) => t.panes[pane.id]);
                  if (tab && tab.id !== activeTabId) selectTab(tab.id);
                  focusPane(pane.id);
                }}
              >
                <span className={`dot dot-${pane.status}`} />
                <span className="row-label">{pane.title}</span>
                <span className="row-meta">{live ? `PID ${live.pid}` : pane.status}</span>
              </button>
              <button type="button" className="row-action" title="Duplicate as a new tab" onClick={() => duplicatePane(pane.id)}>
                ⧉
              </button>
              {pane.status === "exited" && (
                <button type="button" className="row-action" title="Restart" onClick={() => restartPane(pane.id)}>
                  ⟳
                </button>
              )}
            </li>
          );
        })}
      </ul>
      {background.length > 0 && (
        <>
          <h3>Background</h3>
          <ul className="list">
            {background.map((s) => (
              <li key={s.id}>
                <button type="button" title="Open in a new tab" onClick={() => adoptSession(s.id)}>
                  <span className={`dot dot-${s.status}`} />
                  <span className="row-label">{s.name}</span>
                  <span className="row-meta">PID {s.pid}</span>
                </button>
                <button type="button" className="row-action" title="Kill" onClick={() => killSession(s.id)}>
                  ✕
                </button>
              </li>
            ))}
          </ul>
        </>
      )}
      <p className="hint">
        Closing a pane kills its shell. Use ⇧-close on a tab to keep its shells in the background.
      </p>
    </div>
  );
}

function ProfilesPanel() {
  const profiles = useApp((s) => s.profiles);
  const newTab = useApp((s) => s.newTab);
  return (
    <div className="panel">
      <h3>Profiles</h3>
      <ul className="list">
        {profiles.map((p) => (
          <li key={p.id}>
            <button type="button" disabled={p.available === false} onClick={() => newTab(p.id)}>
              <span className="swatch" style={{ background: p.color ?? "var(--ui-accent)" }} />
              <span className="row-label">{p.name}</span>
              <span className="row-meta">{p.available === false ? "not found" : "new tab"}</span>
            </button>
            <span className="row-path" title={p.shell}>
              {p.shell || "(default shell)"}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}

function SnippetsPanel() {
  const snippets = useApp((s) => s.config?.snippets);
  const activePane = useApp((s) => s.activePane);
  if (!snippets || snippets.length === 0) {
    return (
      <div className="panel">
        <h3>Snippets</h3>
        <p className="hint">Add snippets in Settings, or directly in config.json.</p>
      </div>
    );
  }
  return (
    <div className="panel">
      <h3>Snippets</h3>
      <ul className="list">
        {snippets.map((s) => (
          <li key={s.id}>
            <button type="button" title={s.command} onClick={() => sendInput(activePane, s.command + "\r")}>
              <span className="row-label">{s.name}</span>
            </button>
            <span className="row-path">{s.command}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

function ExplorerPanel() {
  const activePane = useApp((s) => s.activePane);
  const paneCwd = useLocatedPane(activePane)?.pane.cwd;
  const [listing, setListing] = useState<DirListing | null>(null);
  const [git, setGit] = useState<GitInfo | null>(null);
  const [path, setPath] = useState("");

  // Following the active pane keeps the explorer useful without extra clicks.
  useEffect(() => {
    if (paneCwd) setPath(paneCwd);
  }, [paneCwd]);

  useEffect(() => {
    if (!path) return;
    let cancelled = false;
    void api
      .dir(path)
      .then((res) => {
        if (cancelled) return;
        setListing(res);
        // The backend normalises the path (absolute, cleaned); adopt it so the
        // breadcrumb and parent navigation agree with the listing.
        if (res.path && res.path !== path) setPath(res.path);
      })
      .catch((err: unknown) => {
        if (!cancelled) setListing({ path, entries: [], error: String(err) });
      });
    void api
      .git(path)
      .then((res) => {
        if (!cancelled) setGit(res);
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [path]);

  const current = listing?.path ?? path;

  return (
    <div className="panel">
      <h3>Explorer</h3>
      {git?.repo && (
        <div className="git-row" title={git.root}>
          <span className="git-branch">⎇ {git.branch}</span>
          {git.dirty ? <span className="git-dirty">{git.dirty} changed</span> : <span className="git-clean">clean</span>}
        </div>
      )}
      <div className="path-row">
        <button
          type="button"
          disabled={!listing?.parent}
          title="Up one level"
          onClick={() => listing?.parent && setPath(listing.parent)}
        >
          ↑
        </button>
        <input value={current} onChange={(ev) => setPath(ev.target.value)} spellCheck={false} />
        <button
          type="button"
          title="Pick a folder"
          onClick={() =>
            void api
              .pick("folder", "Open folder")
              .then((res) => {
                if (res.path) setPath(res.path);
              })
              .catch((err: unknown) => useApp.getState().notify("error", String(err)))
          }
        >
          …
        </button>
      </div>
      <ul className="list explorer">
        {(listing?.entries ?? []).map((entry) => {
          const full = joinPath(current, entry.name);
          return (
            <li key={entry.name}>
              <button
                type="button"
                title={full}
                onClick={() => (entry.dir ? setPath(full) : void api.reveal(full))}
                onDoubleClick={() => {
                  if (!entry.dir) void api.open(full);
                }}
              >
                <span className="row-label">
                  {entry.dir ? "🗀" : "🗎"} {entry.name}
                </span>
                {!entry.dir && <span className="row-meta">{formatSize(entry.size)}</span>}
              </button>
            </li>
          );
        })}
      </ul>
      {listing?.error && <p className="hint error">{listing.error}</p>}
    </div>
  );
}

function SettingsShortcut() {
  const setOverlay = useApp((s) => s.setOverlay);
  const configPath = useApp((s) => s.configPath);
  return (
    <div className="panel">
      <h3>Settings</h3>
      <button type="button" className="wide" onClick={() => setOverlay("settings")}>
        Open all settings
      </button>
      <p className="hint">Config file (hot reloaded):</p>
      <p className="hint path">{configPath || "loading…"}</p>
    </div>
  );
}

function joinPath(base: string, name: string): string {
  if (!base) return name;
  const sep = base.includes("\\") ? "\\" : "/";
  return base.endsWith(sep) ? base + name : base + sep + name;
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

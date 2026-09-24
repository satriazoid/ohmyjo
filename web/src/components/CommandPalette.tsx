import { useMemo, useState } from "react";
import { formatChord } from "../keys";
import { useApp } from "../store";

interface Command {
  id: string;
  label: string;
  hint?: string;
  run: () => void;
}

/** Fuzzy-lite palette: substring match with the actions the spec lists. */
export function CommandPalette() {
  const setOverlay = useApp((s) => s.setOverlay);
  const profiles = useApp((s) => s.profiles);
  const snippets = useApp((s) => s.config?.snippets);
  const keybindings = useApp((s) => s.config?.keybindings);
  const [query, setQuery] = useState("");

  const commands = useMemo<Command[]>(() => {
    const state = () => useApp.getState();
    const base: Command[] = [
      { id: "newTab", label: "New tab", hint: keybindings?.newTab, run: () => state().newTab() },
      { id: "closeTab", label: "Close tab", hint: keybindings?.closeTab, run: () => state().closeTab(state().activeTabId) },
      { id: "splitRight", label: "Split pane right", hint: keybindings?.splitRight, run: () => state().splitPane("row") },
      { id: "splitDown", label: "Split pane down", hint: keybindings?.splitDown, run: () => state().splitPane("column") },
      { id: "closePane", label: "Close pane", hint: keybindings?.closePane, run: () => state().closePane() },
      { id: "restartPane", label: "Restart pane", hint: keybindings?.restartPane, run: () => state().restartPane() },
      { id: "duplicatePane", label: "Duplicate pane", hint: keybindings?.duplicatePane, run: () => state().duplicatePane() },
      { id: "toggleSidebar", label: "Toggle sidebar", hint: keybindings?.toggleSidebar, run: () => state().updateSidebar({ visible: !state().config?.sidebar.visible }) },
      { id: "search", label: "Search terminal", hint: keybindings?.searchTerminal, run: () => state().setSearchOpen(true) },
      { id: "history", label: "Command history", hint: keybindings?.history, run: () => state().setOverlay("history") },
      { id: "settings", label: "Open settings", hint: keybindings?.settings, run: () => state().setOverlay("settings") },
      { id: "clear", label: "Clear terminal", hint: keybindings?.clearTerminal, run: () => state().clearActiveTerminal() },
      { id: "closeAllSessions", label: "Close every session", run: () => state().closeAllSessions() },
    ];
    for (const p of profiles) {
      base.push({
        id: `profile:${p.id}`,
        label: `New tab: ${p.name}`,
        hint: p.available === false ? "not installed" : p.shell,
        run: () => state().newTab(p.id),
      });
    }
    for (const s of snippets ?? []) {
      base.push({
        id: `snippet:${s.id}`,
        label: `Run snippet: ${s.name}`,
        hint: s.command,
        run: () => state().runSnippet(s.command),
      });
    }
    return base.map((c) => ({ ...c, hint: c.hint ? formatChord(c.hint) : undefined }));
  }, [profiles, snippets, keybindings]);

  const matches = useMemo(() => {
    const needle = query.trim().toLowerCase();
    if (!needle) return commands;
    return commands.filter((c) => c.label.toLowerCase().includes(needle));
  }, [commands, query]);

  const [selected, setSelected] = useState(0);
  const active = Math.min(Math.max(selected, 0), Math.max(0, matches.length - 1));

  const execute = (cmd: Command | undefined) => {
    if (!cmd) return;
    setOverlay("none");
    cmd.run();
  };

  return (
    <div className="overlay" onMouseDown={() => setOverlay("none")}>
      <div className="palette" onMouseDown={(ev) => ev.stopPropagation()}>
        <input
          autoFocus
          placeholder="Type a command…"
          value={query}
          spellCheck={false}
          onChange={(ev) => {
            setQuery(ev.target.value);
            setSelected(0);
          }}
          onKeyDown={(ev) => {
            if (ev.key === "Escape") {
              ev.preventDefault();
              setOverlay("none");
              return;
            }
            if (ev.key === "Enter") {
              ev.preventDefault();
              execute(matches[active]);
              return;
            }
            if (ev.key === "ArrowDown") {
              ev.preventDefault();
              setSelected(Math.min(active + 1, matches.length - 1));
              return;
            }
            if (ev.key === "ArrowUp") {
              ev.preventDefault();
              setSelected(Math.max(active - 1, 0));
            }
          }}
        />
        <ul className="palette-list">
          {matches.slice(0, 40).map((cmd, i) => (
            <li key={cmd.id} className={i === active ? "row-active" : ""}>
              <button type="button" onClick={() => execute(cmd)} onMouseMove={() => setSelected(i)}>
                <span className="row-label">{cmd.label}</span>
                {cmd.hint && <span className="row-meta">{cmd.hint}</span>}
              </button>
            </li>
          ))}
          {matches.length === 0 && <li className="empty">No matching command</li>}
        </ul>
      </div>
    </div>
  );
}

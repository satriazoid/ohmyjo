import { useEffect, useMemo, useState } from "react";
import { formatChord } from "../keys";
import { useApp } from "../store";

/**
 * Searchable command history for the focused pane.
 *
 * Entries are recorded by the backend as they are typed, so selecting one
 * inserts it at the prompt for editing rather than running it outright — the
 * same contract as pressing Up.
 */
export function HistoryPanel() {
  const setOverlay = useApp((s) => s.setOverlay);
  const activePane = useApp((s) => s.activePane);
  const history = useApp((s) => s.history);
  const requestHistory = useApp((s) => s.requestHistory);
  const clearHistory = useApp((s) => s.clearHistory);
  const recallCommand = useApp((s) => s.recallCommand);
  const hint = useApp((s) => s.config?.keybindings?.history);
  const [query, setQuery] = useState("");

  // The store keys history by session, so the panel has to resolve the pane's
  // session itself; subscribing only to `activePane` would show a stale list
  // after the pane is restarted onto a new shell.
  const sessionId = useApp((s) => {
    for (const tab of s.tabs) {
      const pane = tab.panes[s.activePane];
      if (pane) return pane.sessionId ?? "";
    }
    return "";
  });

  useEffect(() => {
    requestHistory(activePane);
  }, [activePane, sessionId, requestHistory]);

  const entries = history[sessionId] ?? [];

  // Filtering locally keeps typing instant; the backend filter exists for the
  // case where the list is longer than what was fetched.
  const matches = useMemo(() => {
    const needle = query.trim().toLowerCase();
    if (!needle) return entries;
    return entries.filter((cmd) => cmd.toLowerCase().includes(needle));
  }, [entries, query]);

  const [selected, setSelected] = useState(0);
  useEffect(() => setSelected(0), [query, sessionId]);
  const active = Math.min(Math.max(selected, 0), Math.max(0, matches.length - 1));

  const insert = (command: string | undefined) => {
    if (!command) return;
    setOverlay("none");
    recallCommand(command);
  };

  return (
    <div className="overlay" onMouseDown={() => setOverlay("none")}>
      <div className="panel panel-history" onMouseDown={(ev) => ev.stopPropagation()}>
        <header className="panel-head">
          <span>History</span>
          <span className="panel-head-note">
            {entries.length} command{entries.length === 1 ? "" : "s"}
          </span>
        </header>

        <input
          autoFocus
          className="panel-input"
          placeholder="Search command history"
          value={query}
          spellCheck={false}
          onChange={(ev) => setQuery(ev.target.value)}
          onKeyDown={(ev) => {
            if (ev.key === "ArrowDown") {
              ev.preventDefault();
              setSelected((s) => Math.min(matches.length - 1, s + 1));
            } else if (ev.key === "ArrowUp") {
              ev.preventDefault();
              setSelected((s) => Math.max(0, s - 1));
            } else if (ev.key === "Enter") {
              ev.preventDefault();
              insert(matches[active]);
            } else if (ev.key === "Escape") {
              ev.preventDefault();
              setOverlay("none");
            }
            ev.stopPropagation();
          }}
        />

        <ul className="history-list">
          {matches.length === 0 ? (
            <li className="history-empty">
              {entries.length === 0 ? "Nothing recorded yet — run a command first." : "No match."}
            </li>
          ) : (
            matches.map((cmd, i) => (
              <li key={`${i}:${cmd}`}>
                <button
                  type="button"
                  className={`history-item${i === active ? " history-item-active" : ""}`}
                  onMouseEnter={() => setSelected(i)}
                  onClick={() => insert(cmd)}
                  title={cmd}
                >
                  {cmd}
                </button>
              </li>
            ))
          )}
        </ul>

        <footer className="panel-foot">
          <span className="hint">
            <kbd>Enter</kbd> insert at prompt
            {hint ? ` · ${formatChord(hint)} to reopen` : ""}
          </span>
          <button
            type="button"
            className="panel-danger"
            disabled={entries.length === 0}
            onClick={() => clearHistory(activePane)}
          >
            Clear history
          </button>
        </footer>
      </div>
    </div>
  );
}

import { useEffect, useState } from "react";
import { terminalFor } from "../terminals";
import { useApp } from "../store";

/** Incremental search inside the active pane's scrollback. */
export function SearchBar() {
  const open = useApp((s) => s.searchOpen);
  const setSearchOpen = useApp((s) => s.setSearchOpen);
  const activePane = useApp((s) => s.activePane);
  const [query, setQuery] = useState("");
  const [hits, setHits] = useState(0);

  useEffect(() => {
    if (!open) return;
    const onKey = (ev: KeyboardEvent) => {
      if (ev.key === "Escape") {
        ev.preventDefault();
        setSearchOpen(false);
      }
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [open, setSearchOpen]);

  useEffect(() => {
    if (!open) setQuery("");
  }, [open]);

  if (!open) return null;

  const run = (direction: "next" | "prev") => {
    const search = terminalFor(activePane)?.search;
    if (!search || !query) return;
    const found = direction === "next" ? search.findNext(query) : search.findPrevious(query);
    setHits(found ? 1 : 0);
  };

  return (
    <div className="searchbar">
      <input
        autoFocus
        placeholder="Find in scrollback"
        value={query}
        spellCheck={false}
        onChange={(ev) => {
          setQuery(ev.target.value);
          const search = terminalFor(activePane)?.search;
          if (!ev.target.value) {
            setHits(0);
            return;
          }
          // Incremental search reports its own result, so the "no match" hint
          // has to come from this call and not only from the Enter handler.
          // Without a handle there is nothing to report, which is not a miss.
          if (search) setHits(search.findNext(ev.target.value) ? 1 : 0);
        }}
        onKeyDown={(ev) => {
          if (ev.key === "Enter") {
            ev.preventDefault();
            run(ev.shiftKey ? "prev" : "next");
          }
          // The terminal keeps focus otherwise, which would swallow typing.
          ev.stopPropagation();
        }}
      />
      <span className="search-meta">{query ? (hits ? "" : "no match") : ""}</span>
      <button type="button" title="Previous (Shift+Enter)" onClick={() => run("prev")}>
        ↑
      </button>
      <button type="button" title="Next (Enter)" onClick={() => run("next")}>
        ↓
      </button>
      <button
        type="button"
        title="Close"
        onClick={() => {
          setSearchOpen(false);
          terminalFor(activePane)?.term.focus();
        }}
      >
        ✕
      </button>
    </div>
  );
}

import { useEffect, useRef } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { SearchAddon } from "@xterm/addon-search";
import { WebLinksAddon } from "@xterm/addon-web-links";
import "@xterm/xterm/css/xterm.css";

import type { Pane as PaneModel } from "../layout";
import { encodeBase64 } from "../protocol";
import { reportResize, sendMessage, useApp } from "../store";
import { registerTerminal, terminalFor, unregisterTerminal } from "../terminals";
import { xtermTheme } from "../theme";

interface Props {
  pane: PaneModel;
  active: boolean;
}

/**
 * One xterm.js instance per pane.
 *
 * The instance is created once per pane id and disposed when the pane unmounts,
 * so scrollback survives tab switches and splitting does not reset the
 * neighbouring terminal. Output flows socket -> `term.write` directly; React
 * state is never in the byte path.
 */
export function TerminalView({ pane, active }: Props) {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const config = useApp((s) => s.config);
  const configRef = useRef(config);
  configRef.current = config;

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    const cfg = configRef.current;

    const term = new Terminal({
      fontFamily: cfg?.appearance.fontFamily,
      fontSize: cfg?.appearance.fontSize ?? 14,
      lineHeight: cfg?.appearance.lineHeight ?? 1.2,
      letterSpacing: cfg?.appearance.letterSpacing ?? 0,
      cursorStyle: cfg?.appearance.cursorStyle ?? "block",
      cursorBlink: cfg?.appearance.cursorBlink ?? true,
      scrollback: cfg?.behavior.scrollback ?? 10000,
      allowProposedApi: true,
      // ConPTY reflows its own output; letting xterm guess duplicates the
      // reflow and corrupts full-screen apps.
      windowsPty: { backend: "conpty" },
      theme: xtermTheme(cfg?.themes?.[cfg.appearance.theme]),
    });
    const fit = new FitAddon();
    const search = new SearchAddon();
    term.loadAddon(fit);
    term.loadAddon(search);
    term.loadAddon(new WebLinksAddon());
    term.open(host);

    const refit = () => {
      if (host.clientWidth < 2 || host.clientHeight < 2) return;
      try {
        fit.fit();
      } catch {
        return;
      }
      if (term.cols > 0 && term.rows > 0) reportResize(pane.id, term.cols, term.rows);
    };

    registerTerminal(pane.id, { term, fit, search, element: host, refit });

    term.onData((data) => {
      const session = sessionIdOfPane(pane.id);
      if (!session) return;
      sendMessage({
        type: "input",
        id: session,
        data: encodeBase64(new TextEncoder().encode(data)),
      });
    });

    // Copy-on-select mirrors the Windows Terminal behaviour the spec asks for:
    // finishing a mouse selection puts it on the clipboard without Ctrl+C.
    term.onSelectionChange(() => {
      if (!configRef.current?.behavior.copyOnSelect) return;
      const text = term.getSelection();
      if (text) void navigator.clipboard.writeText(text).catch(() => undefined);
    });

    const observer = new ResizeObserver(refit);
    observer.observe(host);
    // One frame of delay: the flex layout has not settled on first paint and
    // fitting the pre-layout box yields a one-column terminal.
    const raf = window.requestAnimationFrame(refit);

    return () => {
      window.cancelAnimationFrame(raf);
      observer.disconnect();
      unregisterTerminal(pane.id);
      term.dispose();
    };
  }, [pane.id]);

  // Appearance edits apply live; the font metrics change forces a refit.
  useEffect(() => {
    const handle = terminalFor(pane.id);
    if (!handle || !config) return;
    handle.term.options.fontFamily = config.appearance.fontFamily;
    handle.term.options.fontSize = config.appearance.fontSize;
    handle.term.options.lineHeight = config.appearance.lineHeight;
    handle.term.options.letterSpacing = config.appearance.letterSpacing;
    handle.term.options.cursorStyle = config.appearance.cursorStyle;
    handle.term.options.cursorBlink = config.appearance.cursorBlink;
    handle.term.options.scrollback = config.behavior.scrollback;
    handle.term.options.theme = xtermTheme(config.themes?.[config.appearance.theme]);
    handle.refit();
  }, [config, pane.id]);

  useEffect(() => {
    if (active) terminalFor(pane.id)?.term.focus();
  }, [active, pane.id]);

  return (
    <div
      className="term-host"
      ref={hostRef}
      onMouseDown={() => useApp.getState().focusPane(pane.id)}
      onContextMenu={(ev) => {
        if (!configRef.current?.behavior.rightClickPaste) return;
        ev.preventDefault();
        useApp.getState().focusPane(pane.id);
        void navigator.clipboard
          .readText()
          .then((text) => {
            if (text) terminalFor(pane.id)?.term.paste(text);
          })
          .catch(() => undefined);
      }}
    />
  );
}

export function sessionIdOfPane(paneId: string): string | undefined {
  for (const tab of useApp.getState().tabs) {
    const pane = tab.panes[paneId];
    if (pane?.sessionId) return pane.sessionId;
  }
  return undefined;
}

/**
 * Terminal instance registry.
 *
 * xterm instances live outside React state on purpose: writing a stream of
 * output through the store would re-render the tree on every chunk. Components
 * register their handle here, and the message router writes straight into the
 * instance.
 */
import type { Terminal } from "@xterm/xterm";
import type { FitAddon } from "@xterm/addon-fit";
import type { SearchAddon } from "@xterm/addon-search";

export interface TerminalHandle {
  term: Terminal;
  fit: FitAddon;
  search: SearchAddon;
  /** Frame of the terminal host, for measuring and focusing. */
  element: HTMLElement;
  /** Refits and reports the new size to the backend. */
  refit: () => void;
}

const handles = new Map<string, TerminalHandle>();

export function registerTerminal(paneId: string, handle: TerminalHandle): void {
  handles.set(paneId, handle);
}

export function unregisterTerminal(paneId: string): void {
  handles.delete(paneId);
}

export function terminalFor(paneId: string): TerminalHandle | undefined {
  return handles.get(paneId);
}

export function writeToTerminal(paneId: string, bytes: Uint8Array): void {
  handles.get(paneId)?.term.write(bytes);
}

/** Focuses a pane's terminal; used after pane focus changes and tab switches. */
export function focusTerminal(paneId: string): void {
  handles.get(paneId)?.term.focus();
}

/** Refits every terminal, e.g. after the sidebar or window size changes. */
export function refitAll(): void {
  for (const handle of handles.values()) handle.refit();
}

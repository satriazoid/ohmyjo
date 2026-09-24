/** Theme plumbing: Go owns the palettes, the UI applies them as CSS vars. */
import type { ThemeSpec } from "./protocol";

/** CSS custom properties consumed by styles.css and the terminal host. */
const UI_VARS: Array<[keyof ThemeSpec, string]> = [
  ["uiBackground", "--ui-bg"],
  ["uiBackgroundAlt", "--ui-bg-alt"],
  ["uiBorder", "--ui-border"],
  ["uiForeground", "--ui-fg"],
  ["uiForegroundDim", "--ui-fg-dim"],
  ["uiAccent", "--ui-accent"],
  ["uiTabActive", "--ui-tab-active"],
  ["uiTabInactive", "--ui-tab-inactive"],
  ["uiPaneBorder", "--ui-pane-border"],
  ["uiSplitter", "--ui-splitter"],
];

export function applyTheme(theme: ThemeSpec | undefined, root: HTMLElement = document.documentElement): void {
  if (!theme) return;
  for (const [key, cssVar] of UI_VARS) {
    const value = theme[key];
    if (typeof value === "string" && value) root.style.setProperty(cssVar, value);
  }
  root.style.setProperty("--term-bg", theme.background);
  root.style.setProperty("--term-fg", theme.foreground);
  root.style.setProperty("--ui-danger", theme.red || "#f7768e");
}

/** The xterm.js ITheme derived from a palette. Keys match xterm's own names. */
export function xtermTheme(theme: ThemeSpec | undefined) {
  if (!theme) return undefined;
  return {
    background: theme.background,
    foreground: theme.foreground,
    cursor: theme.cursor,
    cursorAccent: theme.cursorAccent,
    selectionBackground: theme.selection,
    black: theme.black,
    red: theme.red,
    green: theme.green,
    yellow: theme.yellow,
    blue: theme.blue,
    magenta: theme.magenta,
    cyan: theme.cyan,
    white: theme.white,
    brightBlack: theme.brightBlack,
    brightRed: theme.brightRed,
    brightGreen: theme.brightGreen,
    brightYellow: theme.brightYellow,
    brightBlue: theme.brightBlue,
    brightMagenta: theme.brightMagenta,
    brightCyan: theme.brightCyan,
    brightWhite: theme.brightWhite,
  };
}

export function themeNames(themes: Record<string, ThemeSpec> | undefined): string[] {
  return Object.keys(themes ?? {}).sort((a, b) => a.localeCompare(b));
}

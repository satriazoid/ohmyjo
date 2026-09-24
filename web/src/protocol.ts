// Wire protocol between the React frontend and the Go backend.
// Transport: one WebSocket at `ws://<origin>/ws`; terminal bytes are base64
// so partial UTF-8 sequences survive transport (xterm decodes incrementally).

export const PROTOCOL_VERSION = 1;

export interface Profile {
  /** Stable identifier, also used as the profile key. */
  id: string;
  name: string;
  /** Executable, absolute path or resolvable name (pwsh, cmd, bash.exe...). */
  shell: string;
  args?: string[];
  /** Working directory; empty means behavior.defaultCwd. */
  cwd?: string;
  env?: Record<string, string>;
  /** Icon key resolved by the frontend (`pwsh`, `cmd`, `git`, `wsl`, `generic`). */
  icon?: string;
  color?: string;
  hidden?: boolean;
  /** Built-in profiles are re-detected on load and cannot be deleted. */
  builtin?: boolean;
  /** Set by the backend when the executable could not be located. */
  available?: boolean;
}

export interface ThemeSpec {
  name: string;
  background: string;
  foreground: string;
  cursor: string;
  cursorAccent?: string;
  selection?: string;
  black: string;
  red: string;
  green: string;
  yellow: string;
  blue: string;
  magenta: string;
  cyan: string;
  white: string;
  brightBlack: string;
  brightRed: string;
  brightGreen: string;
  brightYellow: string;
  brightBlue: string;
  brightMagenta: string;
  brightCyan: string;
  brightWhite: string;
  /** 0..1 UI surface alpha (window transparency) */
  opacity?: number;
  /** Acrylic-style backdrop blur for the UI chrome. */
  blur?: boolean;
  /** UI surface colours, applied as CSS custom properties. */
  uiBackground?: string;
  uiBackgroundAlt?: string;
  uiBorder?: string;
  uiForeground?: string;
  uiForegroundDim?: string;
  uiAccent?: string;
  uiTabActive?: string;
  uiTabInactive?: string;
  uiPaneBorder?: string;
  uiSplitter?: string;
}

export interface AppearanceConfig {
  theme: string;
  fontFamily: string;
  fontSize: number;
  lineHeight: number;
  letterSpacing: number;
  cursorStyle: "block" | "underline" | "bar";
  cursorBlink: boolean;
  background: string | null;
  backgroundOpacity: number;
  padding: number;
  borderRadius: number;
}

export interface BehaviorConfig {
  defaultProfile: string;
  defaultCwd: string;
  copyOnSelect: boolean;
  rightClickPaste: boolean;
  scrollback: number;
}

export interface SidebarConfig {
  visible: boolean;
  side: "left" | "right";
  width: number;
  mode: "sessions" | "profiles" | "snippets" | "explorer" | "settings";
  autoHide: boolean;
}

export interface Snippet {
  id: string;
  name: string;
  command: string;
  /** Optional profile filter; empty = every profile. */
  profile?: string;
}

export interface Config {
  version: number;
  appearance: AppearanceConfig;
  behavior: BehaviorConfig;
  keybindings: Record<string, string>;
  sidebar: SidebarConfig;
  profiles: Profile[];
  themes: Record<string, ThemeSpec>;
  snippets: Snippet[];
  /** Repairs the loader had to make; shown as warnings, never persisted. */
  diagnostics?: string[];
}

export interface SessionInfo {
  id: string;
  profile: string;
  name: string;
  pid: number;
  cwd: string;
  status: "running" | "exited";
  exitCode?: number;
}

export interface GitInfo {
  repo: boolean;
  branch?: string;
  dirty?: number;
  root?: string;
  error?: string;
}

export interface DirListing {
  path: string;
  parent?: string;
  entries: Array<{ name: string; dir: boolean; size: number }>;
  error?: string;
}

/** Result of the native file/folder picker. An empty path means cancelled. */
export interface PickResult {
  path: string;
}

// ---------------------------------------------------------------- client -> server

export type ClientMessage =
  | { type: "hello"; version: number; client: string }
  | { type: "create"; reqId: string; profile: string; cols: number; rows: number; cwd?: string }
  | { type: "attach"; id: string; cols: number; rows: number }
  | { type: "detach"; id: string }
  | { type: "input"; id: string; data: string }
  | { type: "resize"; id: string; cols: number; rows: number }
  | { type: "close"; id: string }
  | { type: "restart"; reqId: string; id: string; cols: number; rows: number }
  | { type: "history"; id: string; query?: string; limit?: number }
  | { type: "historyClear"; id: string }
  | { type: "list" }
  | { type: "ping" };

// ---------------------------------------------------------------- server -> client

export type ServerMessage =
  | { type: "ready"; version: string; protocol: number; platform: string }
  | {
      type: "created";
      reqId: string;
      id: string;
      profile: string;
      name: string;
      pid: number;
      cwd: string;
    }
  | { type: "attached"; id: string; replay: string; info: SessionInfo }
  | { type: "output"; id: string; data: string }
  | { type: "exit"; id: string; code: number }
  | { type: "closed"; id: string }
  | { type: "error"; reqId?: string; code: string; message: string }
  | { type: "config"; config: Config }
  | { type: "sessions"; sessions: SessionInfo[] }
  | { type: "history"; id: string; history: string[] }
  | { type: "historyEntry"; id: string; history: string[] }
  | { type: "pong" };

export function encodeBase64(bytes: Uint8Array): string {
  let binary = "";
  const chunk = 0x8000;
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunk));
  }
  return btoa(binary);
}

export function decodeBase64(text: string): Uint8Array {
  const binary = atob(text);
  const out = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) out[i] = binary.charCodeAt(i);
  return out;
}

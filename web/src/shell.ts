/**
 * Bridge to the native window, injected by the Go shell via WebView2's binding
 * mechanism. These functions only exist in the desktop app: in a plain browser
 * (or the headless test harness) they are undefined and the window controls are
 * not rendered at all.
 */
declare global {
  interface Window {
    ojWindowDrag?: () => Promise<unknown>;
    ojWindowMinimize?: () => Promise<unknown>;
    ojWindowToggleMaximize?: () => Promise<boolean>;
    ojWindowClose?: () => Promise<unknown>;
    ojWindowIsMaximized?: () => Promise<boolean>;
  }
}

/** True when the page is running inside the ohmyjo desktop shell. */
export function isDesktopShell(): boolean {
  return typeof window !== "undefined" && typeof window.ojWindowDrag === "function";
}

export const shell = {
  drag: () => window.ojWindowDrag?.(),
  minimize: () => window.ojWindowMinimize?.(),
  /**
   * Toggles maximize and reports the resulting state. The fallback exists so a
   * browser run does not leave the button stuck on the wrong icon.
   */
  toggleMaximize: async (): Promise<boolean> => {
    if (window.ojWindowToggleMaximize) return window.ojWindowToggleMaximize();
    return false;
  },
  close: () => window.ojWindowClose?.(),
  isMaximized: async (): Promise<boolean> => {
    if (window.ojWindowIsMaximized) return window.ojWindowIsMaximized();
    return false;
  },
};

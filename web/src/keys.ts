/** Keyboard shortcut parsing and matching for the configurable keybindings. */

export interface Chord {
  key: string;
  ctrl: boolean;
  alt: boolean;
  shift: boolean;
  meta: boolean;
}

const MOD_ALIASES: Record<string, keyof Omit<Chord, "key">> = {
  ctrl: "ctrl",
  control: "ctrl",
  alt: "alt",
  option: "alt",
  shift: "shift",
  cmd: "meta",
  meta: "meta",
  super: "meta",
  win: "meta",
};

const KEY_ALIASES: Record<string, string> = {
  comma: ",",
  arrowright: "arrowright",
  arrowleft: "arrowleft",
  arrowup: "arrowup",
  arrowdown: "arrowdown",
  escape: "escape",
  esc: "escape",
  enter: "enter",
  space: " ",
  digit0: "0",
  digit1: "1",
  digit2: "2",
  digit3: "3",
  digit4: "4",
  digit5: "5",
  digit6: "6",
  digit7: "7",
  digit8: "8",
  digit9: "9",
};

/** Parses "ctrl+shift+t" into a chord. Returns null for unusable specs. */
export function parseChord(spec: string): Chord | null {
  if (!spec) return null;
  const parts = spec
    .toLowerCase()
    .split("+")
    .map((p) => p.trim())
    .filter(Boolean);
  if (parts.length === 0) return null;
  const chord: Chord = { key: "", ctrl: false, alt: false, shift: false, meta: false };
  for (const part of parts) {
    const mod = MOD_ALIASES[part];
    if (mod) {
      chord[mod] = true;
      continue;
    }
    chord.key = KEY_ALIASES[part] ?? part;
  }
  return chord.key ? chord : null;
}

function normaliseKey(ev: KeyboardEvent): string {
  if (ev.key === " ") return " ";
  const key = ev.key.toLowerCase();
  // Ctrl+Shift+I reports "I"; the case is already lost above, which is what we
  // want because keybinding specs are case-insensitive.
  return KEY_ALIASES[key] ?? key;
}

/** True when the event matches the spec, e.g. "ctrl+shift+t". */
export function matchesChord(ev: KeyboardEvent, spec: string): boolean {
  const chord = parseChord(spec);
  if (!chord) return false;
  if (chord.ctrl !== ev.ctrlKey) return false;
  if (chord.alt !== ev.altKey) return false;
  if (chord.meta !== ev.metaKey) return false;
  if (chord.shift !== ev.shiftKey) return false;
  return chord.key === normaliseKey(ev);
}

/**
 * Finds the action bound to an event. Longer specs are matched first so
 * "ctrl+shift+t" wins over a plain "t" binding.
 */
export function actionFor(ev: KeyboardEvent, bindings: Record<string, string>): string | null {
  const entries = Object.entries(bindings).sort((a, b) => b[1].length - a[1].length);
  for (const [action, spec] of entries) {
    if (matchesChord(ev, spec)) return action;
  }
  return null;
}

/** Human-readable form for menus and the command palette. */
export function formatChord(spec: string): string {
  return spec
    .split("+")
    .map((p) => {
      const part = p.trim().toLowerCase();
      if (part === "ctrl") return "Ctrl";
      if (part === "alt") return "Alt";
      if (part === "shift") return "Shift";
      if (part === "meta") return "Win";
      if (part === "comma") return ",";
      if (part.startsWith("arrow")) return part.replace("arrow", "Arrow ");
      if (part.startsWith("digit")) return part.replace("digit", "");
      if (part === "equal") return "=";
      if (part === "minus") return "-";
      return part.length === 1 ? part.toUpperCase() : part.charAt(0).toUpperCase() + part.slice(1);
    })
    .join("+");
}

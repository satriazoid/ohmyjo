import { useEffect, useState } from "react";
import { api } from "../api";
import { formatChord } from "../keys";
import { themeNames } from "../theme";
import { useApp } from "../store";
import type { AppearanceConfig, BehaviorConfig } from "../protocol";

type Tab = "appearance" | "behavior" | "keybindings" | "themes" | "snippets" | "about";

const TABS: Tab[] = ["appearance", "behavior", "keybindings", "themes", "snippets", "about"];

/** Full settings surface; every change is persisted through PUT /api/config. */
export function SettingsPanel() {
  const setOverlay = useApp((s) => s.setOverlay);
  const config = useApp((s) => s.config);
  const [tab, setTab] = useState<Tab>("appearance");
  if (!config) return null;

  return (
    <div className="overlay" onMouseDown={() => setOverlay("none")}>
      <div className="settings" onMouseDown={(ev) => ev.stopPropagation()}>
        <header>
          <nav>
            {TABS.map((t) => (
              <button type="button" key={t} className={t === tab ? "mode-active" : ""} onClick={() => setTab(t)}>
                {t}
              </button>
            ))}
          </nav>
          <button type="button" title="Close" onClick={() => setOverlay("none")}>
            ✕
          </button>
        </header>
        <div className="settings-body">
          {tab === "appearance" && <AppearanceSettings />}
          {tab === "behavior" && <BehaviorSettings />}
          {tab === "keybindings" && <KeybindingSettings />}
          {tab === "themes" && <ThemeSettings />}
          {tab === "snippets" && <SnippetSettings />}
          {tab === "about" && <About />}
        </div>
      </div>
    </div>
  );
}

function AppearanceSettings() {
  const appearance = useApp((s) => s.config?.appearance);
  const themes = useApp((s) => s.config?.themes);
  const fonts = useApp((s) => s.fonts);
  const updateConfig = useApp((s) => s.updateConfig);
  if (!appearance) return null;

  const patch = (mutate: (next: AppearanceConfig) => void) =>
    updateConfig((cfg) => {
      mutate(cfg.appearance);
    });

  return (
    <section>
      <label>
        Theme
        <select value={appearance.theme} onChange={(ev) => patch((a) => (a.theme = ev.target.value))}>
          {themeNames(themes).map((name) => (
            <option key={name} value={name}>
              {name}
            </option>
          ))}
        </select>
      </label>
      <label>
        Font family
        <input
          list="font-families"
          value={appearance.fontFamily}
          spellCheck={false}
          onChange={(ev) => patch((a) => (a.fontFamily = ev.target.value))}
        />
        <datalist id="font-families">
          {fonts.map((f) => (
            <option key={f} value={f} />
          ))}
        </datalist>
      </label>
      <label>
        Font size
        <input
          type="number"
          min={6}
          max={48}
          value={appearance.fontSize}
          onChange={(ev) => patch((a) => (a.fontSize = Number(ev.target.value) || a.fontSize))}
        />
      </label>
      <label>
        Line height
        <input
          type="number"
          step={0.05}
          min={1}
          max={2}
          value={appearance.lineHeight}
          onChange={(ev) => patch((a) => (a.lineHeight = Number(ev.target.value) || a.lineHeight))}
        />
      </label>
      <label>
        Letter spacing
        <input
          type="number"
          min={-2}
          max={8}
          value={appearance.letterSpacing}
          onChange={(ev) => patch((a) => (a.letterSpacing = Number(ev.target.value) || 0))}
        />
      </label>
      <label>
        Cursor
        <select
          value={appearance.cursorStyle}
          onChange={(ev) => patch((a) => (a.cursorStyle = ev.target.value as AppearanceConfig["cursorStyle"]))}
        >
          <option value="block">block</option>
          <option value="bar">bar</option>
          <option value="underline">underline</option>
        </select>
      </label>
      <label className="check">
        <input type="checkbox" checked={appearance.cursorBlink} onChange={(ev) => patch((a) => (a.cursorBlink = ev.target.checked))} />
        Blinking cursor
      </label>
      <label>
        Window background image (URL or path)
        <input
          value={appearance.background ?? ""}
          spellCheck={false}
          placeholder="none"
          onChange={(ev) => patch((a) => (a.background = ev.target.value || null))}
        />
      </label>
      <label>
        Background opacity
        <input
          type="number"
          step={0.05}
          min={0}
          max={1}
          value={appearance.backgroundOpacity}
          onChange={(ev) => patch((a) => (a.backgroundOpacity = Number(ev.target.value)))}
        />
      </label>
      <label>
        Terminal padding (px)
        <input
          type="number"
          min={0}
          max={64}
          value={appearance.padding}
          onChange={(ev) => patch((a) => (a.padding = Number(ev.target.value) || 0))}
        />
      </label>
    </section>
  );
}

function BehaviorSettings() {
  const behavior = useApp((s) => s.config?.behavior);
  const profiles = useApp((s) => s.profiles);
  const updateConfig = useApp((s) => s.updateConfig);
  if (!behavior) return null;

  const patch = (mutate: (next: BehaviorConfig) => void) =>
    updateConfig((cfg) => {
      mutate(cfg.behavior);
    });

  return (
    <section>
      <label>
        Default profile
        <select value={behavior.defaultProfile} onChange={(ev) => patch((b) => (b.defaultProfile = ev.target.value))}>
          <option value="">(first available)</option>
          {profiles.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
      </label>
      <label>
        Default working directory
        <input
          value={behavior.defaultCwd}
          spellCheck={false}
          placeholder="~"
          onChange={(ev) => patch((b) => (b.defaultCwd = ev.target.value))}
        />
      </label>
      <label>
        Scrollback lines
        <input
          type="number"
          min={100}
          max={200000}
          value={behavior.scrollback}
          onChange={(ev) => patch((b) => (b.scrollback = Number(ev.target.value) || b.scrollback))}
        />
      </label>
      <label className="check">
        <input
          type="checkbox"
          checked={behavior.copyOnSelect}
          onChange={(ev) => patch((b) => (b.copyOnSelect = ev.target.checked))}
        />
        Copy on select
      </label>
      <label className="check">
        <input
          type="checkbox"
          checked={behavior.rightClickPaste}
          onChange={(ev) => patch((b) => (b.rightClickPaste = ev.target.checked))}
        />
        Paste on right click
      </label>
    </section>
  );
}

function KeybindingSettings() {
  const keybindings = useApp((s) => s.config?.keybindings);
  const updateConfig = useApp((s) => s.updateConfig);
  const [editing, setEditing] = useState<string | null>(null);
  if (!keybindings) return null;

  return (
    <section className="keys">
      <p className="hint">
        Click a binding and press the new chord. Esc cancels; Backspace unbinds.
      </p>
      <ul className="list">
        {Object.entries(keybindings).map(([action, spec]) => (
          <li key={action}>
            <span className="row-label">{action}</span>
            <button
              type="button"
              className={editing === action ? "capturing" : ""}
              onClick={() => setEditing(action)}
              onKeyDown={(ev) => {
                if (editing !== action) return;
                ev.preventDefault();
                ev.stopPropagation();
                if (ev.key === "Escape") {
                  setEditing(null);
                  return;
                }
                if (ev.key === "Backspace") {
                  updateConfig((cfg) => {
                    delete cfg.keybindings[action];
                  });
                  setEditing(null);
                  return;
                }
                const chord = describeChord(ev);
                if (!chord) return;
                updateConfig((cfg) => {
                  cfg.keybindings[action] = chord;
                });
                setEditing(null);
              }}
            >
              {editing === action ? "press keys…" : formatChord(spec)}
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}

/** Turns a keystroke into the config's chord format ("ctrl+shift+t"). */
function describeChord(ev: React.KeyboardEvent<HTMLButtonElement>): string | null {
  const key = ev.key.toLowerCase();
  if (["control", "shift", "alt", "meta"].includes(key)) return null;
  const parts: string[] = [];
  if (ev.ctrlKey) parts.push("ctrl");
  if (ev.shiftKey) parts.push("shift");
  if (ev.altKey) parts.push("alt");
  const named = KEY_NAMES[key] ?? key;
  parts.push(named);
  return parts.join("+");
}

const KEY_NAMES: Record<string, string> = {
  ",": "comma",
  "=": "equal",
  "-": "minus",
  " ": "space",
  arrowup: "arrowup",
  arrowdown: "arrowdown",
  arrowleft: "arrowleft",
  arrowright: "arrowright",
  escape: "escape",
  enter: "enter",
};

function ThemeSettings() {
  const themes = useApp((s) => s.config?.themes);
  const active = useApp((s) => s.config?.appearance.theme);
  const updateConfig = useApp((s) => s.updateConfig);
  if (!themes) return null;

  return (
    <section>
      <p className="hint">
        Themes come from the backend and hot reload from config.json. To add one, copy an entry under{" "}
        <code>themes</code> and edit the colours.
      </p>
      <ul className="theme-grid">
        {themeNames(themes).map((name) => {
          const t = themes[name];
          return (
            <li key={name} className={name === active ? "theme-active" : ""}>
              <button
                type="button"
                onClick={() =>
                  updateConfig((cfg) => {
                    cfg.appearance.theme = name;
                  })
                }
              >
                <span className="theme-preview" style={{ background: t.background, color: t.foreground }}>
                  <span style={{ color: t.red }}>●</span>
                  <span style={{ color: t.green }}>●</span>
                  <span style={{ color: t.yellow }}>●</span>
                  <span style={{ color: t.blue }}>●</span>
                </span>
                <span className="row-label">{name}</span>
              </button>
            </li>
          );
        })}
      </ul>
    </section>
  );
}

function SnippetSettings() {
  const snippets = useApp((s) => s.config?.snippets);
  const profiles = useApp((s) => s.profiles);
  const updateConfig = useApp((s) => s.updateConfig);
  const [name, setName] = useState("");
  const [command, setCommand] = useState("");
  const [profile, setProfile] = useState("");

  return (
    <section>
      <ul className="list">
        {(snippets ?? []).map((s) => (
          <li key={s.id}>
            <span className="row-label">{s.name}</span>
            <span className="row-path">{s.command}</span>
            <button
              type="button"
              title="Delete"
              onClick={() =>
                updateConfig((cfg) => {
                  cfg.snippets = cfg.snippets.filter((x) => x.id !== s.id);
                })
              }
            >
              ✕
            </button>
          </li>
        ))}
        {!snippets?.length && <li className="hint">No snippets yet.</li>}
      </ul>
      <div className="snippet-form">
        <input placeholder="Name" value={name} onChange={(ev) => setName(ev.target.value)} />
        <input placeholder="Command" value={command} spellCheck={false} onChange={(ev) => setCommand(ev.target.value)} />
        <select value={profile} onChange={(ev) => setProfile(ev.target.value)}>
          <option value="">any profile</option>
          {profiles.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
        <button
          type="button"
          disabled={!name.trim() || !command.trim()}
          onClick={() => {
            updateConfig((cfg) => {
              // Ids must be unique because the sidebar keys rows by them.
              cfg.snippets = [
                ...cfg.snippets,
                { id: `snip-${Date.now().toString(36)}`, name: name.trim(), command, profile },
              ];
            });
            setName("");
            setCommand("");
            setProfile("");
          }}
        >
          Add snippet
        </button>
      </div>
    </section>
  );
}

function About() {
  const version = useApp((s) => s.version);
  const configPath = useApp((s) => s.configPath);
  const profiles = useApp((s) => s.profiles);
  const [health, setHealth] = useState<string>("");

  useEffect(() => {
    void api
      .health()
      .then((h) => setHealth(`${h.version} · protocol ${h.protocol} · up ${Math.round(h.uptime)}s`))
      .catch((err: unknown) => setHealth(String(err)));
  }, []);

  return (
    <section>
      <p>ohmyjo {version || health}</p>
      <p className="hint">
        Config file: <code>{configPath || "unknown"}</code>
      </p>
      <p className="hint">Detected profiles: {profiles.length}</p>
      <div className="about-actions">
        <button
          type="button"
          onClick={() => {
            if (configPath) void api.reveal(configPath);
          }}
        >
          Reveal config file
        </button>
        <button
          type="button"
          onClick={() => {
            if (configPath) void api.open(configPath);
          }}
        >
          Open config file
        </button>
      </div>
    </section>
  );
}

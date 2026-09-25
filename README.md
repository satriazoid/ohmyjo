# ohmyjo

A GPU-free, mouse-first terminal for Windows, written in Go. It opens a window,
runs your shell in a ConPTY, parses the ANSI stream itself, and draws every pixel
with GDI. There is no browser engine, no webview, no HTML and no JavaScript at
runtime.

## What it is

- **A native Windows terminal.** The whole surface is one `Format32bppArgb` DIB
  blitted into the window's client area. Text is drawn with GDI's `DrawTextW`
  from a monospace font slot, on a grid the emulator lays out itself.
- **Frameless.** The window has no caption, so the tab strip doubles as the
  title bar: dragging it moves the window, and the minimise, maximise and close
  buttons are drawn and hit-tested by the application.
- **Mouse-first.** Every affordance has a click target. Shortcuts exist and are
  configurable, but nothing only has a shortcut.
- **Windows 10 and 11, x64.** ConPTY is required, so older releases are out.

## Requirements

- Go 1.26 or newer (`go.mod` declares `go 1.26`).
- Windows 10 1809 or newer for ConPTY.
- Optional: [`rsrc`](https://github.com/akavel/rsrc), only needed to
  regenerate `rsrc.syso` from `assets/ohmyjo.ico` after the icon changes. The
  generated `.syso` is committed, so an ordinary build does not need the tool.

## Building

```powershell
.\build.ps1
```

Or by hand:

```powershell
go build -trimpath -ldflags '-H=windowsgui -s -w' -o ohmyjo.exe .
```

The linker flags are not cosmetic. Without `-H=windowsgui` Go produces a
console-subsystem executable, and Windows then creates a console window beside
the application: the app looks like it opened a second terminal, and the startup
log lands there instead of in the log file.

`go build` links every `.syso` in the package directory. Keep exactly one
`rsrc.syso` in the repository root; a second one is a duplicate-resource link
error.

Tests and checks:

```powershell
gofmt -l internal/ main.go
go build ./...
go vet ./internal/...
go test ./internal/...
```

## Running

```powershell
.\ohmyjo.exe
```

## Layout of the source

| package | responsibility |
| --- | --- |
| `internal/vt` | The terminal emulator: an ANSI/VT parser and the screen state it drives. No Windows dependencies, so it is the package with real unit tests. |
| `internal/ui` | The GDI layer: the drawing surface, font slots, DPI handling, the window and its message loop, the input encoders. |
| `internal/session` | Shell processes: profile resolution, ConPTY attachment, reader goroutines, the session registry the side panel lists. |
| `internal/app` | The application: the view, the tab/pane layout tree, key and mouse routing, the side panel, the window chrome. |
| `internal/profiles` | Shell discovery: which shells exist on this machine and whether each is usable. |
| `internal/config` | The config file, its defaults, and a watcher that reapplies changes live. |
| `internal/history` | Command history recorded as you type, seeded into each new shell. |

`main.go` wires the packages together and calls into `internal/app`.

## Configuration

The config lives at `%AppData%\ohmyjo\config.json` (that is
`os.UserConfigDir()/ohmyjo/config.json`). It is watched: saving it reapplies the
theme, fonts, scrollback and keybindings without a restart. `history.txt` sits
beside it.

```jsonc
{
  "version": 1,
  "appearance": {
    "theme": "dracula",
    "fontFamily": "Cascadia Mono",
    "fontSize": 14,
    "cursorBlink": true,
    "padding": 8
  },
  "behavior": {
    "defaultProfile": "powershell",
    "scrollback": 10000,
    "copyOnSelect": false,
    "rightClickPaste": true
  },
  "sidebar": {
    "visible": true,
    "side": "left",   // "left" or "right"
    "width": 260,
    "mode": "sessions"
  },
  "keybindings": {
    "newTab": "ctrl+shift+t",
    "splitRight": "alt+shift+d"
  }
}
```

Chords are written as `ctrl+shift+t`, `alt+arrowright`, `ctrl+comma`. Several
names are accepted for keys that have no character: `comma`, `equal`, `minus`,
`space`, `plus`, `digit0`–`digit9`. On a terminal `ctrl+h` is Backspace, so
history is on `ctrl+shift+h`.

The shipped defaults are the whole binding table in `internal/config`; anything
missing from `keybindings` keeps its default.

## Affordances

**Tab strip.** One click selects a tab; the `×` on the active tab closes it. The
`+` opens a tab, and the two diagrams beside it split the focused pane right and
down. At the trailing edge, just inside the window buttons, two buttons are
pinned: a small triangle hides and restores the tab bar, and a small square shows
and hides the side panel: filled while the panel is up, hollow while it is away.

The two are pinned rather than flowed after the tabs, and they trade places
exactly when the bar is hidden, so the control that brings the bar back sits
where it was drawn. With the bar hidden there are no tabs, so the whole row to
the left of them is empty; dragging there still moves the window, because that
row is the frameless window's title bar.

Hiding the tab bar collapses the strip to what the window's own buttons need
rather than removing the row: the row *is* the frameless window's title bar, so
without it there would be no mouse way to move the window or close it. It does
not touch the side panel: the panel has its own toggle on the same row, and a
hide that also hides something else reads as a bug.

**Side panel.** Lists the running sessions with their state. Each row's `✕` ends
that session. The panel's own `✕` sits in its header, anchored to the panel's
inner edge, so it is top-right when the panel is docked right and top-left when
it is docked left. The panel slides rather than snapping, and reserves no width:
it floats over the panes, so opening it does not resize every shell.

The row the window is showing (the focused pane's session, or the maximized
pane's, since that is the one on screen) carries an accent bar on its leading
edge, the same marker the active tab uses. Clicking any other row brings that
session forward: its tab comes to the front, its pane takes the focus, and a
maximized sibling steps aside. Clicking the row that is already in front changes
nothing. Ending a background session leaves the keyboard where it was.

**Panes.** Drag a split's divider to resize it. Click a pane to focus it; the
focused pane of a split is outlined. Selecting text in a pane copies it when
`copyOnSelect` is on; right-click pastes when `rightClickPaste` is on.

**Tooling.** `scripts/make-icon.mjs` generates `assets/ohmyjo.ico` and
`scripts/check-icon.mjs` decodes it back and asserts the mark is what the design
calls for. Run `check-icon` after regenerating the icon: a stale or wrong icon
is otherwise invisible until it reaches the taskbar.

## Notes on the design

**One renderer, no fallback.** Pane bounds, GDI clipping and hit testing all
derive from a single grid rectangle computed per layout pass. A pane does not
have a drawn position and a separate clickable position, so they cannot drift.

**Rows are derived, not stored.** The panel's rows, its close button and the
strip's buttons are recomputed from the visible rectangle on every paint and
every hit test. A button stored as a rectangle is a button that stops working
mid-slide.

**The emulator is pure.** `internal/vt` has no Windows imports, so the parser and
the screen state can be tested without a window, an event loop or a shell.

**ConPTY sizes are forwarded, never guessed.** A pane's grid is resized first and
the result reported to the shell, because a shell that starts at the wrong width
wraps its first output at the wrong column.

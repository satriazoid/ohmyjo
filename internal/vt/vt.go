package vt

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
)

// Terminal represents the virtual terminal emulator.
type Terminal interface {
	// View displays the virtual terminal.
	View

	// Write parses input and writes terminal changes to state.
	io.Writer

	// Parse blocks on read on pty or io.Reader, then parses sequences until
	// buffer empties. State is locked as soon as first rune is read, and unlocked
	// when buffer is empty.
	Parse(bf *bufio.Reader) error

	// SetScrollbackSize sets how many lines of history to retain above the
	// viewport. Shrinking discards the oldest lines immediately.
	SetScrollbackSize(n int)
}

// View represents the view of the virtual terminal emulator.
type View interface {
	// String dumps the virtual terminal contents.
	fmt.Stringer

	// Size returns the size of the virtual terminal.
	Size() (cols, rows int)

	// Resize changes the size of the virtual terminal.
	Resize(cols, rows int)

	// Mode returns the current terminal mode.//
	Mode() ModeFlag

	// Title represents the title of the console window.
	Title() string

	// Cell returns the glyph containing the character code, foreground color, and
	// background color at position (x, y) relative to the top left of the terminal.
	Cell(x, y int) Glyph

	// ViewRow returns the cells to draw at screen row y, honouring the scroll
	// offset, so a renderer never walks history itself.
	ViewRow(y int) (Line, bool)

	// ScrollbackLen is the number of lines above the viewport.
	ScrollbackLen() int

	// ClearScrollback discards all history above the viewport. Clearing the
	// screen is an escape sequence; discarding history has none, so the
	// renderer calls this rather than the shell.
	ClearScrollback()

	// ContentRows is the total number of addressable lines, history plus screen.
	ContentRows() int

	// ContentRow returns the line at an absolute index, where 0 is the oldest
	// addressable line. Selections are anchored to these indices rather than to
	// screen rows so scrolling does not move them.
	ContentRow(idx int) (Line, bool)

	// ScrollOffset is how many lines the viewport is scrolled back from the
	// live screen.
	ScrollOffset() int

	// ViewportTop is the absolute content index of the first visible row.
	ViewportTop() int

	// ScrollUp moves the viewport by delta lines; positive scrolls back into
	// history. It returns the resulting offset.
	ScrollUp(delta int) int

	// ScrollTo sets the viewport offset, clamped to the available history.
	ScrollTo(offset int) int

	// DirtyLines reports the inclusive band of rows changed since the last
	// ClearDamage, and whether any row changed at all.
	DirtyLines() (y0, y1 int, any bool)

	// CursorCell reports the cursor's viewport position. It is false when the
	// cursor is hidden or the viewport is scrolled back, in which case no
	// cursor is drawn.
	CursorCell() (x, y int, visible bool)

	// CursorShape reports the DECSCUSR cursor style.
	CursorShape() int

	// CursorBlink reports whether the cursor was asked to blink.
	CursorBlink() bool

	// Cursor returns the current position of the cursor.
	Cursor() Cursor

	// CursorVisible returns the visible state of the cursor.
	CursorVisible() bool

	// Lock locks the state so a whole frame can be read consistently, and
	// Unlock releases it. Damage is not reset by Unlock; call ClearDamage
	// after painting the rows reported by DirtyLines.
	Lock()

	Unlock()

	ClearDamage()
}

type TerminalOption func(*TerminalInfo)

type TerminalInfo struct {
	w          io.Writer
	cols, rows int
}

func WithWriter(w io.Writer) TerminalOption {
	return func(info *TerminalInfo) {
		info.w = w
	}
}

func WithSize(cols, rows int) TerminalOption {
	return func(info *TerminalInfo) {
		info.cols = cols
		info.rows = rows
	}
}

// New returns a new virtual terminal emulator.
func New(opts ...TerminalOption) Terminal {
	info := TerminalInfo{
		w:    ioutil.Discard,
		cols: 80,
		rows: 24,
	}
	for _, opt := range opts {
		opt(&info)
	}
	return newTerminal(info)
}

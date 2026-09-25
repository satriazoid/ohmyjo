package app

import (
	"testing"

	"ohmyjo/internal/session"
	"ohmyjo/internal/ui"
)

// fakePaneSession records everything a pane sends to its shell, so the input
// path can be asserted without a console host.
type fakePaneSession struct {
	written []byte
	resizes [][2]int
}

func (f *fakePaneSession) Write(b []byte) error { f.written = append(f.written, b...); return nil }
func (f *fakePaneSession) Resize(c, r int) error {
	f.resizes = append(f.resizes, [2]int{c, r})
	return nil
}
func (f *fakePaneSession) Info() session.Info { return session.Info{} }
func (f *fakePaneSession) Close() error       { return nil }

// viewWithPane builds the smallest view that can route a key: one tab holding
// one pane, already attached to a recording session. The window is left nil, so
// the repaint the input path requests has nowhere to go. The test asserts the
// bytes that reach the shell, which is where the duplication showed up.
func viewWithPane() (*View, *fakePaneSession) {
	p := NewPane(80, 24, ui.NewStyleResolver(ui.DefaultPalette()), ui.Metrics{CellW: 8, Ascent: 12, Descent: 3, LineH: 15}, 0)
	fake := &fakePaneSession{}
	p.sess = fake
	p.id = "s1"
	v := &View{pendingKey: false}
	v.tabs = []*Tab{{root: &node{pane: p}, focus: p}}
	v.active = 0
	return v, fake
}

// TestEncodedKeySendsOneCopy is the regression for commands running twice.
//
// A key press reaches the window as WM_KEYDOWN and then as the WM_CHAR Windows
// derived from it. For Enter the encoder answers the keydown itself, so
// forwarding the character as well delivered two carriage returns: the command
// ran and an extra prompt appeared. The same double-send made Tab emit two tabs,
// Backspace delete two characters and Escape send two escapes.
func TestEncodedKeySendsOneCopy(t *testing.T) {
	for _, tc := range []struct {
		name string
		vk   uintptr
		want string
	}{
		{"enter", vkReturn, "\r"},
		{"tab", vkTab, "\t"},
		{"backspace", vkBack, "\x7f"},
		{"escape", vkEscape, "\x1b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, fake := viewWithPane()
			// The keydown, then the character Windows produces from it.
			v.onKeyDown(tc.vk)
			v.onChar(uintptr(rune(fakeLastChar(tc.vk))))
			if got := string(fake.written); got != tc.want {
				t.Fatalf("shell received %q, want exactly %q", got, tc.want)
			}
		})
	}
}

// TestChordCharacterDoesNotReachTheShell is the regression for a shortcut
// leaking a control byte.
//
// A chord is translated by Windows like any other key press, so Ctrl+Shift+B
// arrives as WM_KEYDOWN followed by a WM_CHAR carrying STX, the same duplicate
// the encoded keys are guarded against, but for a key the application consumed
// rather than one it encoded. The shell must receive nothing at all: a stray
// byte at a prompt is invisible until the user presses Enter, and then it is
// part of the command line.
func TestChordCharacterDoesNotReachTheShell(t *testing.T) {
	v, fake := viewWithPane()
	// A binding whose action is consumed without touching the window, so the
	// test needs no window. Modifier state is read from the live keyboard,
	// which is neutral in a test, so the chord is the bare key.
	v.chords = map[chordKey]string{{key: "b"}: "detachTab"}

	if !v.onKeyDown('B') {
		t.Fatal("the chord was not consumed by the application")
	}
	v.onChar('b')
	if got := string(fake.written); got != "" {
		t.Fatalf("shell received %q from a chord, want nothing", got)
	}
}

// fakeLastChar is the character Windows derives from a key press, which is what
// onChar receives.
func fakeLastChar(vk uintptr) rune {
	switch vk {
	case vkReturn:
		return '\r'
	case vkTab:
		return '\t'
	case vkBack:
		return '\b'
	case vkEscape:
		return 0x1b
	}
	return 0
}

// TestPlainCharacterSendsOnce is the other half: a key the encoder does not
// encode must still deliver its character, and only once.
func TestPlainCharacterSendsOnce(t *testing.T) {
	v, fake := viewWithPane()
	v.onKeyDown('A')
	v.onChar('a')
	if got := string(fake.written); got != "a" {
		t.Fatalf("shell received %q, want %q", got, "a")
	}
}

// TestPendingKeyDoesNotLeakToTheNextKey guards the flag's lifetime. A key that
// encodes nothing must clear it rather than inherit the previous press, or the
// character that follows would be dropped instead of delivered.
func TestPendingKeyDoesNotLeakToTheNextKey(t *testing.T) {
	v, fake := viewWithPane()
	v.onKeyDown(vkReturn) // encoded: flag set, character dropped
	v.onChar('\r')
	v.onKeyDown('A') // not encoded: flag must be cleared
	v.onChar('a')    // and this must reach the shell
	if got := string(fake.written); got != "\ra" {
		t.Fatalf("shell received %q, want %q", got, "\ra")
	}
}

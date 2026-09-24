package history

import (
	"path/filepath"
	"testing"
)

// Commands are recovered from the keystrokes the user sends to the shell. These
// tests pin the line-editing behaviour that recovery depends on, because a
// mistake here silently records the wrong command or drops it.

func TestTypeRecordsCommandOnEnter(t *testing.T) {
	s := New()
	// The frontend sends one message per keystroke, so Type sees one rune at a
	// time; a paste arrives as one call carrying many.
	for _, r := range "echo hello" {
		s.Type("s1", string(r))
	}
	s.Type("s1", "\r")

	if got := s.List("s1"); len(got) != 1 || got[0] != "echo hello" {
		t.Fatalf("List = %q, want [echo hello]", got)
	}

	// Enter on an empty prompt must not invent an entry.
	s.Type("s1", "\r")
	if got := s.List("s1"); len(got) != 1 {
		t.Fatalf("List = %q, want the empty Enter to be ignored", got)
	}
}

// Type's return value is what the server pushes to the UI as it happens, so it
// must name the command exactly when one was recorded and be empty otherwise.
func TestTypeReturnsOnlyTheRecordedCommand(t *testing.T) {
	s := New()

	// Still typing: nothing is complete yet.
	if got := s.Type("s1", "echo par"); got != "" {
		t.Fatalf("Type(partial) = %q, want empty", got)
	}
	if got := s.Type("s1", "tial"); got != "" {
		t.Fatalf("Type(partial) = %q, want empty", got)
	}
	// Enter completes it, and the caller learns the command.
	if got := s.Type("s1", "\r"); got != "echo partial" {
		t.Fatalf("Type(enter) = %q, want echo partial", got)
	}
	// A blank line records nothing and must not be announced.
	if got := s.Type("s1", "\r"); got != "" {
		t.Fatalf("Type(blank enter) = %q, want empty", got)
	}
	// A repeat of the previous command is dropped, so it is not announced twice.
	if got := s.Type("s1", "echo partial\r"); got != "" {
		t.Fatalf("Type(repeat) = %q, want empty", got)
	}
}

// Every command typed in a session has to survive; recording only the first was
// the bug this feature was reported with.
func TestConsecutiveCommandsAllRecorded(t *testing.T) {
	s := New()
	for _, cmd := range []string{"echo one", "echo two", "echo three"} {
		for _, r := range cmd {
			s.Type("s1", string(r))
		}
		s.Type("s1", "\r")
	}

	got := s.List("s1")
	if len(got) != 3 || got[0] != "echo one" || got[2] != "echo three" {
		t.Fatalf("List = %q, want [echo one echo two echo three]", got)
	}

	// The panel asks newest first.
	got = s.Search("s1", "", 10)
	if len(got) != 3 || got[0] != "echo three" || got[2] != "echo one" {
		t.Fatalf("Search = %q, want newest first", got)
	}
}

// Backspace edits the pending line rather than the recorded one, and a multibyte
// character is removed whole.
func TestTypeBackspace(t *testing.T) {
	s := New()
	for _, r := range "echo hellp" {
		s.Type("s1", string(r))
	}
	s.Type("s1", "\x7f")
	s.Type("s1", "o\r")

	if got := s.List("s1"); len(got) != 1 || got[0] != "echo hello" {
		t.Fatalf("List = %q, want [echo hello]", got)
	}

	// "café" is c,a,f,é: two backspaces remove the accented character and the f,
	// and the accented character has to go in one step even though it is two
	// bytes on the wire.
	s.Type("s1", "ls café\x7f\x7f\r")
	if got := s.List("s1"); len(got) != 2 || got[1] != "ls ca" {
		t.Fatalf("List = %q, want the accented character removed whole", got)
	}
}

// Ctrl+C abandons the line, so the Enter that follows has nothing to run.
func TestTypeControlC(t *testing.T) {
	s := New()
	s.Type("s1", "echo nope")
	s.Type("s1", "\x03")
	s.Type("s1", "\r")
	if got := s.List("s1"); len(got) != 0 {
		t.Fatalf("List = %q, want Ctrl+C to abandon the line", got)
	}

	s.Type("s1", "echo fine\r")
	if got := s.List("s1"); len(got) != 1 || got[0] != "echo fine" {
		t.Fatalf("List = %q, want [echo fine]", got)
	}
}

// An arrow key recalls a line the terminal never typed, so the guess would be
// wrong; the buffer is dropped instead of recording a corrupted command.
func TestTypeEscapeSequenceDropsPendingLine(t *testing.T) {
	s := New()
	s.Type("s1", "echo partial")
	s.Type("s1", "\x1b[A") // Up: rewrites the line from the shell's own history.
	s.Type("s1", "\r")

	if got := s.List("s1"); len(got) != 0 {
		t.Fatalf("List = %q, want the guessed line to be discarded", got)
	}
}

func TestSearchFiltersCaseInsensitively(t *testing.T) {
	s := New()
	for _, cmd := range []string{"git status", "go build", "git log"} {
		s.Type("s1", cmd+"\r")
	}

	got := s.Search("s1", "GIT", 10)
	if len(got) != 2 || got[0] != "git log" || got[1] != "git status" {
		t.Fatalf("Search = %q, want newest first [git log git status]", got)
	}

	// A repeat of the command just run is not a new entry.
	s.Type("s1", "git log\r")
	if got := s.List("s1"); len(got) != 3 {
		t.Fatalf("List = %q, want the duplicate to be dropped", got)
	}
}

func TestSeedSurvivesClear(t *testing.T) {
	s := New()
	s.Seed("s1", []string{"older one", "older two"})
	if got := s.List("s1"); len(got) != 2 {
		t.Fatalf("after Seed List = %q, want 2 entries", got)
	}

	s.Clear("s1")
	if got := s.List("s1"); len(got) != 0 {
		t.Fatalf("after Clear List = %q, want empty", got)
	}

	// Recording still works after a clear, which is what makes the panel's Clear
	// button usable rather than terminal.
	s.Type("s1", "echo after\r")
	if got := s.List("s1"); len(got) != 1 || got[0] != "echo after" {
		t.Fatalf("List = %q, want [echo after]", got)
	}
}

func TestFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history")
	f := OpenFile(path)
	f.Append("first", "second")

	// A second File stands in for the next run of the program.
	got := OpenFile(path).Load()
	if len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("Load = %q, want [first second]", got)
	}

	// Appending again keeps what was already on disk, and a consecutive repeat is
	// not written twice.
	OpenFile(path).Append("second", "third")
	got = OpenFile(path).Load()
	if len(got) != 3 || got[2] != "third" {
		t.Fatalf("Load = %q, want [first second third]", got)
	}
}

// Package history records the commands a user runs in each terminal.
//
// The shell runs in a ConPTY, so the terminal only ever sees a stream of bytes:
// there is no shell hook that can report "the user just pressed Enter". Entries
// are therefore recovered from the keystrokes the user sends, which is exact and
// cheap: the bytes are already going through the server on their way to the
// shell, so recognising a command costs one pass over them and nothing else.
package history

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// MaxEntries bounds a single session's history. The terminal's own scrollback
// (10k lines by default) is not reachable from here, so this only has to hold
// the commands someone plausibly wants to recall.
const MaxEntries = 1000

// The pending buffer doubles as the escape-sequence state machine: while it holds
// one of these markers the bytes arriving belong to a sequence, not to the
// command, so they are consumed without being recorded. They are values no real
// line can hold, because clean rejects control characters.
const (
	escStart = "\x1b"  // an Escape has arrived; the kind is not known yet
	escCSI   = "\x1b[" // control sequence: runs to a final byte
	escOSC   = "\x1b]" // operating system command (window title): runs to BEL or ST
)

// escAdvance moves the escape-sequence state machine on by one byte and returns
// the next state, or "" once the sequence is complete.
func escAdvance(state string, r rune) string {
	switch state {
	case escStart:
		switch r {
		case '[':
			return escCSI
		case ']':
			return escOSC
		default:
			// A two-byte escape such as Alt+key; the sequence ends here.
			return ""
		}
	case escCSI:
		switch {
		case r >= 0x40 && r <= 0x7e:
			return "" // the final byte
		case r == '\r' || r == '\n' || r == 0x03:
			// Truncated by Enter or Ctrl+C; there is no line to record.
			return ""
		}
		return escCSI
	case escOSC:
		switch r {
		case 0x07:
			return "" // BEL terminates the title
		case '\r', '\n':
			return ""
		case 0x1b:
			return escStart // the next byte may be the ST terminator
		}
		return escOSC
	}
	return ""
}

// Store is a command history for every session id. It is safe for concurrent
// use: the websocket reader records typed keys while an HTTP request may be
// listing, and both touch the same maps.
type Store struct {
	mu      sync.Mutex
	entries map[string][]string
	partial map[string]string
}

func New() *Store {
	return &Store{
		entries: map[string][]string{},
		partial: map[string]string{},
	}
}

// Type records keystrokes the user sent to a session and returns the command
// that Enter completed, or "" when this batch completed nothing (still typing,
// a blank line, or a repeat of the command before it).
//
// Returning the single command rather than the list is what lets the owner push
// it to the UI as it happens: the payload is one short string instead of the
// session's whole history.
func (s *Store) Type(id, data string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	recorded := ""
	for _, r := range data {
		state := s.partial[id]
		if state == escStart || state == escCSI || state == escOSC {
			s.partial[id] = escAdvance(state, r)
			continue
		}
		switch {
		case r == 0x1b:
			// Escape introduces a sequence (arrow keys, Home/End, Alt). The shell
			// may rewrite the line as a result, so the guess made so far is
			// abandoned rather than recorded wrong; the sequence itself is
			// consumed by the state machine until it terminates.
			s.partial[id] = escStart
		case r == '\r' || r == '\n':
			if cmd := clean(s.partial[id]); cmd != "" {
				recorded = s.append(id, cmd)
			}
			s.partial[id] = ""
		case r == 0x7f || r == 0x08:
			// Backspace and delete: drop the last rune, not the last byte, so
			// multibyte input does not turn into garbage.
			p := s.partial[id]
			if p != "" {
				_, size := utf8.DecodeLastRuneInString(p)
				s.partial[id] = p[:len(p)-size]
			}
		case r == 0x03 || r == 0x15:
			// Ctrl+C / Ctrl+U abandon whatever was being typed.
			s.partial[id] = ""
		case unicode.IsPrint(r) || r == '\t':
			s.partial[id] += string(r)
		}
	}
	return recorded
}

// Observe is gone: commands are recorded from the keystrokes the user sends and
// nothing else.

// append records a command, dropping a consecutive duplicate and any blank. It
// returns the command when it was stored, and "" when it was dropped.
func (s *Store) append(id, cmd string) string {
	if cmd == "" {
		return ""
	}
	list := s.entries[id]
	if len(list) > 0 && list[len(list)-1] == cmd {
		return ""
	}
	list = append(list, cmd)
	if len(list) > MaxEntries {
		list = list[len(list)-MaxEntries:]
	}
	s.entries[id] = list
	return cmd
}

// Seed primes a session with previously saved commands, oldest first. It is
// used when a pane spawns so recall reaches commands from earlier runs.
func (s *Store) Seed(id string, cmds []string) {
	if len(cmds) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]string, 0, len(cmds))
	for _, cmd := range cmds {
		if cmd = clean(cmd); cmd != "" {
			list = append(list, cmd)
		}
	}
	if len(list) > MaxEntries {
		list = list[len(list)-MaxEntries:]
	}
	s.entries[id] = list
	s.partial[id] = ""
}

// List returns a session's history, oldest first.
func (s *Store) List(id string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.entries[id]
	out := make([]string, len(list))
	copy(out, list)
	return out
}

// Search returns up to limit commands containing query, newest first, which is
// the order a reverse history search wants to present them in.
func (s *Store) Search(id, query string, limit int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if limit <= 0 {
		limit = 50
	}
	needle := strings.ToLower(query)
	out := make([]string, 0, limit)
	list := s.entries[id]
	for i := len(list) - 1; i >= 0 && len(out) < limit; i-- {
		if needle == "" || strings.Contains(strings.ToLower(list[i]), needle) {
			out = append(out, list[i])
		}
	}
	return out
}

// Forget drops a session's history; the manager calls it when a shell exits so
// the map cannot grow without bound.
func (s *Store) Forget(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, id)
	delete(s.partial, id)
}

// Clear empties a session's history but keeps tracking it.
func (s *Store) Clear(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, id)
	s.partial[id] = ""
}

// File is the on-disk history, shared by every session.
type File struct {
	path string
	mu   sync.Mutex
}

// OpenFile points the shared history at path. An empty path disables saving.
func OpenFile(path string) *File { return &File{path: path} }

// Load returns the saved commands, newest last. A missing file is not an error.
func (f *File) Load() []string {
	if f == nil || f.path == "" {
		return nil
	}
	body, err := os.ReadFile(f.path)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(body), "\n") {
		if line = strings.TrimRight(line, "\r"); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// Append adds commands to the shared history, dropping consecutive duplicates.
func (f *File) Append(cmds ...string) {
	if f == nil || f.path == "" || len(cmds) == 0 {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	existing := f.Load()
	for _, cmd := range cmds {
		if cmd = clean(cmd); cmd == "" {
			continue
		}
		if len(existing) > 0 && existing[len(existing)-1] == cmd {
			continue
		}
		existing = append(existing, cmd)
	}
	if len(existing) > MaxEntries {
		existing = existing[len(existing)-MaxEntries:]
	}
	f.write(existing)
}

func (f *File) write(cmds []string) {
	if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
		return
	}
	body := strings.Join(cmds, "\n")
	if body != "" {
		body += "\n"
	}
	// Write-then-rename keeps a crash from truncating the existing history.
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, f.path)
}

// clean normalises a candidate command and rejects anything that is not
// plausibly one: control characters, prompt decorations, or pure whitespace.
func clean(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 4096 {
		return ""
	}
	for _, r := range s {
		if r < 0x20 && r != '\t' {
			return ""
		}
		if r == 0x7f {
			return ""
		}
	}
	return s
}
